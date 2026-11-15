package s1_test

import (
	"slices"
	"testing"

	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/sctptest"
	"github.com/prometheus/client_golang/prometheus"
)

type marshalFunc func() ([]byte, error)

func (f marshalFunc) Marshal() ([]byte, error) { return f() }

func withIEs(t *testing.T, msg interface{ Marshal() ([]byte, error) }, ies ...s1ap.RawIE) []byte {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	raw, err := s1ap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	for _, ie := range ies {
		if err := raw.Insert(raw.Len(), ie); err != nil {
			t.Fatal(err)
		}
	}

	out, err := raw.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func tlaIE(t *testing.T, id s1ap.ProtocolIEID, tla s1ap.TransportLayerAddress) s1ap.RawIE {
	t.Helper()

	v, err := per.Marshal(&tla, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	return s1ap.RawIE{ID: id, Criticality: s1ap.CriticalityIgnore, Value: v}
}

func addERAB(t *testing.T, enb, mme *peer, ue attached, id s1ap.ERABID, ul, dl s1ap.GTPTEID) {
	t.Helper()

	qos := s1ap.ERABLevelQoSParameters{QCI: 9, ARP: s1ap.AllocationAndRetentionPriority{
		PriorityLevel: 2, PreemptionCapability: s1ap.PreemptionShallNotTrigger,
		PreemptionVulnerability: s1ap.PreemptionNotPreemptable,
	}}

	mme.Send(t, &s1ap.ERABSetupRequest{
		MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID,
		ERABToBeSetup: []s1ap.ERABToBeSetupItemBearerSUReq{
			{ERABID: id, QoS: qos, TransportLayerAddress: sgw, GTPTEID: ul, NASPDU: nas},
		},
	})
	expectRaw(t, enb, "initiating", s1ap.ProcERABSetup)

	enb.Send(t, &s1ap.ERABSetupResponse{
		MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID),
		ERABSetup: []s1ap.ERABSetupItemBearerSURes{{ERABID: id, TransportLayerAddress: enbUP, GTPTEID: dl}},
	})
	expectRaw(t, mme, "successful", s1ap.ProcERABSetup)
}

func TestAggregatedERABModificationIndication(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.1")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	setUpBearer(t, enb, mme, plane, ue)
	addERAB(t, enb, mme, ue, 6, 0x60, 0x61)

	if plane.Tunnels() != 4 {
		t.Fatalf("%d tunnels, want 4", plane.Tunnels())
	}

	moved := tlaOf("10.90.1.31")
	ind := &s1ap.ERABModificationIndication{
		MMEUES1APID:     ue.ranMMEID,
		ENBUES1APID:     ue.enbUEID,
		ToBeModified:    s1ap.ERABToBeModifiedListBearerModInd{{ERABID: 5, TransportLayerAddress: moved, DLGTPTEID: 0x78}},
		NotToBeModified: s1ap.ERABNotToBeModifiedListBearerModInd{{ERABID: 6, TransportLayerAddress: enbUP, DLGTPTEID: 0x61}},
	}
	enb.Send(t, ind)

	_, raw := expectRaw(t, mme, "initiating", s1ap.ProcERABModificationIndication)

	got, err := s1ap.ParseERABModificationIndication(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	mod := got.ToBeModified[0]
	if tun := tunnelTo(t, plane, mod.TransportLayerAddress, mod.DLGTPTEID, coreGW, gtpu.Core); tun.Peer !=
		(gtpu.Endpoint{Addr: moved.IPv4(), TEID: 0x78}) {
		t.Fatalf("modified downlink = %+v", tun)
	}

	kept := got.NotToBeModified[0]
	if tun := tunnelTo(t, plane, kept.TransportLayerAddress, kept.DLGTPTEID, coreGW, gtpu.Core); tun.Peer !=
		(gtpu.Endpoint{Addr: enbUP.IPv4(), TEID: 0x61}) {
		t.Fatalf("unchanged downlink = %+v", tun)
	}

	if plane.Tunnels() != 5 {
		t.Fatalf("%d tunnels after E-RAB MODIFICATION INDICATION, want 5: the unchanged E-RAB keeps its tunnel", plane.Tunnels())
	}

	mme.Send(t, &s1ap.ERABModificationConfirm{
		MMEUES1APID: new(ue.mmeUEID), ENBUES1APID: new(ue.mmeENBID),
		ModifiedERABs: []s1ap.ERABID{5},
		ToBeReleased:  []s1ap.ERABItem{{ERABID: 6, Cause: s1ap.CauseRadioNetworkUnspecified.Cause()}},
	})
	expectRaw(t, enb, "successful", s1ap.ProcERABModificationIndication)

	if _, ok := plane.Get(uint32(kept.DLGTPTEID)); ok {
		t.Fatal("tunnel of an E-RAB in the E-RAB To Be Released List kept")
	}

	if _, ok := plane.Get(uint32(mod.DLGTPTEID)); !ok {
		t.Fatal("modified downlink freed after E-RAB MODIFICATION CONFIRM")
	}

	if plane.Tunnels() != 3 {
		t.Fatalf("%d tunnels after E-RAB MODIFICATION CONFIRM, want 3", plane.Tunnels())
	}
}

func TestAggregatedInitialContextSetupFailureFreesTunnels(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.2")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	mme.Send(t, contextSetup(ue, true))
	expectRaw(t, enb, "initiating", s1ap.ProcInitialContextSetup)

	if plane.Tunnels() != 1 {
		t.Fatalf("%d tunnels, want 1", plane.Tunnels())
	}

	enb.Send(t, &s1ap.InitialContextSetupFailure{
		MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID),
		Cause: s1ap.Ptr(s1ap.CauseRadioNetworkUnspecified.Cause()),
	})
	expectRaw(t, mme, "unsuccessful", s1ap.ProcInitialContextSetup)

	if plane.Tunnels() != 0 {
		t.Fatalf("%d tunnels after INITIAL CONTEXT SETUP FAILURE, want 0", plane.Tunnels())
	}
}

func TestAggregatedHandoverFailureFreesTargetTunnels(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.3")
	enb1, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb1, mme, 3, 300)

	setUpBearer(t, enb1, mme, plane, ue)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	mme.Send(t, handoverRequest(t, 900, 2<<8|0x01))

	_, raw := expectRaw(t, enb2, "initiating", s1ap.ProcHandoverResourceAllocation)
	targetMMEID, _, _ := raw.MMEUES1APID()

	if plane.Tunnels() != 3 {
		t.Fatalf("%d tunnels after HANDOVER REQUEST, want 3", plane.Tunnels())
	}

	enb2.Send(t, &s1ap.HandoverFailure{
		MMEUES1APID: &targetMMEID, Cause: s1ap.Ptr(s1ap.CauseRadioNetworkUnspecified.Cause()),
	})
	expectRaw(t, mme, "unsuccessful", s1ap.ProcHandoverResourceAllocation)

	h.eventually(t, "target tunnels freed", func() bool { return plane.Tunnels() == 2 })
}

func TestAggregatedCompletedHandoverFreesSourceTunnels(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.4")
	enb1, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb1, mme, 3, 300)

	setUpBearer(t, enb1, mme, plane, ue)

	source := plane.Tunnels()

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	mme.Send(t, handoverRequest(t, 900, 2<<8|0x01))

	_, raw := expectRaw(t, enb2, "initiating", s1ap.ProcHandoverResourceAllocation)
	targetMMEID, _, _ := raw.MMEUES1APID()
	target := tlaOf("10.90.1.22")

	enb2.Send(t, &s1ap.HandoverRequestAcknowledge{
		MMEUES1APID: &targetMMEID, ENBUES1APID: new(s1ap.ENBUES1APID(21)),
		ERABAdmitted: []s1ap.ERABAdmittedItem{{
			ERABID: 5, TransportLayerAddress: target, GTPTEID: 9, DLTransportLayerAddr: &target, DLGTPTEID: new(s1ap.GTPTEID(10)),
		}},
		TargetToSource: s1ap.TransparentContainer{0xcc},
	})

	_, raw = expectRaw(t, mme, "successful", s1ap.ProcHandoverResourceAllocation)

	ack, err := s1ap.ParseHandoverRequestAcknowledge(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	admitted := ack.ERABAdmitted[0]

	mme.Send(t, &s1ap.HandoverCommand{
		MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID, HandoverType: s1ap.HandoverTypeIntraLTE,
		ERABSubjecttoDataForwarding: []s1ap.ERABDataForwardingItem{{
			ERABID: 5, DLTransportLayerAddr: admitted.DLTransportLayerAddr, DLGTPTEID: admitted.DLGTPTEID,
		}},
		TargetToSource: s1ap.TransparentContainer{0xcc},
	})

	_, raw = expectRaw(t, enb1, "successful", s1ap.ProcHandoverPreparation)

	cmd, err := s1ap.ParseHandoverCommand(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	hairpin := uint32(*cmd.ERABSubjecttoDataForwarding[0].DLGTPTEID)

	enb2.Send(t, &s1ap.HandoverNotify{
		MMEUES1APID: targetMMEID, ENBUES1APID: 21,
		EUTRANCGI: &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 2<<8 | 0x01},
		TAI:       &s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
	})
	expectRaw(t, mme, "initiating", s1ap.ProcHandoverNotification)

	before := plane.Tunnels()

	mme.Send(t, &s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{UES1APIDPair: &s1ap.UES1APIDPair{MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID}},
		Cause:     s1ap.Ptr(s1ap.CauseRadioNetworkSuccessfulHandover.Cause()),
	})
	expectRaw(t, enb1, "initiating", s1ap.ProcUEContextRelease)
	enb1.Send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID)})
	expectRaw(t, mme, "successful", s1ap.ProcUEContextRelease)

	h.eventually(t, "source tunnels freed", func() bool { return plane.Tunnels() == before-source-1 })

	if _, ok := plane.Get(hairpin); ok {
		t.Fatal("source forwarding tunnel kept after the source UE context release")
	}

	wantTrafficMovedToTheTarget(t, h, plane, enb1)
}

func TestAggregatedStripsUnsupportedTransportInformation(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.5")
	enb, mme := upAndRunning(t, h, mmeListener)

	const (
		idGWTLA       s1ap.ProtocolIEID = 155
		idBBF         s1ap.ProtocolIEID = 176
		idSIPTOLGWTLA s1ap.ProtocolIEID = 184
	)

	lgw := tlaOf("10.90.1.99")

	enb.SendRaw(t, withIEs(t, initialUE(t, 7), tlaIE(t, idGWTLA, lgw), tlaIE(t, idSIPTOLGWTLA, lgw)))

	_, raw := expectRaw(t, mme, "initiating", s1ap.ProcInitialUEMessage)
	for _, id := range []s1ap.ProtocolIEID{idGWTLA, idSIPTOLGWTLA} {
		if indexOf(t, raw, id) >= 0 {
			t.Errorf("INITIAL UE MESSAGE relayed with IE %d", id)
		}
	}

	_, mmeENBID := ids(t, raw)
	mme.Send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 1000, ENBUES1APID: mmeENBID, NASPDU: nas})

	_, raw = expectRaw(t, enb, "initiating", s1ap.ProcDownlinkNASTransport)
	ranMMEID, _ := ids(t, raw)
	ue := attached{enbUEID: 7, ranMMEID: ranMMEID, mmeENBID: mmeENBID, mmeUEID: 1000}

	enb.SendRaw(t, withIEs(t, &s1ap.UplinkNASTransport{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID, NASPDU: nas,
		EUTRANCGI: &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 0x19b01},
		TAI:       &s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
	},
		tlaIE(t, idGWTLA, lgw), tlaIE(t, idSIPTOLGWTLA, lgw)))

	_, raw = expectRaw(t, mme, "initiating", s1ap.ProcUplinkNASTransport)
	for _, id := range []s1ap.ProtocolIEID{idGWTLA, idSIPTOLGWTLA} {
		if indexOf(t, raw, id) >= 0 {
			t.Errorf("UPLINK NAS TRANSPORT relayed with IE %d", id)
		}
	}

	setUpBearer(t, enb, mme, plane, ue)

	bbf := s1ap.RawIE{ID: idBBF, Criticality: s1ap.CriticalityIgnore, Value: []byte{0x00}}

	enb.SendRaw(t, withIEs(t, &s1ap.ERABModificationIndication{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID,
		ToBeModified: s1ap.ERABToBeModifiedListBearerModInd{{ERABID: 5, TransportLayerAddress: enbUP, DLGTPTEID: 0x79}},
	}, bbf))

	_, raw = expectRaw(t, mme, "initiating", s1ap.ProcERABModificationIndication)
	if indexOf(t, raw, idBBF) >= 0 {
		t.Error("E-RAB MODIFICATION INDICATION relayed with Tunnel Information for BBF")
	}
}

func TestAggregatedStripsCDMA2000DataForwarding(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.6")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	setUpBearer(t, enb, mme, plane, ue)

	dl, err := (&s1ap.DownlinkNASTransport{MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID, NASPDU: nas}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	pdu, err := s1ap.Unmarshal(dl)
	if err != nil {
		t.Fatal(err)
	}

	init, ok := pdu.(*s1ap.InitiatingMessage)
	if !ok {
		t.Fatalf("%T", pdu)
	}

	init.ProcedureCode = s1ap.ProcDownlinkS1cdma2000tunnelling

	fwd, err := per.Marshal(&s1ap.ERABSubjecttoDataForwardingList{{
		ERABID: 5, DLTransportLayerAddr: &sgw, DLGTPTEID: new(s1ap.GTPTEID(0x55)),
	}}, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	mme.SendRaw(t, withIEs(t, marshalFunc(func() ([]byte, error) { return s1ap.Marshal(init) }), s1ap.RawIE{
		ID: s1ap.IDERABSubjecttoDataForwardingList, Criticality: s1ap.CriticalityIgnore, Value: fwd,
	}))

	_, raw := expectRaw(t, enb, "initiating", s1ap.ProcDownlinkS1cdma2000tunnelling)
	if indexOf(t, raw, s1ap.IDERABSubjecttoDataForwardingList) >= 0 {
		t.Fatal("DOWNLINK S1 CDMA2000 TUNNELLING relayed with an E-RAB Subject to Data Forwarding List")
	}

	if plane.Tunnels() != 2 {
		t.Fatalf("%d tunnels, want 2", plane.Tunnels())
	}
}

func TestAggregatedTunnelsReleasedWhenTheOperatorIsDeleted(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.7")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	setUpBearer(t, enb, mme, plane, ue)

	h.Ops.Set()

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
}

func radioDirections(t *testing.T, plane *gtpu.Plane, radio string) []string {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(plane.Collectors()...)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, f := range families {
		if f.GetName() != "ellamocn_user_plane_packets_total" {
			continue
		}

		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}

			if labels["radio"] == radio {
				out = append(out, labels["direction"])
			}
		}
	}

	slices.Sort(out)

	return out
}

const (
	enb1Ref = "eNB:001-01:MacroeNB-00001"
	enb2Ref = "eNB:001-01:MacroeNB-00002"
)

func wantTrafficMovedToTheTarget(t *testing.T, h *harness, plane *gtpu.Plane, source *peer) {
	t.Helper()

	if got := radioDirections(t, plane, enb2Ref); !slices.Equal(got, []string{"downlink", "uplink"}) {
		t.Fatalf("target radio series = %v, want both directions", got)
	}

	if got := radioDirections(t, plane, enb1Ref); !slices.Equal(got, []string{"downlink", "uplink"}) {
		t.Fatalf("source radio series = %v, want its earlier traffic in both directions", got)
	}

	if err := source.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	h.eventually(t, "source radio series removed", func() bool { return len(radioDirections(t, plane, enb1Ref)) == 0 })

	if got := radioDirections(t, plane, enb2Ref); !slices.Equal(got, []string{"downlink", "uplink"}) {
		t.Fatalf("target radio series after the source left = %v", got)
	}
}

func TestAggregatedRadioSeriesRemovedWhenTheRadioLeaves(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.2.16")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	setUpBearer(t, enb, mme, plane, ue)

	if got := radioDirections(t, plane, enb1Ref); len(got) == 0 {
		t.Fatalf("no series of radio %s", enb1Ref)
	}

	if err := enb.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	h.eventually(t, "no series of the radio that left", func() bool {
		return plane.Tunnels() == 0 && len(radioDirections(t, plane, enb1Ref)) == 0
	})
}

type movedHost struct{ sctptest.LoopbackHost }

func (movedHost) InterfaceName(int) (string, error) { return "xfrm-gone", nil }

func TestAggregatedSessionRefusedWhileTheUserPlaneIsUnavailable(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGatewayWith(t, "127.0.2.9", movedHost{})
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	if st, _ := plane.Status(operatorA().ID); st.State != gtpu.StateUnavailable || st.Reason != gtpu.ReasonRouteMismatch {
		t.Fatalf("user plane = %+v, want unavailable", st)
	}

	mme.Send(t, contextSetup(ue, true))

	if r := expect(t, enb, "initiating", s1ap.ProcReset, s1ap.ParseReset); r.Cause == nil ||
		!r.Cause.Equal(s1ap.CauseTransportResourceUnavailable.Cause()) {
		t.Fatalf("Reset to the eNB = %+v, want transport resource unavailable", r)
	}

	expect(t, mme, "initiating", s1ap.ProcReset, s1ap.ParseReset)

	if plane.Tunnels() != 0 {
		t.Fatalf("%d tunnels, want none", plane.Tunnels())
	}

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(plane.Collectors()...)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range families {
		if f.GetName() != "ellamocn_user_plane_tunnel_setup_failures_total" {
			continue
		}

		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}

			if labels["operator"] == "A" && labels["interface"] == "s1" &&
				labels["procedure"] == "initial_context_setup" &&
				labels["reason"] == "user_plane_unavailable" && m.GetCounter().GetValue() == 1 {
				return
			}
		}
	}

	t.Fatalf("no tunnel setup failure counted for the unavailable operator: %v", families)
}
