package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/ng"
)

type rawPDU struct {
	pdu ngap.PDU
}

func (r rawPDU) Marshal() ([]byte, error) {
	return ngap.Marshal(r.pdu)
}

func bodyOf(t *testing.T, msg interface{ Marshal() ([]byte, error) }) []byte {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	pdu, err := ngap.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}

	switch m := pdu.(type) {
	case *ngap.InitiatingMessage:
		return m.Value
	case *ngap.SuccessfulOutcome:
		return m.Value
	case *ngap.UnsuccessfulOutcome:
		return m.Value
	}

	t.Fatal("no message body")

	return nil
}

func initiating(proc ngap.ProcedureCode, crit ngap.Criticality, value []byte) rawPDU {
	return rawPDU{&ngap.InitiatingMessage{ProcedureCode: proc, Criticality: crit, Value: value}}
}

func expectProtocolError(t *testing.T, p *peer, cause int) *ngap.ErrorIndication {
	t.Helper()

	ind := expect(t, p, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || ind.Cause.Group != ngap.CauseGroupProtocol || ind.Cause.Value != cause {
		t.Fatalf("Error Indication cause = %v, want protocol %d", ind.Cause, cause)
	}

	return ind
}

func assertDiagnostics(t *testing.T, d *ngap.CriticalityDiagnostics, proc ngap.ProcedureCode, crit *ngap.Criticality) {
	t.Helper()

	if d == nil || d.ProcedureCode == nil || *d.ProcedureCode != proc || d.TriggeringMessage == nil ||
		*d.TriggeringMessage != ngap.TriggeringInitiatingMessage {
		t.Fatalf("Criticality Diagnostics = %+v, want procedure %s, initiating message", d, proc)
	}

	if crit != nil && (d.ProcedureCriticality == nil || *d.ProcedureCriticality != *crit) {
		t.Fatalf("Procedure Criticality = %v, want %s", d.ProcedureCriticality, *crit)
	}
}

func expectReset(t *testing.T, p *peer, want ...ngap.UEAssociatedLogicalNGConnectionItem) {
	t.Helper()

	reset := expect(t, p, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if reset.ResetType.All || len(reset.ResetType.Part) != len(want) {
		t.Fatalf("NG Reset = %+v, want a partial reset of %d connections", reset.ResetType, len(want))
	}

	for i, w := range want {
		got := reset.ResetType.Part[i]
		if (got.AMFUENGAPID == nil) != (w.AMFUENGAPID == nil) || (got.RANUENGAPID == nil) != (w.RANUENGAPID == nil) ||
			(w.AMFUENGAPID != nil && *got.AMFUENGAPID != *w.AMFUENGAPID) ||
			(w.RANUENGAPID != nil && *got.RANUENGAPID != *w.RANUENGAPID) {
			t.Fatalf("NG Reset item %d = %v/%v, want %v/%v", i, got.AMFUENGAPID, got.RANUENGAPID, w.AMFUENGAPID, w.RANUENGAPID)
		}
	}
}

func connection(amf *ngap.AMFUENGAPID, ran ngap.RANUENGAPID) ngap.UEAssociatedLogicalNGConnectionItem {
	return ngap.UEAssociatedLogicalNGConnectionItem{AMFUENGAPID: amf, RANUENGAPID: &ran}
}

func uplinkNAS(t *testing.T, amfID ngap.AMFUENGAPID, ranID ngap.RANUENGAPID) *ngap.UplinkNASTransport {
	t.Helper()

	return &ngap.UplinkNASTransport{
		AMFUENGAPID: amfID, RANUENGAPID: ranID, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1)),
	}
}

func TestUnsupportedProceduresRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.60")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.60"))
	gnb, amf := upAndRunning(t, h, amfs)

	reject := ngap.CriticalityReject
	ueBody := bodyOf(t, uplinkNAS(t, 11, 7))
	nonUEBody := bodyOf(t, &ngap.ErrorIndication{Cause: &causeUnknownLocal})

	gnb.Send(t, initiating(ngap.ProcPathSwitchRequest, reject, ueBody))

	ind := expectProtocolError(t, gnb, ngap.CauseProtocolAbstractSyntaxErrorReject)
	assertDiagnostics(t, ind.CriticalityDiagnostics, ngap.ProcPathSwitchRequest, &reject)

	if ind.RANUENGAPID == nil || *ind.RANUENGAPID != 7 {
		t.Fatalf("Error Indication RAN UE NGAP ID = %v, want 7", ind.RANUENGAPID)
	}

	for _, proc := range []ngap.ProcedureCode{
		ngap.ProcRetrieveUEInformation, ngap.ProcUEInformationTransfer,
	} {
		amf.Send(t, initiating(proc, reject, nonUEBody))

		ind := expectProtocolError(t, amf, ngap.CauseProtocolAbstractSyntaxErrorReject)
		assertDiagnostics(t, ind.CriticalityDiagnostics, proc, &reject)
	}

	gnb.Send(t, initiating(ngap.ProcUERadioCapabilityIDMapping, reject, nonUEBody))
	assertDiagnostics(t, expectProtocolError(t, gnb, ngap.CauseProtocolAbstractSyntaxErrorReject).CriticalityDiagnostics,
		ngap.ProcUERadioCapabilityIDMapping, &reject)

	notify := ngap.CriticalityNotify
	amf.Send(t, initiating(ngap.ProcRetrieveUEInformation, notify, nonUEBody))
	assertDiagnostics(t, expectProtocolError(t, amf, ngap.CauseProtocolAbstractSyntaxErrorIgnoreAndNotify).CriticalityDiagnostics,
		ngap.ProcRetrieveUEInformation, &notify)

	amf.Send(t, initiating(ngap.ProcDownlinkRIMInformationTransfer, ngap.CriticalityIgnore, nonUEBody))
	amf.Quiet(t, 200*time.Millisecond)
	gnb.Quiet(t, 0)
}

func TestGWContextReleaseIndicationReleasesTheUE(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.61")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.61"))
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 3, 77)

	gnb.Send(t, &ngap.UEContextReleaseRequest{
		AMFUENGAPID: ue.ranAMFID, RANUENGAPID: ue.ranID, Cause: &causeUnknownLocal,
		GWContextReleaseIndication: ngap.Ptr(ngap.GWContextReleaseIndicationTrue),
	})

	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
	amf.Quiet(t, 200*time.Millisecond)
	gnb.Quiet(t, 0)
}

func TestErrorIndicationWithOnlyTheRANUEID(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.62")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.62"))
	gnb, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb, amf, 3, 77)

	amf.Send(t, &ngap.ErrorIndication{RANUENGAPID: &ue.amfRANID, Cause: &causeUnknownLocal})

	expectReset(t, gnb, connection(&ue.ranAMFID, ue.ranID))
	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
	amf.Quiet(t, 200*time.Millisecond)
}

func TestUnansweredInitialUEMessageReleasesTheUE(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	const timeout = 300 * time.Millisecond

	amfs := listenAMF(t, "127.0.1.63")
	h := startGatewayWith(t, ng.Config{CorePort: amfs.Port(), ResponseTimeout: timeout}, operatorA("127.0.1.63"))
	gnb, amf := upAndRunning(t, h, amfs)

	gnb.Send(t, initialUE(t, 5, "00101"))

	_, raw := expectRaw(t, amf, "initiating", ngap.ProcInitialUEMessage)
	_, amfRANID := ueIDs(t, raw)

	expectReset(t, amf, connection(nil, amfRANID))
	expectReset(t, gnb, connection(nil, 5))

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs = %+v, want none", ues)
	}

	ue := attach(t, gnb, amf, 5, 78)

	amf.Quiet(t, 3*timeout)
	gnb.Quiet(t, 0)
	gnb.Send(t, uplinkNAS(t, ue.ranAMFID, ue.ranID))
	expectRaw(t, amf, "initiating", ngap.ProcUplinkNASTransport)
}

func TestMessagesBeforeNGSetup(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.64")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.64"))
	amf := amfs.Accept(t)
	gnb := h.connectGNB(t)

	gnb.Send(t, uplinkNAS(t, 11, 7))

	ind := expectProtocolError(t, gnb, ngap.CauseProtocolMessageNotCompatible)
	assertDiagnostics(t, ind.CriticalityDiagnostics, ngap.ProcUplinkNASTransport, nil)

	if ind.AMFUENGAPID == nil || *ind.AMFUENGAPID != 11 || ind.RANUENGAPID == nil || *ind.RANUENGAPID != 7 {
		t.Fatalf("Error Indication IDs = %v/%v, want 11/7", ind.AMFUENGAPID, ind.RANUENGAPID)
	}

	gnb.Send(t, initiating(ngap.ProcHandoverPreparation, ngap.CriticalityReject, bodyOf(t, uplinkNAS(t, 11, 7))))

	fail := expect(t, gnb, "unsuccessful", ngap.ProcHandoverPreparation, ngap.ParseHandoverPreparationFailure)
	if fail.Cause == nil || fail.Cause.Value != ngap.CauseProtocolMessageNotCompatible || *fail.AMFUENGAPID != 11 ||
		*fail.RANUENGAPID != 7 {
		t.Fatalf("Handover Preparation Failure = %+v", fail)
	}

	amf.Send(t, &ngap.Paging{
		FiveGSTMSI:       &ngap.FiveGSTMSI{AMFSetID: 1, FiveGTMSI: 0xdeadbeef},
		TAIListForPaging: []ngap.TAI{{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1}},
	})
	assertDiagnostics(t, expectProtocolError(t, amf, ngap.CauseProtocolMessageNotCompatible).CriticalityDiagnostics,
		ngap.ProcPaging, nil)

	amf.Send(t, contextSetup(t, attached{amfID: 5, amfRANID: 6}))

	ics := expect(t, amf, "unsuccessful", ngap.ProcInitialContextSetup, ngap.ParseInitialContextSetupFailure)
	if ics.Cause == nil || ics.Cause.Value != ngap.CauseProtocolMessageNotCompatible || *ics.AMFUENGAPID != 5 ||
		*ics.RANUENGAPID != 6 {
		t.Fatalf("Initial Context Setup Failure = %+v", ics)
	}

	amf.Send(t, &ngap.ErrorIndication{Cause: &causeUnknownLocal})
	amf.Quiet(t, 200*time.Millisecond)

	amf.SendRaw(t, []byte{0xff, 0xff})
	expectProtocolError(t, amf, ngap.CauseProtocolTransferSyntaxError)

	gnb.SendRaw(t, []byte{0xff, 0xff})
	expectProtocolError(t, gnb, ngap.CauseProtocolTransferSyntaxError)
}

func TestInitialUEMessageWithoutNASPDU(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.65")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.65"))
	gnb, amf := upAndRunning(t, h, amfs)

	b, err := initialUE(t, 9, "00101").Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := ngap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Remove(p.Index(ngap.IDNASPDU)); err != nil {
		t.Fatal(err)
	}

	if b, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	gnb.SendRaw(t, b)

	ind := expectProtocolError(t, gnb, ngap.CauseProtocolAbstractSyntaxErrorReject)
	assertDiagnostics(t, ind.CriticalityDiagnostics, ngap.ProcInitialUEMessage, nil)

	ies := ind.CriticalityDiagnostics.IEsCriticalityDiagnostics
	if len(ies) != 1 || ies[0].IEID != ngap.IDNASPDU || ies[0].TypeOfError != ngap.TypeOfErrorMissing {
		t.Fatalf("IE diagnostics = %+v, want the NAS-PDU missing", ies)
	}

	if ind.RANUENGAPID == nil || *ind.RANUENGAPID != 9 {
		t.Fatalf("Error Indication RAN UE NGAP ID = %v, want 9", ind.RANUENGAPID)
	}

	amf.Quiet(t, 200*time.Millisecond)
}
