package ng_test

import (
	"bytes"
	"net/netip"
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/tnl"
)

var (
	upf2   = netip.MustParseAddr("10.45.0.2")
	gnbUP2 = netip.MustParseAddr("10.90.1.24")
)

func qosFlow(u ngap.UPTransportLayerInformation) ngap.QosFlowPerTNLInformation {
	return ngap.QosFlowPerTNLInformation{UPTransportLayerInformation: u, AssociatedQosFlowList: ngap.AssociatedQosFlowList{{QosFlowIdentifier: 1}}}
}

func marshal(t *testing.T, m interface {
	Marshal() (ngap.TransferContainer, error)
},
) ngap.TransferContainer {
	t.Helper()

	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func modifyRequest(t *testing.T, ue attached, tr ngap.TransferContainer) *ngap.PDUSessionResourceModifyRequest {
	t.Helper()

	return &ngap.PDUSessionResourceModifyRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceModify: ngap.PDUSessionResourceModifyListModReq{{PDUSessionID: 1, Transfer: tr}},
	}
}

func modifyResponse(t *testing.T, ue attached, tr ngap.TransferContainer) *ngap.PDUSessionResourceModifyResponse {
	t.Helper()

	return &ngap.PDUSessionResourceModifyResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceModify: ngap.PDUSessionResourceModifyListModRes{{PDUSessionID: 1, Transfer: tr}},
	}
}

type splitSession struct {
	ul, ulAdd, dl, dlAdd gtpu.Tunnel
}

func setUpSplitSession(t *testing.T, gnb, amf *peer, plane *gtpu.Plane, ue attached) splitSession {
	t.Helper()

	req, err := (&ngap.PDUSessionResourceSetupRequestTransfer{
		ULNGUUPTNLInformation:           tunnel(upf, 0x100),
		AdditionalULNGUUPTNLInformation: ngap.UPTransportLayerInformationList{{NGUUPTNLInformation: tunnel(upf2, 0x110)}},
		RedundantULNGUUPTNLInformation:  new(tunnel(upf, 0x120)),
		PDUSessionType:                  ngap.PDUSessionTypeIPv4,
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

	amf.Send(t, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: req}},
	})

	got := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceSetup, ngap.ParsePDUSessionResourceSetupRequest)

	tr, err := ngap.ParsePDUSessionResourceSetupRequestTransfer(got.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	ul := endpointOf(t, plane, tr.ULNGUUPTNLInformation, ranGW, gtpu.RAN)
	ulAdd := endpointOf(t, plane, tr.AdditionalULNGUUPTNLInformation[0].NGUUPTNLInformation, ranGW, gtpu.RAN)

	if red := endpointOf(t, plane, *tr.RedundantULNGUUPTNLInformation, ranGW, gtpu.RAN); red.Peer.TEID != 0x120 {
		t.Fatalf("redundant uplink = %+v", red)
	}

	if ul.Peer.TEID != 0x100 || ulAdd.Peer != (gtpu.Endpoint{Addr: upf2, TEID: 0x110}) {
		t.Fatalf("uplinks = %+v %+v", ul, ulAdd)
	}

	resp := marshal(t, &ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation:           qosFlow(tunnel(gnbUP, 0x200)),
		AdditionalDLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformationList{{QosFlowPerTNLInformation: qosFlow(tunnel(gnbUP2, 0x210))}},
		RedundantDLQosFlowPerTNLInformation:  new(qosFlow(tunnel(gnbUP, 0x220))),
	})

	gnb.Send(t, &ngap.PDUSessionResourceSetupResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSURes{{PDUSessionID: 1, Transfer: resp}},
	})

	res := expect(t, amf, "successful", ngap.ProcPDUSessionResourceSetup, ngap.ParsePDUSessionResourceSetupResponse)

	rt, err := ngap.ParsePDUSessionResourceSetupResponseTransfer(res.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	dl := endpointOf(t, plane, rt.DLQosFlowPerTNLInformation.UPTransportLayerInformation, coreGW, gtpu.Core)
	dlAdd := endpointOf(t, plane, rt.AdditionalDLQosFlowPerTNLInformation[0].QosFlowPerTNLInformation.UPTransportLayerInformation,
		coreGW, gtpu.Core)

	if red := endpointOf(t, plane, rt.RedundantDLQosFlowPerTNLInformation.UPTransportLayerInformation, coreGW,
		gtpu.Core); red.Peer.TEID != 0x220 {
		t.Fatalf("redundant downlink = %+v", red)
	}

	if dlAdd.Peer != (gtpu.Endpoint{Addr: gnbUP2, TEID: 0x210}) || plane.Tunnels() != 6 {
		t.Fatalf("additional downlink = %+v, %d tunnels", dlAdd, plane.Tunnels())
	}

	return splitSession{ul: ul, ulAdd: ulAdd, dl: dl, dlAdd: dlAdd}
}

func TestAggregatedModifyIdentifiesTheTransportBearer(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.1")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	s := setUpSplitSession(t, gnb, amf, plane, ue)
	ul, ulAdd, dl, dlAdd := s.ul, s.ulAdd, s.dl, s.dlAdd

	modify := func(items ngap.ULNGUUPTNLModifyList) ngap.ULNGUUPTNLModifyList {
		amf.Send(t, modifyRequest(t, ue, marshal(t, &ngap.PDUSessionResourceModifyRequestTransfer{ULNGUUPTNLModify: items})))

		req := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceModify, ngap.ParsePDUSessionResourceModifyRequest)

		tr, err := ngap.ParsePDUSessionResourceModifyRequestTransfer(req.PDUSessionResourceModify[0].Transfer)
		if err != nil {
			t.Fatal(err)
		}

		gnb.Send(t, modifyResponse(t, ue, marshal(t, &ngap.PDUSessionResourceModifyResponseTransfer{})))
		expectRaw(t, amf, "successful", ngap.ProcPDUSessionResourceModify)

		return tr.ULNGUUPTNLModify
	}

	got := modify(ngap.ULNGUUPTNLModifyList{
		{ULNGUUPTNLInformation: tunnel(upf2, 0x111), DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dlAdd.TEID))},
		{ULNGUUPTNLInformation: tunnel(upf, 0x101), DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dl.TEID))},
	})

	if got[0].DLNGUUPTNLInformation.GTPTunnel.GTPTEID != 0x210 || got[1].DLNGUUPTNLInformation.GTPTunnel.GTPTEID != 0x200 {
		t.Fatalf("DL identifiers toward the radio = %+v", got)
	}

	newAdd := endpointOf(t, plane, got[0].ULNGUUPTNLInformation, ranGW, gtpu.RAN)
	newMain := endpointOf(t, plane, got[1].ULNGUUPTNLInformation, ranGW, gtpu.RAN)

	if newAdd.Peer.TEID != 0x111 || newMain.Peer.TEID != 0x101 {
		t.Fatalf("new uplinks = %+v %+v", newAdd, newMain)
	}

	modify(ngap.ULNGUUPTNLModifyList{
		{ULNGUUPTNLInformation: tunnel(upf, 0x102), DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dl.TEID))},
	})

	if _, ok := plane.Get(ul.TEID); ok {
		t.Error("the first uplink of the main transport bearer kept after it was replaced twice")
	}

	for _, keep := range []gtpu.Tunnel{ulAdd, newAdd, newMain, dl, dlAdd} {
		if _, ok := plane.Get(keep.TEID); !ok {
			t.Errorf("tunnel %+v freed by a modification of another transport bearer", keep)
		}
	}
}

func TestAggregatedModifyRejectsUnknownIdentifiers(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	for _, c := range []struct {
		name, ip string
		transfer func(dl gtpu.Tunnel) *ngap.PDUSessionResourceModifyRequestTransfer
	}{
		{"unknown DL NG-U UP TNL Information", "127.0.3.2", func(gtpu.Tunnel) *ngap.PDUSessionResourceModifyRequestTransfer {
			return &ngap.PDUSessionResourceModifyRequestTransfer{ULNGUUPTNLModify: ngap.ULNGUUPTNLModifyList{{
				ULNGUUPTNLInformation: tunnel(upf, 0x101), DLNGUUPTNLInformation: tunnel(coreGW, 0x0eadbee),
			}}}
		}},
		{"unknown User Plane Failure Indication UL", "127.0.3.3", func(dl gtpu.Tunnel) *ngap.PDUSessionResourceModifyRequestTransfer {
			return &ngap.PDUSessionResourceModifyRequestTransfer{UserPlaneFailureIndication: &ngap.UserPlaneFailureIndication{
				ULNGUUPTNLInformation: tunnel(upf, 0x999),
				DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dl.TEID)),
			}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			h, plane, amfs := aggregatedGateway(t, c.ip)
			gnb, amf := upAndRunning(t, h, amfs)
			ue := attach(t, gnb, amf, 7, 1<<39+5)
			dl := setUpSession(t, gnb, amf, plane, ue)

			amf.Send(t, modifyRequest(t, ue, marshal(t, c.transfer(dl))))

			expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
			expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)

			h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
		})
	}
}

func TestAggregatedUserPlaneFailureIndicationTranslated(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.4")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	dl := setUpSession(t, gnb, amf, plane, ue)
	before := plane.Tunnels()

	amf.Send(t, modifyRequest(t, ue, marshal(t, &ngap.PDUSessionResourceModifyRequestTransfer{
		UserPlaneFailureIndication: &ngap.UserPlaneFailureIndication{
			ULNGUUPTNLInformation: tunnel(upf, 0x100), DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dl.TEID)),
		},
	})))

	req := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceModify, ngap.ParsePDUSessionResourceModifyRequest)

	tr, err := ngap.ParsePDUSessionResourceModifyRequestTransfer(req.PDUSessionResourceModify[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	f := tr.UserPlaneFailureIndication
	if ul := endpointOf(t, plane, f.ULNGUUPTNLInformation, ranGW, gtpu.RAN); ul.Peer != (gtpu.Endpoint{Addr: upf, TEID: 0x100}) {
		t.Fatalf("UL identifier = %+v, want the existing uplink", ul)
	}

	if f.DLNGUUPTNLInformation.GTPTunnel.GTPTEID != 0x200 {
		t.Fatalf("DL identifier = %+v", f.DLNGUUPTNLInformation)
	}

	if plane.Tunnels() != before {
		t.Fatalf("%d tunnels, want %d: an identifier allocates nothing", plane.Tunnels(), before)
	}
}

func TestAggregatedModifyResponseKindsAreDistinct(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.5")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	ulAdd := setUpSplitSession(t, gnb, amf, plane, ue).ulAdd

	amf.Send(t, modifyRequest(t, ue, marshal(t, &ngap.PDUSessionResourceModifyRequestTransfer{})))
	expectRaw(t, gnb, "initiating", ngap.ProcPDUSessionResourceModify)

	before := plane.Tunnels()

	gnb.Send(t, modifyResponse(t, ue, marshal(t, &ngap.PDUSessionResourceModifyResponseTransfer{
		AdditionalDLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformationList{{QosFlowPerTNLInformation: qosFlow(tunnel(gnbUP2, 0x230))}},
		AdditionalNGUUPTNLInformation: ngap.UPTransportLayerInformationPairList{{
			ULNGUUPTNLInformation: tunnel(ranGW, ngap.GTPTEID(ulAdd.TEID)), DLNGUUPTNLInformation: tunnel(gnbUP2, 0x231),
		}},
	})))

	res := expect(t, amf, "successful", ngap.ProcPDUSessionResourceModify, ngap.ParsePDUSessionResourceModifyResponse)

	rt, err := ngap.ParsePDUSessionResourceModifyResponseTransfer(res.PDUSessionResourceModify[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	a := endpointOf(t, plane, rt.AdditionalDLQosFlowPerTNLInformation[0].QosFlowPerTNLInformation.UPTransportLayerInformation,
		coreGW, gtpu.Core)
	b := endpointOf(t, plane, rt.AdditionalNGUUPTNLInformation[0].DLNGUUPTNLInformation, coreGW, gtpu.Core)

	if a.Peer.TEID != 0x230 || b.Peer.TEID != 0x231 {
		t.Fatalf("additional downlinks = %+v %+v", a, b)
	}

	if pair := rt.AdditionalNGUUPTNLInformation[0].ULNGUUPTNLInformation.GTPTunnel; pair.GTPTEID != 0x110 {
		t.Fatalf("UL identifier toward the AMF = %+v", pair)
	}

	if plane.Tunnels() != before+2 {
		t.Fatalf("%d tunnels, want %d", plane.Tunnels(), before+2)
	}

	if _, ok := plane.Get(a.TEID); !ok {
		t.Fatal("additional downlink freed by the paired downlink of the same index")
	}
}

func TestAggregatedModifyIndicationAndConfirm(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.6")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	setUpSession(t, gnb, amf, plane, ue)

	gnb.Send(t, &ngap.PDUSessionResourceModifyIndication{
		AMFUENGAPID: ue.ranAMFID, RANUENGAPID: ue.ranID,
		PDUSessionResourceModify: ngap.PDUSessionResourceModifyListModInd{{
			PDUSessionID: 1,
			Transfer: marshal(t, &ngap.PDUSessionResourceModifyIndicationTransfer{
				DLQosFlowPerTNLInformation: qosFlow(tunnel(gnbUP2, 0x240)),
			}),
		}},
	})

	ind := expect(t, amf, "initiating", ngap.ProcPDUSessionResourceModifyIndication, ngap.ParsePDUSessionResourceModifyIndication)

	it, err := ngap.ParsePDUSessionResourceModifyIndicationTransfer(ind.PDUSessionResourceModify[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	dl := endpointOf(t, plane, it.DLQosFlowPerTNLInformation.UPTransportLayerInformation, coreGW, gtpu.Core)
	if dl.Peer != (gtpu.Endpoint{Addr: gnbUP2, TEID: 0x240}) {
		t.Fatalf("modified downlink = %+v", dl)
	}

	amf.Send(t, &ngap.PDUSessionResourceModifyConfirm{
		AMFUENGAPID: &ue.amfID, RANUENGAPID: &ue.amfRANID,
		PDUSessionResourceModify: ngap.PDUSessionResourceModifyListModCfm{{
			PDUSessionID: 1,
			Transfer: marshal(t, &ngap.PDUSessionResourceModifyConfirmTransfer{
				QosFlowModifyConfirm:  ngap.QosFlowModifyConfirmList{{QosFlowIdentifier: 1}},
				ULNGUUPTNLInformation: tunnel(upf2, 0x150),
			}),
		}},
	})

	cfm := expect(t, gnb, "successful", ngap.ProcPDUSessionResourceModifyIndication, ngap.ParsePDUSessionResourceModifyConfirm)

	ct, err := ngap.ParsePDUSessionResourceModifyConfirmTransfer(cfm.PDUSessionResourceModify[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	if ul := endpointOf(t, plane, ct.ULNGUUPTNLInformation, ranGW, gtpu.RAN); ul.Peer != (gtpu.Endpoint{Addr: upf2, TEID: 0x150}) {
		t.Fatalf("confirmed uplink = %+v", ul)
	}

	if _, ok := plane.Get(dl.TEID); !ok {
		t.Fatal("confirmed downlink freed")
	}
}

func TestAggregatedSessionReleasedByNotify(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.7")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	setUpSession(t, gnb, amf, plane, ue)

	gnb.Send(t, &ngap.PDUSessionResourceNotify{
		AMFUENGAPID: ue.ranAMFID, RANUENGAPID: ue.ranID,
		PDUSessionResourceReleased: ngap.PDUSessionResourceReleasedListNot{{PDUSessionID: 1, Transfer: ngap.TransferContainer{0x00}}},
	})
	expectRaw(t, amf, "initiating", ngap.ProcPDUSessionResourceNotify)

	if plane.Tunnels() != 0 {
		t.Fatalf("%d tunnels after PDU SESSION RESOURCE NOTIFY, want 0", plane.Tunnels())
	}
}

func TestAggregatedInitialContextSetupWithSessions(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.8")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)

	ics := contextSetup(t, ue)
	ics.UEAggregateMaximumBitRate = &ngap.UEAggregateMaximumBitRate{DL: 1e9, UL: 1e9}
	ics.PDUSessionResourceSetup = ngap.PDUSessionResourceSetupListCxtReq{
		{PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: setupRequestTransfer(t, tunnel(upf, 0x100))},
		{PDUSessionID: 2, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: setupRequestTransfer(t, tunnel(upf, 0x101))},
	}
	amf.Send(t, ics)

	req := expect(t, gnb, "initiating", ngap.ProcInitialContextSetup, ngap.ParseInitialContextSetupRequest)

	for i, it := range req.PDUSessionResourceSetup {
		tr, err := ngap.ParsePDUSessionResourceSetupRequestTransfer(it.Transfer)
		if err != nil {
			t.Fatal(err)
		}

		if ul := endpointOf(t, plane, tr.ULNGUUPTNLInformation, ranGW, gtpu.RAN); ul.Peer.TEID != uint32(0x100+i) {
			t.Fatalf("uplink %d = %+v", i, ul)
		}
	}

	gnb.Send(t, &ngap.InitialContextSetupResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListCxtRes{
			{PDUSessionID: 1, Transfer: setupResponseTransfer(t, tunnel(gnbUP, 0x200))},
		},
		PDUSessionResourceFailed: ngap.PDUSessionResourceFailedToSetupListCxtRes{{PDUSessionID: 2, Transfer: ngap.TransferContainer{0x00}}},
	})

	res := expect(t, amf, "successful", ngap.ProcInitialContextSetup, ngap.ParseInitialContextSetupResponse)

	rt, err := ngap.ParsePDUSessionResourceSetupResponseTransfer(res.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	endpointOf(t, plane, rt.DLQosFlowPerTNLInformation.UPTransportLayerInformation, coreGW, gtpu.Core)

	if plane.Tunnels() != 2 {
		t.Fatalf("%d tunnels, want 2: the failed session's uplink is freed", plane.Tunnels())
	}
}

func TestAggregatedRepeatedSetupReusesTunnels(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.9")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)

	var first ngap.GTPTEID

	for i := range 2 {
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

		if i == 0 {
			first = tr.ULNGUUPTNLInformation.GTPTunnel.GTPTEID
		} else if tr.ULNGUUPTNLInformation.GTPTunnel.GTPTEID != first {
			t.Fatalf("repeated setup handed out %#x, want %#x", tr.ULNGUUPTNLInformation.GTPTunnel.GTPTEID, first)
		}
	}

	if plane.Tunnels() != 1 {
		t.Fatalf("%d tunnels after a repeated setup, want 1", plane.Tunnels())
	}
}

func TestAggregatedUnknownTransferExtensionFailsClosed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.10")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)

	amf.Send(t, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: setupRequestTransfer(t, tunnel(upf, 0x100)),
		}},
	})
	expectRaw(t, gnb, "initiating", ngap.ProcPDUSessionResourceSetup)

	known := marshal(t, &ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation:          qosFlow(tunnel(gnbUP, 0x200)),
		RedundantDLQosFlowPerTNLInformation: new(qosFlow(tunnel(gnbUP, 0x201))),
	})

	unknown := bytes.Replace(known, []byte{0x00, byte(ngap.IDRedundantDLQosFlowPerTNLInformation)}, []byte{0x0f, 0xff}, 1)

	parsed, err := ngap.ParsePDUSessionResourceSetupResponseTransfer(unknown)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.RedundantDLQosFlowPerTNLInformation != nil || tnl.Lossless(parsed) == nil {
		t.Fatal("test transfer does not carry an unknown extension")
	}

	gnb.Send(t, &ngap.PDUSessionResourceSetupResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSURes{{PDUSessionID: 1, Transfer: unknown}},
	})

	expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)

	h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
}

func renamedIE(t *testing.T, b ngap.TransferContainer, id ngap.ProtocolIEID) ngap.TransferContainer {
	t.Helper()

	from := []byte{byte(id >> 8), byte(id), byte(ngap.CriticalityIgnore) << 6}
	if bytes.Count(b, from) != 1 {
		t.Fatalf("IE %d is not encoded exactly once in %x", id, b)
	}

	return bytes.Replace(b, from, []byte{0x0f, 0xfe, byte(ngap.CriticalityIgnore) << 6}, 1)
}

func samePER(t *testing.T, a, b per.Marshaler) {
	t.Helper()

	x, err := per.Marshal(a, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	y, err := per.Marshal(b, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(x, y) {
		t.Fatalf("relayed %x, sent %x", y, x)
	}
}

func TestAggregatedUnknownQosFlowExtensionsPassThrough(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.18")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+6)

	req := renamedIE(t, marshal(t, &ngap.PDUSessionResourceSetupRequestTransfer{
		ULNGUUPTNLInformation: tunnel(upf, 0x100),
		PDUSessionType:        ngap.PDUSessionTypeIPv4,
		QosFlowSetupRequest: ngap.QosFlowSetupRequestList{{
			QosFlowIdentifier: 1,
			QosFlowLevelQosParameters: ngap.QosFlowLevelQosParameters{
				QosCharacteristics:             ngap.QosCharacteristics{NonDynamic5QI: &ngap.NonDynamic5QIDescriptor{FiveQI: 9}},
				AllocationAndRetentionPriority: ngap.AllocationAndRetentionPriority{PriorityLevelARP: 1},
			},
			RedundantQosFlowIndicator: new(ngap.RedundantQosFlowIndicatorTrue),
		}},
	}), ngap.IDRedundantQosFlowIndicator)

	sent, err := ngap.DecodePDUSessionResourceSetupRequestTransfer(req)
	if err != nil || sent.QosFlowSetupRequest[0].RedundantQosFlowIndicator != nil {
		t.Fatalf("test transfer does not carry an unknown QoS flow extension: %v", err)
	}

	amf.Send(t, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: req}},
	})

	got := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceSetup, ngap.ParsePDUSessionResourceSetupRequest)

	tr, err := ngap.DecodePDUSessionResourceSetupRequestTransfer(got.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	if ul := endpointOf(t, plane, tr.ULNGUUPTNLInformation, ranGW, gtpu.RAN); ul.Peer != (gtpu.Endpoint{Addr: upf, TEID: 0x100}) {
		t.Fatalf("uplink = %+v", ul)
	}

	samePER(t, &sent.QosFlowSetupRequest, &tr.QosFlowSetupRequest)

	resp := renamedIE(t, marshal(t, &ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformation{
			UPTransportLayerInformation: tunnel(gnbUP, 0x200),
			AssociatedQosFlowList: ngap.AssociatedQosFlowList{{
				QosFlowIdentifier: 1, CurrentQoSParaSetIndex: new(ngap.AlternativeQoSParaSetIndex(1)),
			}},
		},
	}), ngap.IDCurrentQoSParaSetIndex)

	answered, err := ngap.DecodePDUSessionResourceSetupResponseTransfer(resp)
	if err != nil || answered.DLQosFlowPerTNLInformation.AssociatedQosFlowList[0].CurrentQoSParaSetIndex != nil {
		t.Fatalf("test transfer does not carry an unknown associated QoS flow extension: %v", err)
	}

	gnb.Send(t, &ngap.PDUSessionResourceSetupResponse{
		AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSURes{{PDUSessionID: 1, Transfer: resp}},
	})

	res := expect(t, amf, "successful", ngap.ProcPDUSessionResourceSetup, ngap.ParsePDUSessionResourceSetupResponse)

	rt, err := ngap.DecodePDUSessionResourceSetupResponseTransfer(res.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	dl := endpointOf(t, plane, rt.DLQosFlowPerTNLInformation.UPTransportLayerInformation, coreGW, gtpu.Core)
	if dl.Peer != (gtpu.Endpoint{Addr: gnbUP, TEID: 0x200}) || plane.Tunnels() != 2 {
		t.Fatalf("downlink = %+v, %d tunnels", dl, plane.Tunnels())
	}

	samePER(t, &answered.DLQosFlowPerTNLInformation.AssociatedQosFlowList, &rt.DLQosFlowPerTNLInformation.AssociatedQosFlowList)
}

func TestAggregatedHandoverCancelFreesForwarding(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.11")
	gnb1, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb1, amf, 7, 1<<39+5)
	setUpSession(t, gnb1, amf, plane, ue)

	before := plane.Tunnels()

	amf.Send(t, &ngap.HandoverCommand{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID, HandoverType: ngap.HandoverTypeIntra5GS,
		PDUSessionResourceHandoverList: ngap.PDUSessionResourceHandoverList{{
			PDUSessionID: 1,
			Transfer:     marshal(t, &ngap.HandoverCommandTransfer{DLForwardingUPTNLInformation: new(tunnel(upf, 0x300))}),
		}},
		TargetToSourceTransparentContainer: ngap.TargetToSourceTransparentContainer{0xcc},
	})

	cmd := expect(t, gnb1, "successful", ngap.ProcHandoverPreparation, ngap.ParseHandoverCommand)

	ct, err := ngap.ParseHandoverCommandTransfer(cmd.PDUSessionResourceHandoverList[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	fwd := endpointOf(t, plane, *ct.DLForwardingUPTNLInformation, ranGW, gtpu.RAN)

	gnb1.Send(t, &ngap.HandoverCancel{
		AMFUENGAPID: ue.ranAMFID, RANUENGAPID: ue.ranID,
		Cause: new(ngap.CauseRadioNetworkHandoverCancelled.Cause()),
	})
	expectRaw(t, amf, "initiating", ngap.ProcHandoverCancel)

	if _, ok := plane.Get(fwd.TEID); ok || plane.Tunnels() != before {
		t.Fatalf("%d tunnels after HANDOVER CANCEL, want %d", plane.Tunnels(), before)
	}
}

func TestAggregatedPathSwitchKeepsTheUplink(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.12")
	gnb1, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb1, amf, 7, 1<<39+5)
	dl := setUpSession(t, gnb1, amf, plane, ue)
	gnb2 := secondRadio(t, h)

	ps := pathSwitchRequest(t, ue.amfID, new(guami(t, "00101", 2, 1, 0)))
	ps.PDUSessionResourceToBeSwitchedDLList[0].Transfer = marshal(t, &ngap.PathSwitchRequestTransfer{
		DLNGUUPTNLInformation: tunnel(gnbUP2, 0x250),
		QosFlowAccepted:       ngap.QosFlowAcceptedList{{QosFlowIdentifier: 1}},
	})
	gnb2.Send(t, ps)

	req := expect(t, amf, "initiating", ngap.ProcPathSwitchRequest, ngap.ParsePathSwitchRequest)

	pt, err := ngap.ParsePathSwitchRequestTransfer(req.PDUSessionResourceToBeSwitchedDLList[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	newDL := endpointOf(t, plane, pt.DLNGUUPTNLInformation, coreGW, gtpu.Core)
	if newDL.Peer != (gtpu.Endpoint{Addr: gnbUP2, TEID: 0x250}) {
		t.Fatalf("switched downlink = %+v", newDL)
	}

	amf.Send(t, &ngap.PathSwitchRequestAcknowledge{
		AMFUENGAPID: new(ngap.AMFUENGAPID(1<<39 + 6)), RANUENGAPID: &req.RANUENGAPID,
		SecurityContext: ngap.SecurityContext{NextHopChainingCount: 2, NextHopNH: securityKey()},
		PDUSessionResourceSwitchedList: ngap.PDUSessionResourceSwitchedList{{
			PDUSessionID: 1, Transfer: marshal(t, &ngap.PathSwitchRequestAcknowledgeTransfer{}),
		}},
		AllowedNSSAI: ngap.AllowedNSSAI{{SNSSAI: slice.SNSSAI}},
	})
	expectRaw(t, gnb2, "successful", ngap.ProcPathSwitchRequest)
	expectRaw(t, gnb1, "initiating", ngap.ProcUEContextRelease)

	gnb1.Send(t, &ngap.UEContextReleaseComplete{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID})

	h.eventually(t, "old downlink freed, uplink kept", func() bool {
		_, old := plane.Get(dl.TEID)
		_, cur := plane.Get(newDL.TEID)

		return !old && cur && plane.Tunnels() == 2
	})

	wantTrafficMovedToTheTarget(t, h, plane, gnb1, dl.Radio, newDL.Radio)
}

func TestAggregatedTunnelsReleasedOnPartialReset(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	for _, c := range []struct {
		name, ip string
		release  func(t *testing.T, h *harness, gnb, amf *peer, ue attached)
	}{
		{"partial reset from AMF", "127.0.3.14", func(t *testing.T, _ *harness, _, amf *peer, ue attached) {
			t.Helper()
			amf.Send(t, &ngap.NGReset{
				Cause:     new(ngap.CauseMiscControlProcessingOverload.Cause()),
				ResetType: ngap.ResetType{PartOfNGInterface: &ngap.UEAssociatedLogicalNGConnectionList{connection(&ue.amfID, ue.amfRANID)}},
			})
		}},
		{"partial reset from radio", "127.0.3.15", func(t *testing.T, _ *harness, gnb, _ *peer, ue attached) {
			t.Helper()
			gnb.Send(t, &ngap.NGReset{
				Cause:     new(ngap.CauseMiscControlProcessingOverload.Cause()),
				ResetType: ngap.ResetType{PartOfNGInterface: &ngap.UEAssociatedLogicalNGConnectionList{connection(&ue.ranAMFID, ue.ranID)}},
			})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			h, plane, amfs := aggregatedGateway(t, c.ip)
			gnb, amf := upAndRunning(t, h, amfs)
			ue := attach(t, gnb, amf, 7, 1<<39+5)
			setUpSession(t, gnb, amf, plane, ue)

			c.release(t, h, gnb, amf, ue)

			h.eventually(t, "no tunnels", func() bool { return plane.Tunnels() == 0 })
		})
	}
}

func TestAggregationTurnedOffKeepsLiveSessionsRelayed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.17")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	dl := setUpSession(t, gnb, amf, plane, ue)

	if err := plane.SetOperators([]gtpu.Operator{{ID: operatorA().ID, Name: operatorA().Name}}); err != nil {
		t.Fatal(err)
	}

	amf.Send(t, modifyRequest(t, ue, marshal(t, &ngap.PDUSessionResourceModifyRequestTransfer{
		ULNGUUPTNLModify: ngap.ULNGUUPTNLModifyList{{
			ULNGUUPTNLInformation: tunnel(upf, 0x101), DLNGUUPTNLInformation: tunnel(coreGW, ngap.GTPTEID(dl.TEID)),
		}},
	})))

	req := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceModify, ngap.ParsePDUSessionResourceModifyRequest)

	tr, err := ngap.ParsePDUSessionResourceModifyRequestTransfer(req.PDUSessionResourceModify[0].Transfer)
	if err != nil {
		t.Fatal(err)
	}

	if tr.ULNGUUPTNLModify[0].DLNGUUPTNLInformation.GTPTunnel.GTPTEID != 0x200 {
		t.Fatalf("live session not relayed after aggregation was turned off: %+v", tr.ULNGUUPTNLModify[0])
	}

	other := attach(t, gnb, amf, 8, 1<<39+6)

	amf.Send(t, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: other.amfID, RANUENGAPID: other.amfRANID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: 1, SNSSAI: ngap.SNSSAI{SST: 1}, Transfer: setupRequestTransfer(t, tunnel(upf, 0x300)),
		}},
	})

	direct := expect(t, gnb, "initiating", ngap.ProcPDUSessionResourceSetup, ngap.ParsePDUSessionResourceSetupRequest)
	if string(direct.PDUSessionResourceSetup[0].Transfer) != string(setupRequestTransfer(t, tunnel(upf, 0x300))) {
		t.Fatal("new session relayed after aggregation was turned off")
	}
}

func TestAggregatedTunnelsSurviveAMFLoss(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, plane, amfs := aggregatedGateway(t, "127.0.3.13")
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1<<39+5)
	setUpSession(t, gnb, amf, plane, ue)

	_ = amf.Conn.Abort()

	gnb.Quiet(t, 300*time.Millisecond)

	if plane.Tunnels() != 2 || len(h.gw.UEs()) != 1 {
		t.Fatalf("%d tunnels, %d UEs after the AMF was lost, want the UE and its PDU session kept", plane.Tunnels(),
			len(h.gw.UEs()))
	}
}
