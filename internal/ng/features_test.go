package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/ng"
)

func TestInitialContextSetupRequestCarriesTheAMFUENGAPID2(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.90")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.90"))
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 7, 1000)

	ics := contextSetup(t, ue)
	ics.AMFUENGAPID2 = ngap.Ptr(ngap.AMFUENGAPID(4))
	amf.Send(t, ics)

	f, _ := expectRaw(t, gnb, "initiating", ngap.ProcInitialContextSetup)

	pdu, err := ngap.Unmarshal(f.Raw)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ngap.ParseInitialContextSetupRequest(pdu.(*ngap.InitiatingMessage).Value)
	if err != nil {
		t.Fatal(err)
	}

	if got.AMFUENGAPID != ue.ranAMFID || got.AMFUENGAPID2 == nil || *got.AMFUENGAPID2 != 1000 {
		t.Fatalf("AMF UE NGAP IDs = %d/%v, want %d/1000", got.AMFUENGAPID, got.AMFUENGAPID2, ue.ranAMFID)
	}
}

func TestErrorIndicationWithOnlyTheAMFUEID(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.91")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.91"))
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 3, 77)

	other := ngap.Cause{Group: ngap.CauseGroupMisc, Value: ngap.CauseMiscUnspecified}
	amf.Send(t, &ngap.ErrorIndication{AMFUENGAPID: &ue.amfID, Cause: &other})
	gnb.Quiet(t, 200*time.Millisecond)

	if len(h.gw.UEs()) != 1 {
		t.Fatal("UE released on an Error Indication without an AP ID cause")
	}

	amf.Send(t, &ngap.ErrorIndication{AMFUENGAPID: &ue.amfID, Cause: &causeUnknownLocal})

	expectReset(t, gnb, connection(&ue.ranAMFID, ue.ranID))
	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
	amf.Quiet(t, 200*time.Millisecond)
}

func TestDuplicateGlobalGNBIDRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.93")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.93"))
	upAndRunning(t, h, amfs)

	gnb2 := h.connectGNBFrom(t, "127.0.1.102")
	gnb2.Send(t, setupRequest(t, 1, "copy", ngap.PagingDRXv128, ta(t, 2, "00101")))

	fail := expect(t, gnb2, "unsuccessful", ngap.ProcNGSetup, ngap.ParseNGSetupFailure)
	if fail.Cause == nil || fail.Cause.Group != ngap.CauseGroupProtocol || fail.Cause.Value != ngap.CauseProtocolSemanticError {
		t.Fatalf("NG Setup Failure = %+v, want semantic error", fail)
	}

	nodes := h.gw.RANNodes()
	if len(nodes) != 2 || nodes[0].State != ng.RANNodeUp || nodes[1].GlobalGNBID != nil {
		t.Fatalf("RAN nodes = %+v", nodes)
	}

	gnb2.Send(t, setupRequest(t, 2, "gnb2", ngap.PagingDRXv128, ta(t, 2, "00101")))
	expect(t, gnb2, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
}

func TestDuplicateGlobalGNBIDFromTheSameAddressesReplacesTheOldAssociation(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.103")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.103"))
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 1, 10)

	rebooted := h.connectGNB(t)
	rebooted.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))
	expect(t, rebooted, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	gnb.Closed(t)

	expectReset(t, amf, connection(&ue.amfID, ue.amfRANID))
	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })

	nodes := h.gw.RANNodes()
	if len(nodes) != 1 || nodes[0].State != ng.RANNodeUp || nodes[0].GlobalGNBID == nil {
		t.Fatalf("RAN nodes = %+v", nodes)
	}
}

func TestExtendedAMFNameReplacesTheAMFName(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.94")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.94"))

	gnb := h.connectGNB(t)
	gnb.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	resp := setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0))
	resp.ExtendedAMFName = &ngap.ExtendedAMFName{AMFNameUTF8String: ngap.Ptr(ngap.AMFNameUTF8String("ämf-1"))}
	amf, _ := setupAMF(t, amfs, resp)
	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	if st, _ := h.gw.AMFStatus(1, []string{"127.0.1.94"}); st.AMFName != "ämf-1" {
		t.Fatalf("AMF name = %q, want the Extended AMF Name", st.AMFName)
	}

	amf.Send(t, &ngap.AMFConfigurationUpdate{
		AMFName:         ngap.Ptr("ignored"),
		ExtendedAMFName: &ngap.ExtendedAMFName{AMFNameVisibleString: ngap.Ptr(ngap.AMFNameVisibleString("amf-2"))},
	})
	expect(t, amf, "successful", ngap.ProcAMFConfigurationUpdate, ngap.ParseAMFConfigurationUpdateAcknowledge)

	h.eventually(t, "the Extended AMF Name updated", func() bool {
		st, _ := h.gw.AMFStatus(1, []string{"127.0.1.94"})
		return st.AMFName == "amf-2"
	})
}

func TestUEsReleasedWhenTheAMFDoesNotRetainThem(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.95")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.95"))
	gnb, amf := upAndRunning(t, h, amfs)
	kept := attach(t, gnb, amf, 1, 10)

	_ = amf.Conn.Abort()

	resp := setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0))
	resp.UERetentionInformation = ngap.Ptr(ngap.UERetentionUesRetained)

	amf, req := setupAMF(t, amfs, resp)
	if req.UERetentionInformation == nil || *req.UERetentionInformation != ngap.UERetentionUesRetained {
		t.Fatalf("NG Setup Request UE Retention Information = %v", req.UERetentionInformation)
	}

	gnb.Quiet(t, 300*time.Millisecond)
	expectUplinkOn(t, gnb, amf, kept)

	released := attach(t, gnb, amf, 2, 20)

	_ = amf.Conn.Abort()

	setupAMF(t, amfs, setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0)))

	expectReset(t, gnb, connection(&kept.ranAMFID, kept.ranID), connection(&released.ranAMFID, released.ranID))
	h.eventually(t, "the UE contexts released", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestUEsOfAnEarlierAssociationReleasedAfterAFlapWithoutNGSetup(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.101")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.101"))
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 1, 10)

	_ = amf.Conn.Abort()

	flap := amfs.Accept(t)
	expect(t, flap, "initiating", ngap.ProcNGSetup, ngap.ParseNGSetupRequest)

	_ = flap.Conn.Abort()

	setupAMF(t, amfs, setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0)))

	expectReset(t, gnb, connection(&ue.ranAMFID, ue.ranID))
	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestResetUEsOfALostAMFReportedToAnotherAMFOfTheSet(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoAMFs(t, "127.0.1.96", "127.0.1.97", guami(t, "00101", 2, 1, 0), guami(t, "00101", 2, 1, 2))

	ue := attach(t, x.gnb, x.amf1, 1, 10)
	setUpContext(t, x.gnb, x.amf1, ue)

	_ = x.amf1.Conn.Abort()

	expect(t, x.gnb, "initiating", ngap.ProcAMFConfigurationUpdate, ngap.ParseAMFConfigurationUpdate)
	x.gnb.Send(t, &ngap.AMFConfigurationUpdateAcknowledge{})

	cause := ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkUnspecified}
	x.gnb.Send(t, &ngap.NGReset{Cause: &cause, ResetType: ngap.ResetType{Part: []ngap.UEAssociatedLogicalNGConnectionItem{
		connection(&ue.ranAMFID, ue.ranID),
	}}})
	expect(t, x.gnb, "successful", ngap.ProcNGReset, ngap.ParseNGResetAcknowledge)

	expectReset(t, x.amf2, connection(&ue.amfID, ue.amfRANID))
	x.h.eventually(t, "the UE context released", func() bool { return len(x.h.gw.UEs()) == 0 })
}

func TestAMFConfigurationUpdateEndsTheOverloadOfGUAMIsNoLongerServed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.98")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.98"))
	gnb, amf := upAndRunning(t, h, amfs)

	amf.Send(t, &ngap.OverloadStart{
		AMFOverloadResponse: &ngap.OverloadResponse{OverloadAction: ngap.OverloadActionRejectRRCCRSignalling},
	})
	expect(t, gnb, "initiating", ngap.ProcOverloadStart, ngap.ParseOverloadStart)

	amf.Send(t, &ngap.AMFConfigurationUpdate{ServedGUAMIList: ngap.ServedGUAMIList{{GUAMI: guami(t, "00101", 2, 1, 5)}}})
	expect(t, amf, "successful", ngap.ProcAMFConfigurationUpdate, ngap.ParseAMFConfigurationUpdateAcknowledge)

	for {
		f := gnb.NextFrame(t)

		m, ok := f.PDU.(*ngap.InitiatingMessage)
		if ok && m.ProcedureCode == ngap.ProcAMFConfigurationUpdate {
			continue
		}

		if !ok || m.ProcedureCode != ngap.ProcOverloadStop {
			t.Fatalf("got %s, want Overload Stop", procedureName(f.PDU))
		}

		stop, err := ngap.ParseOverloadStop(m.Value)
		if err != nil || len(stop.GUAMIList) != 1 || stop.GUAMIList[0].GUAMI != guami(t, "00101", 2, 1, 0) {
			t.Fatalf("Overload Stop = %+v, %v", stop, err)
		}

		break
	}

	if st, _ := h.gw.AMFStatus(1, []string{"127.0.1.98"}); len(st.Overload) != 0 {
		t.Fatalf("overload = %+v, want none", st.Overload)
	}
}

func TestOverloadReplayedWhenARANConfigurationUpdateAddsAPLMN(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfsA := listenAMF(t, "127.0.1.99")
	amfsB := listenAMFAt(t, "127.0.1.100", amfsA.Port())
	h := startGateway(t, amfsA.Port(), operatorA("127.0.1.99"), operatorB("127.0.1.100"))

	gnbA := h.connectGNB(t)
	gnbA.Send(t, setupRequest(t, 1, "gnbA", ngap.PagingDRXv128, ta(t, 1, "00101")))
	amfA, _ := setupAMF(t, amfsA, setupResponse(t, "amfA", 255, "00101", guami(t, "00101", 2, 1, 0)))
	expect(t, gnbA, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	gnbB := h.connectGNB(t)
	gnbB.Send(t, setupRequest(t, 2, "gnbB", ngap.PagingDRXv128, ta(t, 2, "002002")))
	setupAMF(t, amfsB, setupResponse(t, "amfB", 255, "002002", guami(t, "002002", 2, 1, 0)))
	expect(t, gnbB, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	amfA.Send(t, &ngap.OverloadStart{
		AMFOverloadResponse: &ngap.OverloadResponse{OverloadAction: ngap.OverloadActionRejectRRCCRSignalling},
	})

	for {
		if m, ok := gnbA.NextFrame(t).PDU.(*ngap.InitiatingMessage); ok && m.ProcedureCode == ngap.ProcOverloadStart {
			break
		}
	}

	gnbB.Send(t, &ngap.RANConfigurationUpdate{SupportedTAList: ngap.SupportedTAList{ta(t, 2, "002002"), ta(t, 3, "00101")}})

	var acked, started bool

	for !acked || !started {
		f := gnbB.NextFrame(t)

		switch m := f.PDU.(type) {
		case *ngap.SuccessfulOutcome:
			acked = acked || m.ProcedureCode == ngap.ProcRANConfigurationUpdate
		case *ngap.InitiatingMessage:
			if m.ProcedureCode == ngap.ProcOverloadStart {
				if !acked {
					t.Fatal("Overload Start sent before the RAN Configuration Update Acknowledge")
				}

				start, err := ngap.ParseOverloadStart(m.Value)
				if err != nil || len(start.GUAMIList) != 1 || start.GUAMIList[0].GUAMI != guami(t, "00101", 2, 1, 0) {
					t.Fatalf("Overload Start = %+v, %v", start, err)
				}

				started = true
			}
		}
	}
}
