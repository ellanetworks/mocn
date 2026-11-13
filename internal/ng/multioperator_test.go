package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/ng"
)

func TestPagingFanOut(t *testing.T) {
	skipWithoutSCTP(t)

	amfsA, amfsB := listenAMF(t, "127.0.1.20"), listenAMFAt(t, "127.0.1.21", 0)
	h := startGateway(t, amfsA.port(), operatorA("127.0.1.20"))

	gnb1 := h.connectGNB(t)
	gnb1.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101", "002002")))

	amf, _ := setupAMF(t, amfsA, setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0)))
	expect(t, gnb1, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	gnb2 := h.connectGNB(t)
	gnb2.send(t, setupRequest(t, 2, "gnb2", ngap.PagingDRXv128, ta(t, 2, "00101")))
	expect(t, gnb2, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	expect(t, amf, "initiating", ngap.ProcRANConfigurationUpdate, ngap.ParseRANConfigurationUpdate)
	amf.send(t, &ngap.RANConfigurationUpdateAcknowledge{})

	_ = amfsB

	paging := &ngap.Paging{
		FiveGSTMSI: &ngap.FiveGSTMSI{AMFSetID: 1, AMFPointer: 0, FiveGTMSI: 0xdeadbeef},
		TAIListForPaging: []ngap.TAI{
			{PLMNIdentity: plmnID(t, "001", "01"), TAC: 2},
			{PLMNIdentity: plmnID(t, "002", "002"), TAC: 1},
		},
	}
	amf.send(t, paging)

	want, err := paging.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	f := gnb2.nextFrame(t)
	if string(f.raw) != string(want) || f.stream != 0 {
		t.Fatalf("Paging relayed as %x on stream %d, want %x", f.raw, f.stream, want)
	}

	gnb1.quiet(t, 300*time.Millisecond)
}

func TestMultiOperatorSelectionAndScoping(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.22")
	amfsB := listenAMFAt(t, "127.0.1.23", amfs.port())
	h := startGateway(t, amfs.port(), operatorA("127.0.1.22"), operatorB("127.0.1.23"))

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101", "002002")))

	amfA, reqA := setupAMF(t, amfs, setupResponse(t, "amfA", 255, "00101", guami(t, "00101", 2, 1, 0)))
	amfB, reqB := setupAMF(t, amfsB, setupResponse(t, "amfB", 255, "002002", guami(t, "002002", 4, 3, 0)))

	if amfA == amfB {
		t.Fatal("one association for two AMFs")
	}

	assertTAs(t, reqA.SupportedTAList, ngap.SupportedTAList{ta(t, 1, "00101")})
	assertTAs(t, reqB.SupportedTAList, ngap.SupportedTAList{ta(t, 1, "002002")})

	if reqB.GlobalRANNodeID.Value != 7 || reqB.GlobalRANNodeID.Bits != 32 {
		t.Fatalf("operator B Global gNB ID = %+v", reqB.GlobalRANNodeID)
	}

	resp := expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	for len(resp.ServedGUAMIList) < 2 {
		_, raw := expectRaw(t, gnb, "initiating", ngap.ProcAMFConfigurationUpdate)
		if ok, err := raw.Get(ngap.IDServedGUAMIList, &resp.ServedGUAMIList); !ok || err != nil {
			t.Fatalf("Served GUAMI List: %v %v", ok, err)
		}

		gnb.send(t, &ngap.AMFConfigurationUpdateAcknowledge{})
	}

	if len(resp.ServedGUAMIList) != 2 {
		t.Fatalf("merged Served GUAMI List = %+v", resp.ServedGUAMIList)
	}

	ueB := attachWith(t, gnb, amfB, initialUE(t, 1, "002002"), 100)
	ueA := attach(t, gnb, amfA, 2, 100)

	if ueA.ranAMFID == ueB.ranAMFID {
		t.Fatal("same AMF UE NGAP ID toward the radio for two UEs")
	}

	byOperator := map[string]int{}
	for _, u := range h.gw.UEs() {
		byOperator[u.OperatorName]++
	}

	if byOperator["A"] != 1 || byOperator["B"] != 1 {
		t.Fatalf("UEs by operator = %v", byOperator)
	}

	amfA.send(t, &ngap.NGReset{
		Cause: &ngap.Cause{Group: ngap.CauseGroupMisc, Value: ngap.CauseMiscOMIntervention}, ResetType: ngap.ResetType{All: true},
	})
	expect(t, amfA, "successful", ngap.ProcNGReset, ngap.ParseNGResetAcknowledge)

	reset := expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].AMFUENGAPID != ueA.ranAMFID {
		t.Fatalf("NG Reset = %+v, want only operator A's UE", reset)
	}

	gnb.send(t, &ngap.NGResetAcknowledge{ConnectionList: reset.ResetType.Part})

	h.eventually(t, "only operator B's UE", func() bool {
		ues := h.gw.UEs()
		return len(ues) == 1 && ues[0].OperatorName == "B"
	})

	amfB.quiet(t, 200*time.Millisecond)
}

func TestSelectionWithinTheOperatorsAMFs(t *testing.T) {
	skipWithoutSCTP(t)

	amfs1 := listenAMF(t, "127.0.1.24")
	amfs2 := listenAMFAt(t, "127.0.1.25", amfs1.port())
	h := startGatewayWith(t, ng.Config{AMFPort: amfs1.port(), IntN: func(int) int { return 0 }},
		operatorA("127.0.1.24", "127.0.1.25"))

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	amf1, _ := setupAMF(t, amfs1, setupResponse(t, "amf1", 0, "00101", guami(t, "00101", 2, 1, 1)))
	amf2, _ := setupAMF(t, amfs2, setupResponse(t, "amf2", 50, "00101", guami(t, "00101", 2, 1, 2)))

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	settle(t, h, gnb, "127.0.1.24", "127.0.1.25")

	attach(t, gnb, amf2, 1, 1)

	stmsi := initialUE(t, 2, "00101")
	stmsi.FiveGSTMSI = &ngap.FiveGSTMSI{AMFSetID: 1, AMFPointer: 1, FiveGTMSI: 42}
	attachWith(t, gnb, amf1, stmsi, 2)

	amf1.send(t, &ngap.AMFStatusIndication{UnavailableGUAMIList: ngap.UnavailableGUAMIList{{GUAMI: guami(t, "00101", 2, 1, 1)}}})

	ind := expect(t, gnb, "initiating", ngap.ProcAMFStatusIndication, ngap.ParseAMFStatusIndication)
	if len(ind.UnavailableGUAMIList) != 1 || ind.UnavailableGUAMIList[0].GUAMI != guami(t, "00101", 2, 1, 1) {
		t.Fatalf("AMF Status Indication = %+v", ind)
	}

	h.eventually(t, "GUAMI unavailable", func() bool {
		st, _ := h.gw.AMFStatus(1, []string{"127.0.1.24"})
		return len(st.UnavailableGUAMIs) == 1 && st.UnavailableGUAMIs[0] == (ng.GUAMI{PLMN: plmnA, AMFRegionID: 2, AMFSetID: 1, AMFPointer: 1})
	})

	stmsi = initialUE(t, 3, "00101")
	stmsi.FiveGSTMSI = &ngap.FiveGSTMSI{AMFSetID: 1, AMFPointer: 1, FiveGTMSI: 43}
	attachWith(t, gnb, amf2, stmsi, 3)

	amf1.send(t, &ngap.OverloadStart{
		AMFOverloadResponse:               &ngap.OverloadResponse{OverloadAction: ngap.OverloadActionRejectRRCCRSignalling},
		AMFTrafficLoadReductionIndication: ngap.Ptr(ngap.TrafficLoadReductionIndication(40)),
		OverloadStartNSSAIList: ngap.OverloadStartNSSAIList{{
			SliceOverloadList: []ngap.SliceOverloadItem{{SNSSAI: slice.SNSSAI}},
		}},
	})

	h.eventually(t, "overload recorded", func() bool {
		st, _ := h.gw.AMFStatus(1, []string{"127.0.1.24"})
		o := st.Overload

		return o != nil && o.Action != nil && *o.Action == ng.OverloadRejectRRCCRSignalling &&
			o.TrafficLoadReductionIndication != nil && *o.TrafficLoadReductionIndication == 40 &&
			len(o.Slices) == 1 && o.Slices[0].SNSSAIs[0] == (ng.SNSSAI{SST: 1, SD: "010203"})
	})

	gnb.quiet(t, 200*time.Millisecond)

	amf1.send(t, &ngap.OverloadStop{})

	h.eventually(t, "overload cleared", func() bool {
		st, _ := h.gw.AMFStatus(1, []string{"127.0.1.24"})
		return st.Overload == nil
	})
}

func TestRerouteNASRequestWithinTheOperator(t *testing.T) {
	skipWithoutSCTP(t)

	amfs1 := listenAMF(t, "127.0.1.26")
	amfs2 := listenAMFAt(t, "127.0.1.27", amfs1.port())
	h := startGatewayWith(t, ng.Config{AMFPort: amfs1.port(), IntN: func(int) int { return 0 }},
		operatorA("127.0.1.26", "127.0.1.27"))

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	amf1, _ := setupAMF(t, amfs1, setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0)))
	amf2, _ := setupAMF(t, amfs2, setupResponse(t, "amf2", 255, "00101", guami(t, "00101", 2, 9, 0)))

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	settle(t, h, gnb, "127.0.1.26", "127.0.1.27")

	init := initialUE(t, 5, "00101")
	gnb.send(t, init)

	f, raw := expectRaw(t, amf1, "initiating", ngap.ProcInitialUEMessage)
	_, amfRANID := ueIDs(t, raw)

	amf1.send(t, &ngap.RerouteNASRequest{
		RANUENGAPID: amfRANID, NGAPMessage: ngap.NGAPMessage(f.raw), AMFSetID: 9,
		AllowedNSSAI: ngap.AllowedNSSAI{{SNSSAI: slice.SNSSAI}},
	})

	f, raw = expectRaw(t, amf2, "initiating", ngap.ProcInitialUEMessage)

	var set ngap.AMFSetID
	if ok, err := raw.Get(ngap.IDAMFSetID, &set); err != nil || !ok || set != 9 {
		t.Fatalf("AMF Set ID = %d, %v, %v", set, ok, err)
	}

	if raw.Index(ngap.IDAllowedNSSAI) < 0 || raw.Index(ngap.IDAMFSetID) > raw.Index(ngap.IDAllowedNSSAI) {
		t.Fatalf("rerouted IEs = %+v", raw.IEs())
	}

	if _, ranID := ueIDs(t, raw); ranID != amfRANID {
		t.Fatalf("RAN UE NGAP ID = %d, want %d", ranID, amfRANID)
	}

	var gotNAS ngap.NASPDU
	if ok, err := raw.Get(ngap.IDNASPDU, &gotNAS); err != nil || !ok || string(gotNAS) != string(nas) {
		t.Fatalf("NAS-PDU = %x", gotNAS)
	}

	dl := &ngap.DownlinkNASTransport{AMFUENGAPID: 500, RANUENGAPID: amfRANID, NASPDU: nas}
	amf2.send(t, dl)

	_, raw = expectRaw(t, gnb, "initiating", ngap.ProcDownlinkNASTransport)
	if _, ranID := ueIDs(t, raw); ranID != 5 {
		t.Fatalf("RAN UE NGAP ID toward the radio = %d", ranID)
	}

	ues := h.gw.UEs()
	if len(ues) != 1 || ues[0].AMFTNLAddresses[0] != "127.0.1.27" || *ues[0].AMFUENGAPID != 500 {
		t.Fatalf("UEs = %+v", ues)
	}

	amf1.quiet(t, 200*time.Millisecond)
}

func TestDownlinkFromAnotherAMFOfTheOperator(t *testing.T) {
	skipWithoutSCTP(t)

	amfs1 := listenAMF(t, "127.0.1.28")
	amfs2 := listenAMFAt(t, "127.0.1.29", amfs1.port())
	h := startGatewayWith(t, ng.Config{AMFPort: amfs1.port(), IntN: func(int) int { return 0 }},
		operatorA("127.0.1.28", "127.0.1.29"))

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	amf1, _ := setupAMF(t, amfs1, setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0)))
	amf2, _ := setupAMF(t, amfs2, setupResponse(t, "amf2", 0, "00101", guami(t, "00101", 2, 1, 1)))

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	settle(t, h, gnb, "127.0.1.28", "127.0.1.29")

	ue := attach(t, gnb, amf1, 5, 1)

	amf2.send(t, &ngap.DownlinkNASTransport{AMFUENGAPID: 2, RANUENGAPID: ue.amfRANID, NASPDU: nas})

	_, raw := expectRaw(t, gnb, "initiating", ngap.ProcDownlinkNASTransport)
	if a, r := ueIDs(t, raw); a != ue.ranAMFID || r != 5 {
		t.Fatalf("IDs toward the radio = %d/%d", a, r)
	}

	gnb.send(t, &ngap.UplinkNASTransport{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: 5, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1))})

	_, raw = expectRaw(t, amf2, "initiating", ngap.ProcUplinkNASTransport)
	if a, _ := ueIDs(t, raw); a != 2 {
		t.Fatalf("AMF UE NGAP ID = %d, want the new AMF's", a)
	}

	amf1.quiet(t, 200*time.Millisecond)

	ues := h.gw.UEs()
	if len(ues) != 1 || ues[0].AMFTNLAddresses[0] != "127.0.1.29" {
		t.Fatalf("UEs = %+v", ues)
	}
}

func settle(t *testing.T, h *harness, gnb *peer, amfs ...string) {
	t.Helper()

	h.eventually(t, "the AMFs up", func() bool {
		for _, a := range amfs {
			if st, _ := h.gw.AMFStatus(1, []string{a}); st.State != ng.AMFUp {
				return false
			}
		}

		return true
	})

	for {
		select {
		case f := <-gnb.msgs:
			m, ok := f.pdu.(*ngap.InitiatingMessage)
			if !ok || m.ProcedureCode != ngap.ProcAMFConfigurationUpdate {
				t.Fatalf("unexpected %s", procedureName(f.pdu))
			}

			gnb.send(t, &ngap.AMFConfigurationUpdateAcknowledge{})
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}
