package ng_test

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/sctptest"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	ranGW  = netip.MustParseAddr("10.1.0.1")
	coreGW = netip.MustParseAddr("127.0.0.1")
	upf    = netip.MustParseAddr("10.45.0.1")
	gnbUP  = netip.MustParseAddr("10.90.1.23")
)

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

func wantTrafficMovedToTheTarget(t *testing.T, h *harness, plane *gtpu.Plane, source *peer, from, to string) {
	t.Helper()

	both := []string{"downlink", "uplink"}

	if got := radioDirections(t, plane, to); !slices.Equal(got, both) {
		t.Fatalf("target radio %s series = %v, want both directions", to, got)
	}

	if got := radioDirections(t, plane, from); !slices.Equal(got, both) {
		t.Fatalf("source radio %s series = %v, want its earlier traffic in both directions", from, got)
	}

	if err := source.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	h.eventually(t, "source radio series removed", func() bool { return len(radioDirections(t, plane, from)) == 0 })

	if got := radioDirections(t, plane, to); !slices.Equal(got, both) {
		t.Fatalf("target radio %s series after the source left = %v", to, got)
	}
}

func tunnel(a netip.Addr, teid ngap.GTPTEID) ngap.UPTransportLayerInformation {
	return ngap.UPTransportLayerInformation{GTPTunnel: &ngap.GTPTunnel{TransportLayerAddress: tla(a), GTPTEID: teid}}
}

func aggregatedGateway(t *testing.T, amf string) (*harness, *gtpu.Plane, *fakeAMF) {
	t.Helper()

	plane := gtpu.New(gtpu.Config{RANAddress: ranGW, RANInterface: "lo", Host: sctptest.LoopbackHost{}})

	op := operatorA(amf)
	op.UserPlane = db.UserPlane{Aggregation: true}

	if err := plane.SetOperators([]gtpu.Operator{{ID: op.ID, Name: op.Name, Aggregated: true}}); err != nil {
		t.Fatal(err)
	}

	amfs := listenAMF(t, amf)
	h := startGatewayWith(t, ng.Config{CorePort: amfs.Port(), Plane: plane}, op)

	return h, plane, amfs
}

func endpointOf(t *testing.T, plane *gtpu.Plane, u ngap.UPTransportLayerInformation, local netip.Addr, ingress gtpu.Side) gtpu.Tunnel {
	t.Helper()

	addr, _ := netip.AddrFromSlice(u.GTPTunnel.TransportLayerAddress.Bytes())
	if addr != local {
		t.Fatalf("relayed address = %v, want %v", addr, local)
	}

	tun, ok := plane.Get(uint32(u.GTPTunnel.GTPTEID))
	if !ok || tun.Ingress != ingress {
		t.Fatalf("tunnel %#x = %+v, %v", u.GTPTunnel.GTPTEID, tun, ok)
	}

	return tun
}

func setupRequestTransfer(t *testing.T, ul ngap.UPTransportLayerInformation) ngap.TransferContainer {
	t.Helper()

	b, err := (&ngap.PDUSessionResourceSetupRequestTransfer{
		ULNGUUPTNLInformation: ul,
		PDUSessionType:        ngap.PDUSessionTypeIPv4,
		QosFlowSetupRequest: ngap.QosFlowSetupRequestList{{
			QosFlowIdentifier: 1,
			QosFlowLevelQosParameters: ngap.QosFlowLevelQosParameters{
				QosCharacteristics:             ngap.QosCharacteristics{NonDynamic5QI: &ngap.NonDynamic5QIDescriptor{FiveQI: 9}},
				AllocationAndRetentionPriority: ngap.AllocationAndRetentionPriority{PriorityLevelARP: 1},
			},
		}},
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func setupResponseTransfer(t *testing.T, dl ngap.UPTransportLayerInformation) ngap.TransferContainer {
	t.Helper()

	b, err := (&ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformation{
			UPTransportLayerInformation: dl, AssociatedQosFlowList: ngap.AssociatedQosFlowList{{QosFlowIdentifier: 1}},
		},
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func setUpSession(t *testing.T, gnb, amf *peer, plane *gtpu.Plane, ue attached) gtpu.Tunnel {
	t.Helper()

	amf.Send(t, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: setupRequestTransfer(t, tunnel(upf, 0x100)),
		}},
	})

	req := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceSetup, ngap.ParsePDUSessionResourceSetupRequest)

	tr, err := ngap.ParsePDUSessionResourceSetupRequestTransfer(req.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	if ul := endpointOf(t, plane, tr.ULNGUUPTNLInformation, ranGW, gtpu.RAN); ul.Peer != (gtpu.Endpoint{Addr: upf, TEID: 0x100}) {
		t.Fatalf("uplink tunnel = %+v", ul)
	}

	golden := setupRequestTransfer(t, tr.ULNGUUPTNLInformation)
	if string(golden) != string(req.PDUSessionResourceSetup[0].Transfer) {
		t.Fatalf("relayed transfer = %x, want %x", req.PDUSessionResourceSetup[0].Transfer, golden)
	}

	gnb.Send(t, &ngap.PDUSessionResourceSetupResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSURes{{
			PDUSessionID: 1, Transfer: setupResponseTransfer(t, tunnel(gnbUP, 0x200)),
		}},
	})

	resp := expect(t, amf, "successful", ngap.ProcPDUSessionResourceSetup, ngap.ParsePDUSessionResourceSetupResponse)

	rt, err := ngap.ParsePDUSessionResourceSetupResponseTransfer(resp.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	dl := endpointOf(t, plane, rt.DLQosFlowPerTNLInformation.UPTransportLayerInformation, coreGW, gtpu.Core)
	if dl.Peer != (gtpu.Endpoint{Addr: gnbUP, TEID: 0x200}) {
		t.Fatalf("downlink tunnel = %+v", dl)
	}

	return dl
}

func TestAggregatedSessionModifyAndRelease(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.1.201")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	dl := setUpSession(t, gnb, amf, plane, ue)

	modify, err := (&ngap.PDUSessionResourceModifyRequestTransfer{
		ULNGUUPTNLModify: ngap.ULNGUUPTNLModifyList{{
			ULNGUUPTNLInformation: tunnel(upf, 0x101),
			DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dl.TEID)),
		}},
		UserPlaneFailureIndication: &ngap.UserPlaneFailureIndication{
			ULNGUUPTNLInformation: tunnel(upf, 0x101),
			DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dl.TEID)),
		},
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	amf.Send(t, &ngap.PDUSessionResourceModifyRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceModify: ngap.PDUSessionResourceModifyListModReq{{PDUSessionID: 1, Transfer: modify}},
	})

	req := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceModify, ngap.ParsePDUSessionResourceModifyRequest)

	tr, err := ngap.ParsePDUSessionResourceModifyRequestTransfer(req.PDUSessionResourceModify[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	item := tr.ULNGUUPTNLModify[0]
	if tun := endpointOf(t, plane, item.ULNGUUPTNLInformation, ranGW, gtpu.RAN); tun.Peer.TEID != 0x101 {
		t.Fatalf("new uplink = %+v", tun)
	}

	if got, _ := netip.AddrFromSlice(item.DLNGUUPTNLInformation.GTPTunnel.TransportLayerAddress.Bytes()); got != gnbUP ||
		item.DLNGUUPTNLInformation.GTPTunnel.GTPTEID != 0x200 {
		t.Fatalf("DL identifier = %+v, want the radio's own endpoint", item.DLNGUUPTNLInformation)
	}

	if f := tr.UserPlaneFailureIndication; f == nil || f.DLNGUUPTNLInformation.GTPTunnel.GTPTEID != 0x200 ||
		f.ULNGUUPTNLInformation.GTPTunnel.GTPTEID != item.ULNGUUPTNLInformation.GTPTunnel.GTPTEID {
		t.Fatalf("User Plane Failure Indication = %+v", f)
	}

	respTransfer, err := (&ngap.PDUSessionResourceModifyResponseTransfer{
		ULNGUUPTNLInformation: &item.ULNGUUPTNLInformation,
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	gnb.Send(t, &ngap.PDUSessionResourceModifyResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceModify: ngap.PDUSessionResourceModifyListModRes{{PDUSessionID: 1, Transfer: respTransfer}},
	})

	resp := expect(t, amf, "successful", ngap.ProcPDUSessionResourceModify, ngap.ParsePDUSessionResourceModifyResponse)

	rt, err := ngap.ParsePDUSessionResourceModifyResponseTransfer(resp.PDUSessionResourceModify[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := netip.AddrFromSlice(rt.ULNGUUPTNLInformation.GTPTunnel.TransportLayerAddress.Bytes()); got != upf ||
		rt.ULNGUUPTNLInformation.GTPTunnel.GTPTEID != 0x101 {
		t.Fatalf("UL identifier toward the AMF = %+v, want the UPF's endpoint", rt.ULNGUUPTNLInformation)
	}

	if plane.Tunnels() != 3 {
		t.Fatalf("%d tunnels, want 3", plane.Tunnels())
	}

	gnb.Send(t, &ngap.PDUSessionResourceReleaseResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceReleased: ngap.PDUSessionResourceReleasedListRelRes{{PDUSessionID: 1, Transfer: ngap.TransferContainer{0x00}}},
	})
	expectRaw(t, amf, "successful", ngap.ProcPDUSessionResourceRelease)

	if plane.Tunnels() != 0 {
		t.Fatalf("%d tunnels after PDU SESSION RESOURCE RELEASE RESPONSE, want 0", plane.Tunnels())
	}
}

func TestAggregatedTunnelsReleasedOnEveryPath(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	for _, c := range []struct {
		name, ip string
		release  func(t *testing.T, gnb, amf *peer, ue attached)
	}{
		{"radio lost", "127.0.1.202", func(t *testing.T, gnb, _ *peer, _ attached) { _ = gnb.Conn.Abort() }},
		{"reset from AMF", "127.0.1.203", func(t *testing.T, _, amf *peer, _ attached) {
			t.Helper()
			amf.Send(t, &ngap.NGReset{Cause: new(ngap.CauseMiscControlProcessingOverload.Cause()), ResetType: ngap.ResetType{NGInterface: new(ngap.ResetAllResetAll)}})
		}},
		{"UE context release", "127.0.1.204", func(t *testing.T, gnb, amf *peer, ue attached) {
			t.Helper()
			amf.Send(t, &ngap.UEContextReleaseCommand{
				UENGAPIDs: ngap.UENGAPIDs{UENGAPIDPair: &ngap.UENGAPIDPair{AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID}},
				Cause:     new(ngap.CauseNASNormalRelease.Cause()),
			})
			expectRaw(t, gnb, "initiating", ngap.ProcUEContextRelease)
			gnb.Send(t, &ngap.UEContextReleaseComplete{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, plane, amfs := aggregatedGateway(t, c.ip)
			gnb, amf := upAndRunning(t, h, amfs)
			ue := attach(t, gnb, amf, 7, 1<<39+5)
			setUpSession(t, gnb, amf, plane, ue)

			c.release(t, gnb, amf, ue)

			h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
		})
	}
}

func TestAggregatedFailsClosed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.1.205")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)

	amf.Send(t, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1},
			Transfer: setupRequestTransfer(t, tunnel(netip.MustParseAddr("2001:db8::1"), 0x100)),
		}},
	})

	expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)

	if plane.Tunnels() != 0 || len(h.gw.UEs()) != 0 {
		t.Fatalf("%d tunnels, %d UEs", plane.Tunnels(), len(h.gw.UEs()))
	}
}

func TestAggregatedUndecodableTransferFailsClosed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.19")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)

	amf.Send(t, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: ngap.TransferContainer{0xff, 0xff},
		}},
	})

	expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)

	if plane.Tunnels() != 0 || len(h.gw.UEs()) != 0 {
		t.Fatalf("%d tunnels, %d UEs", plane.Tunnels(), len(h.gw.UEs()))
	}
}

func TestAggregatedHandoverHairpinsDirectForwarding(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.1.206")
	gnb1, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb1, amf, 7, 1<<39+5)
	source := setUpSession(t, gnb1, amf, plane, ue)
	gnb2 := secondRadio(t, h)

	target := gnbID(t, 2)
	req := handoverRequest(t, 900, &target)
	req.PDUSessionResourceSetupListHOReq = ngap.PDUSessionResourceSetupListHOReq{{
		PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: setupRequestTransfer(t, tunnel(upf, 0x100)),
	}}
	amf.Send(t, req)

	_, raw := expectRaw(t, gnb2, "initiating", ngap.ProcHandoverResourceAllocation)
	targetAMFID, _ := ueIDs(t, raw)

	ackTransfer, err := (&ngap.HandoverRequestAcknowledgeTransfer{
		DLNGUUPTNLInformation:        tunnel(netip.MustParseAddr("10.90.1.24"), 0x300),
		DLForwardingUPTNLInformation: new(tunnel(netip.MustParseAddr("10.90.1.24"), 0x301)),
		QosFlowSetupResponse:         ngap.QosFlowListWithDataForwarding{{QosFlowIdentifier: 1}},
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	gnb2.Send(t, &ngap.HandoverRequestAcknowledge{
		AMFUENGAPID: &targetAMFID, RANUENGAPID: new(ngap.RANUENGAPID(21)),
		PDUSessionResourceAdmittedList:     ngap.PDUSessionResourceAdmittedList{{PDUSessionID: 1, Transfer: ackTransfer}},
		TargetToSourceTransparentContainer: ngap.TargetToSourceTransparentContainer{0xcc},
	})

	ack := expect(t, amf, "successful", ngap.ProcHandoverResourceAllocation, ngap.ParseHandoverRequestAcknowledge)

	at, err := ngap.ParseHandoverRequestAcknowledgeTransfer(ack.PDUSessionResourceAdmittedList[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	fwd := endpointOf(t, plane, *at.DLForwardingUPTNLInformation, coreGW, gtpu.Core)

	cmdTransfer, err := (&ngap.HandoverCommandTransfer{DLForwardingUPTNLInformation: at.DLForwardingUPTNLInformation}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	amf.Send(t, &ngap.HandoverCommand{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID, HandoverType: ngap.HandoverTypeIntra5GS,
		PDUSessionResourceHandoverList:     ngap.PDUSessionResourceHandoverList{{PDUSessionID: 1, Transfer: cmdTransfer}},
		TargetToSourceTransparentContainer: ngap.TargetToSourceTransparentContainer{0xcc},
	})

	cmd := expect(t, gnb1, "successful", ngap.ProcHandoverPreparation, ngap.ParseHandoverCommand)

	ct, err := ngap.ParseHandoverCommandTransfer(cmd.PDUSessionResourceHandoverList[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	hairpin := endpointOf(t, plane, *ct.DLForwardingUPTNLInformation, ranGW, gtpu.RAN)
	if hairpin.Peer != fwd.Peer || hairpin.Source != ranGW {
		t.Fatalf("hairpin = %+v, want toward %+v", hairpin, fwd.Peer)
	}

	wantTrafficMovedToTheTarget(t, h, plane, gnb1, source.Radio, fwd.Radio)
}
