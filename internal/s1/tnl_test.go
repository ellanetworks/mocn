package s1_test

import (
	"net/netip"
	"testing"

	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/sctptest"
	"github.com/ellanetworks/mocn/internal/tnl"
)

var (
	ranGW  = netip.MustParseAddr("10.1.0.1")
	coreGW = netip.MustParseAddr("127.0.0.1")
	sgw    = tlaOf("10.45.0.1")
	enbUP  = tlaOf("10.90.1.21")
)

func tlaOf(addr string) s1ap.TransportLayerAddress {
	a, err := tnl.Address(netip.MustParseAddr(addr))
	if err != nil {
		panic(err)
	}

	return a
}

func aggregatedGateway(t *testing.T, mme string) (*harness, *gtpu.Plane, *fakeMME) {
	t.Helper()

	return aggregatedGatewayWith(t, mme, sctptest.LoopbackHost{})
}

func aggregatedGatewayWith(t *testing.T, mme string, host gtpu.Host) (*harness, *gtpu.Plane, *fakeMME) {
	t.Helper()

	plane := gtpu.New(gtpu.Config{RANAddress: ranGW, RANInterface: "lo", Host: host})

	op := operatorA(mme)
	op.UserPlane = db.UserPlane{Aggregation: true}

	if err := plane.SetOperators([]gtpu.Operator{{ID: op.ID, Name: op.Name, Aggregated: true}}); err != nil {
		t.Fatal(err)
	}

	mmeListener := listenMME(t, mme)
	h := startGatewayWith(t, s1.Config{CorePort: mmeListener.Port(), Plane: plane}, op)

	return h, plane, mmeListener
}

func tunnelTo(t *testing.T, plane *gtpu.Plane, tla s1ap.TransportLayerAddress, teid s1ap.GTPTEID, local netip.Addr,
	ingress gtpu.Side,
) gtpu.Tunnel {
	t.Helper()

	addr, _ := netip.AddrFromSlice(tla.Bytes())
	if addr != local {
		t.Fatalf("relayed address = %v, want %v", addr, local)
	}

	tun, ok := plane.Get(uint32(teid))
	if !ok || tun.Local != local || tun.Ingress != ingress {
		t.Fatalf("tunnel %#x = %+v, %v", teid, tun, ok)
	}

	return tun
}

func setUpBearer(t *testing.T, enb, mme *peer, plane *gtpu.Plane, ue attached) {
	t.Helper()

	ics := contextSetup(ue, true)
	mme.Send(t, ics)

	f, raw := expectRaw(t, enb, "initiating", s1ap.ProcInitialContextSetup)

	req, err := s1ap.ParseInitialContextSetupRequest(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	item := req.ERABToBeSetup[0]
	ul := tunnelTo(t, plane, item.TransportLayerAddress, item.GTPTEID, ranGW, gtpu.RAN)

	if ul.Peer != (gtpu.Endpoint{Addr: sgw.IPv4(), TEID: 0x12345678}) || ul.Source != coreGW {
		t.Fatalf("uplink tunnel = %+v", ul)
	}

	golden := contextSetup(ue, true)
	golden.ERABToBeSetup[0].TransportLayerAddress, golden.ERABToBeSetup[0].GTPTEID = item.TransportLayerAddress, item.GTPTEID
	assertRelayed(t, golden, servingMME(t, f, 1, 1, ue.mmeUEID))

	resp := &s1ap.InitialContextSetupResponse{
		MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID),
		ERABSetup: []s1ap.ERABSetupItemCtxtSURes{{ERABID: 5, TransportLayerAddress: enbUP, GTPTEID: 0x77}},
	}
	enb.Send(t, resp)

	_, raw = expectRaw(t, mme, "successful", s1ap.ProcInitialContextSetup)

	got, err := s1ap.ParseInitialContextSetupResponse(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	dl := tunnelTo(t, plane, got.ERABSetup[0].TransportLayerAddress, got.ERABSetup[0].GTPTEID, coreGW, gtpu.Core)
	if dl.Peer != (gtpu.Endpoint{Addr: enbUP.IPv4(), TEID: 0x77}) || dl.Source != ranGW {
		t.Fatalf("downlink tunnel = %+v", dl)
	}

	if plane.Tunnels() != 2 {
		t.Fatalf("%d tunnels, want 2", plane.Tunnels())
	}
}

func TestAggregatedBearerReleasedWithTheUE(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.0.101")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	setUpBearer(t, enb, mme, plane, ue)

	mme.Send(t, &s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{UES1APIDPair: &s1ap.UES1APIDPair{MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID}},
		Cause:     s1ap.Ptr(s1ap.CauseNASNormalRelease.Cause()),
	})
	expectRaw(t, enb, "initiating", s1ap.ProcUEContextRelease)
	enb.Send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID)})
	expectRaw(t, mme, "successful", s1ap.ProcUEContextRelease)

	h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
}

func TestAggregatedTunnelsReleasedOnEveryPath(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	for name, release := range map[string]func(t *testing.T, h *harness, enb, mme *peer, ue attached){
		"radio lost": func(t *testing.T, _ *harness, enb, _ *peer, _ attached) {
			t.Helper()

			_ = enb.Conn.Abort()
		},
		"MME lost": func(t *testing.T, _ *harness, _, mme *peer, _ attached) {
			t.Helper()

			_ = mme.Conn.Abort()
		},
		"reset from MME": func(t *testing.T, _ *harness, _, mme *peer, _ attached) {
			t.Helper()
			mme.Send(t, &s1ap.Reset{
				Cause:     s1ap.Ptr(s1ap.CauseMiscControlProcessingOverload.Cause()),
				ResetType: s1ap.ResetType{S1Interface: s1ap.Ptr(s1ap.ResetAllResetAll)},
			})
		},
		"reset from radio": func(t *testing.T, _ *harness, enb, _ *peer, ue attached) {
			t.Helper()
			enb.Send(t, &s1ap.Reset{
				Cause:     s1ap.Ptr(s1ap.CauseMiscControlProcessingOverload.Cause()),
				ResetType: s1ap.ResetType{PartOfS1Interface: &s1ap.UEAssociatedLogicalS1ConnectionListRes{item(ue.ranMMEID, ue.enbUEID)}},
			})
		},
		"GW context release": func(t *testing.T, _ *harness, enb, _ *peer, ue attached) {
			t.Helper()
			enb.Send(t, &s1ap.UEContextReleaseRequest{
				MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID,
				Cause:                      s1ap.Ptr(s1ap.CauseRadioNetworkUnspecified.Cause()),
				GWContextReleaseIndication: new(s1ap.GWContextReleaseIndicationTrue),
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			ip := map[string]string{
				"radio lost": "127.0.0.102", "MME lost": "127.0.0.103", "reset from MME": "127.0.0.104",
				"reset from radio": "127.0.0.105", "GW context release": "127.0.0.106",
			}[name]

			h, plane, mmeListener := aggregatedGateway(t, ip)
			enb, mme := upAndRunning(t, h, mmeListener)
			ue := attach(t, enb, mme, 7, 1000)

			setUpBearer(t, enb, mme, plane, ue)
			release(t, h, enb, mme, ue)

			h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
		})
	}
}

func TestAggregatedERABSetupModifyRelease(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.0.107")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	setUpBearer(t, enb, mme, plane, ue)

	qos := s1ap.ERABLevelQoSParameters{QCI: 1, ARP: s1ap.AllocationAndRetentionPriority{
		PriorityLevel: 2, PreemptionCapability: s1ap.PreemptionShallNotTrigger,
		PreemptionVulnerability: s1ap.PreemptionNotPreemptable,
	}}

	mme.Send(t, &s1ap.ERABSetupRequest{
		MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID,
		ERABToBeSetup: []s1ap.ERABToBeSetupItemBearerSUReq{
			{ERABID: 6, QoS: qos, TransportLayerAddress: sgw, GTPTEID: 0x60, NASPDU: nas},
			{ERABID: 7, QoS: qos, TransportLayerAddress: sgw, GTPTEID: 0x70, NASPDU: nas},
		},
	})
	expectRaw(t, enb, "initiating", s1ap.ProcERABSetup)

	if plane.Tunnels() != 4 {
		t.Fatalf("%d tunnels after E-RAB SETUP REQUEST, want 4", plane.Tunnels())
	}

	enb.Send(t, &s1ap.ERABSetupResponse{
		MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID),
		ERABSetup:         []s1ap.ERABSetupItemBearerSURes{{ERABID: 6, TransportLayerAddress: enbUP, GTPTEID: 0x61}},
		ERABFailedToSetup: []s1ap.ERABItem{{ERABID: 7, Cause: s1ap.CauseRadioNetworkUnspecified.Cause()}},
	})
	expectRaw(t, mme, "successful", s1ap.ProcERABSetup)

	if plane.Tunnels() != 4 {
		t.Fatalf("%d tunnels after E-RAB SETUP RESPONSE, want 4: the failed E-RAB's tunnel is freed", plane.Tunnels())
	}

	mme.Send(t, &s1ap.ERABModifyRequest{
		MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID,
		ERABToBeModified: []s1ap.ERABToBeModifiedItemBearerModReq{{
			ERABID: 6, QoS: qos, NASPDU: nas,
			TransportInformation: &s1ap.TransportInformation{TransportLayerAddress: tlaOf("10.45.0.9"), ULGTPTEID: 0x99},
		}},
	})

	_, raw := expectRaw(t, enb, "initiating", s1ap.ProcERABModify)

	mod, err := s1ap.ParseERABModifyRequest(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	ti := mod.ERABToBeModified[0].TransportInformation
	if tun := tunnelTo(t, plane, ti.TransportLayerAddress, ti.ULGTPTEID, ranGW, gtpu.RAN); tun.Peer.TEID != 0x99 {
		t.Fatalf("new uplink tunnel = %+v", tun)
	}

	enb.Send(t, &s1ap.ERABModifyResponse{
		MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID),
		ERABModify: []s1ap.ERABModifyItemBearerModRes{{ERABID: 6}},
	})
	expectRaw(t, mme, "successful", s1ap.ProcERABModify)

	if plane.Tunnels() != 5 {
		t.Fatalf("%d tunnels after E-RAB MODIFY, want 5: the old uplink tunnel is kept", plane.Tunnels())
	}

	mme.Send(t, &s1ap.ERABReleaseCommand{
		MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID,
		ERABToBeReleased: []s1ap.ERABItem{{ERABID: 6, Cause: s1ap.CauseNASNormalRelease.Cause()}},
	})
	expectRaw(t, enb, "initiating", s1ap.ProcERABRelease)

	enb.Send(t, &s1ap.ERABReleaseResponse{
		MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID),
		ERABReleased: []s1ap.ERABReleaseItemBearerRelComp{{ERABID: 6}},
	})
	expectRaw(t, mme, "successful", s1ap.ProcERABRelease)

	if plane.Tunnels() != 2 {
		t.Fatalf("%d tunnels after E-RAB RELEASE, want 2", plane.Tunnels())
	}

	enb.Send(t, &s1ap.ERABReleaseIndication{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID,
		ERABReleased: []s1ap.ERABItem{{ERABID: 5, Cause: s1ap.CauseRadioNetworkUnspecified.Cause()}},
	})
	expectRaw(t, mme, "initiating", s1ap.ProcERABReleaseIndication)

	if plane.Tunnels() != 0 {
		t.Fatalf("%d tunnels after E-RAB RELEASE INDICATION, want 0", plane.Tunnels())
	}
}

func TestAggregatedFailsClosed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.0.108")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	ics := contextSetup(ue, true)
	ics.ERABToBeSetup[0].TransportLayerAddress = tlaOf("2001:db8::1")
	mme.Send(t, ics)

	expectReset(t, mme, s1ap.UEAssociatedLogicalS1ConnectionItem{
		MMEUES1APID: new(ue.mmeUEID), ENBUES1APID: new(ue.mmeENBID),
	})
	expectReset(t, enb, item(ue.ranMMEID, ue.enbUEID))

	if plane.Tunnels() != 0 || len(h.gw.UEs()) != 0 {
		t.Fatalf("%d tunnels, %d UEs after a transport address that cannot be relayed", plane.Tunnels(), len(h.gw.UEs()))
	}
}

func TestAggregatedUnknownExtensionFailsClosed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.0.109")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	mme.Send(t, contextSetup(ue, true))
	expectRaw(t, enb, "initiating", s1ap.ProcInitialContextSetup)

	withExtension, err := per.Marshal(&s1ap.ERABToBeSwitchedDLItem{
		ERABID: 5, TransportLayerAddress: enbUP, GTPTEID: 0x77,
		SecurityIndication: &s1ap.SecurityIndication{IntegrityProtectionIndication: s1ap.IntegrityProtectionRequired},
	}, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	var res s1ap.ERABSetupItemCtxtSURes
	if err := per.Unmarshal(withExtension, &res, per.Aligned); err != nil {
		t.Fatal(err)
	}

	enb.Send(t, &s1ap.InitialContextSetupResponse{
		MMEUES1APID: new(ue.ranMMEID), ENBUES1APID: new(ue.enbUEID), ERABSetup: []s1ap.ERABSetupItemCtxtSURes{res},
	})

	expectReset(t, mme, s1ap.UEAssociatedLogicalS1ConnectionItem{
		MMEUES1APID: new(ue.mmeUEID), ENBUES1APID: new(ue.mmeENBID),
	})

	h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
}

func TestAggregatedHandoverHairpinsDirectForwarding(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.0.110")
	enb1, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb1, mme, 3, 300)

	setUpBearer(t, enb1, mme, plane, ue)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	mme.Send(t, handoverRequest(t, 900, 2<<8|0x01))

	_, raw := expectRaw(t, enb2, "initiating", s1ap.ProcHandoverResourceAllocation)

	req, err := s1ap.ParseHandoverRequest(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	tunnelTo(t, plane, req.ERABToBeSetup[0].TransportLayerAddress, req.ERABToBeSetup[0].GTPTEID, ranGW, gtpu.RAN)

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
	tunnelTo(t, plane, admitted.TransportLayerAddress, admitted.GTPTEID, coreGW, gtpu.Core)
	fwd := tunnelTo(t, plane, *admitted.DLTransportLayerAddr, *admitted.DLGTPTEID, coreGW, gtpu.Core)

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

	f := cmd.ERABSubjecttoDataForwarding[0]

	hairpin := tunnelTo(t, plane, *f.DLTransportLayerAddr, *f.DLGTPTEID, ranGW, gtpu.RAN)
	if hairpin.Peer != fwd.Peer || hairpin.Source != ranGW {
		t.Fatalf("hairpin = %+v, want toward %+v from the radio side", hairpin, fwd.Peer)
	}

	enb1.Send(t, &s1ap.HandoverCancel{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID,
		Cause: s1ap.Ptr(s1ap.CauseRadioNetworkHandoverCancelled.Cause()),
	})
	expectRaw(t, mme, "initiating", s1ap.ProcHandoverCancel)

	if _, ok := plane.Get(hairpin.TEID); ok {
		t.Fatal("forwarding tunnel kept after HANDOVER CANCEL")
	}
}

func TestAggregatedPathSwitchKeepsTheUplink(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, mmeListener := aggregatedGateway(t, "127.0.0.111")
	enb1, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb1, mme, 3, 300)

	setUpBearer(t, enb1, mme, plane, ue)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	ps := pathSwitchRequest(t, 10, ue.mmeUEID, new(gummei(t, 1)))
	ps.ERABToBeSwitchedDL = []s1ap.ERABToBeSwitchedDLItem{{ERABID: 5, TransportLayerAddress: tlaOf("10.90.1.22"), GTPTEID: 0x88}}
	enb2.Send(t, ps)

	_, raw := expectRaw(t, mme, "initiating", s1ap.ProcPathSwitchRequest)
	_, mmeENBID := ids(t, raw)

	req, err := s1ap.ParsePathSwitchRequest(valueOf(t, raw))
	if err != nil {
		t.Fatal(err)
	}

	tunnelTo(t, plane, req.ERABToBeSwitchedDL[0].TransportLayerAddress, req.ERABToBeSwitchedDL[0].GTPTEID, coreGW, gtpu.Core)

	mme.Send(t, &s1ap.PathSwitchRequestAcknowledge{
		MMEUES1APID: &ue.mmeUEID, ENBUES1APID: &mmeENBID,
		SecurityContext: s1ap.SecurityContext{NextHopChainingCount: 2, NextHopParameter: securityKey()},
	})
	expectRaw(t, enb2, "successful", s1ap.ProcPathSwitchRequest)
	expectRaw(t, enb1, "initiating", s1ap.ProcUEContextRelease)

	enb1.Send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: &ue.ranMMEID, ENBUES1APID: &ue.enbUEID})

	h.eventually(t, "old downlink freed, uplink kept", func() bool { return plane.Tunnels() == 2 })

	wantTrafficMovedToTheTarget(t, h, plane, enb1)
}

func valueOf(t *testing.T, p *s1ap.RawPDU) []byte {
	t.Helper()

	pdu, err := s1ap.Unmarshal(bodyOfRawPDU(t, p))
	if err != nil {
		t.Fatal(err)
	}

	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		return m.Value
	case *s1ap.SuccessfulOutcome:
		return m.Value
	case *s1ap.UnsuccessfulOutcome:
		return m.Value
	}

	t.Fatal("no message value")

	return nil
}
