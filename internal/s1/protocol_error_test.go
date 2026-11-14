package s1_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/s1"
)

func bodyOf(t *testing.T, msg interface{ Marshal() ([]byte, error) }) []byte {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	pdu, err := s1ap.Unmarshal(b)
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

	t.Fatal("no message body")

	return nil
}

func initiating(proc s1ap.ProcedureCode, crit s1ap.Criticality, value []byte) rawPDU {
	return rawPDU{&s1ap.InitiatingMessage{ProcedureCode: proc, Criticality: crit, Value: value}}
}

func assertDiagnostics(t *testing.T, d *s1ap.CriticalityDiagnostics, proc s1ap.ProcedureCode, crit *s1ap.Criticality) {
	t.Helper()

	if d == nil || d.ProcedureCode == nil || *d.ProcedureCode != proc || d.TriggeringMessage == nil ||
		*d.TriggeringMessage != s1ap.TriggeringInitiatingMessage {
		t.Fatalf("Criticality Diagnostics = %+v, want procedure %s, initiating message", d, proc)
	}

	if crit != nil && (d.ProcedureCriticality == nil || *d.ProcedureCriticality != *crit) {
		t.Fatalf("Procedure Criticality = %v, want %s", d.ProcedureCriticality, *crit)
	}
}

func TestUnsupportedProceduresRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.60")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.60"))
	enb, mme := upAndRunning(t, h, mmeListener)

	reject := s1ap.CriticalityReject
	ueBody := bodyOf(t, uplinkNAS(t, 11, 7))
	nonUEBody := bodyOf(t, &s1ap.ErrorIndication{Cause: &s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscUnspecified}})

	enb.Send(t, initiating(s1ap.ProcPathSwitchRequest, reject, ueBody))

	ind := expectProtocolError(t, enb, s1ap.CauseProtocolAbstractSyntaxErrorReject)
	assertDiagnostics(t, ind.CriticalityDiagnostics, s1ap.ProcPathSwitchRequest, &reject)

	if ind.ENBUES1APID == nil || *ind.ENBUES1APID != 7 {
		t.Fatalf("Error Indication eNB UE S1AP ID = %v, want 7", ind.ENBUES1APID)
	}

	for _, proc := range []s1ap.ProcedureCode{s1ap.ProcRetrieveUEInformation, s1ap.ProcMMEDirectInformationTransfer} {
		mme.Send(t, initiating(proc, reject, nonUEBody))

		ind := expectProtocolError(t, mme, s1ap.CauseProtocolAbstractSyntaxErrorReject)
		assertDiagnostics(t, ind.CriticalityDiagnostics, proc, &reject)
	}

	enb.Send(t, initiating(s1ap.ProcUERadioCapabilityIDMapping, reject, nonUEBody))
	assertDiagnostics(t, expectProtocolError(t, enb, s1ap.CauseProtocolAbstractSyntaxErrorReject).CriticalityDiagnostics,
		s1ap.ProcUERadioCapabilityIDMapping, &reject)

	notify := s1ap.CriticalityNotify
	mme.Send(t, initiating(s1ap.ProcRetrieveUEInformation, notify, nonUEBody))
	assertDiagnostics(t, expectProtocolError(t, mme, s1ap.CauseProtocolAbstractSyntaxErrorIgnoreAndNotify).CriticalityDiagnostics,
		s1ap.ProcRetrieveUEInformation, &notify)

	mme.Send(t, initiating(s1ap.ProcMMEDirectInformationTransfer, s1ap.CriticalityIgnore, nonUEBody))
	mme.Quiet(t, 200*time.Millisecond)
	enb.Quiet(t, 0)
}

func TestGWContextReleaseIndicationReleasesTheUE(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.61")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.61"))
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 3, 77)

	b, err := (&s1ap.UEContextReleaseRequest{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID, Cause: &causeUserInactivity,
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := s1ap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Insert(p.Len(), s1ap.RawIE{ID: s1ap.IDGWContextReleaseIndication, Criticality: s1ap.CriticalityReject, Value: []byte{0x00}}); err != nil {
		t.Fatal(err)
	}

	if b, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	enb.SendRaw(t, b)

	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
	mme.Quiet(t, 200*time.Millisecond)
	enb.Quiet(t, 0)

	enb.Send(t, uplinkNAS(t, ue.ranMMEID, ue.enbUEID))
	expectErrorIndication(t, enb, s1ap.CauseRadioNetworkUnknownMMEUES1APID, &ue.ranMMEID, &ue.enbUEID)
}

var causeUserInactivity = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUserInactivity}

func TestErrorIndicationWithOnlyTheENBUEID(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.62")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.62"))
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 3, 77)

	cause := s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownENBUES1APID}
	mme.Send(t, &s1ap.ErrorIndication{ENBUES1APID: &ue.mmeENBID, Cause: &cause})

	expectReset(t, enb, item(ue.ranMMEID, ue.enbUEID))
	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
	mme.Quiet(t, 200*time.Millisecond)
}

func TestUnansweredInitialUEMessageReleasesTheUE(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	const timeout = 300 * time.Millisecond

	mmeListener := listenMME(t, "127.0.0.63")
	h := startGatewayWith(t, s1.Config{CorePort: mmeListener.Port(), ResponseTimeout: timeout}, operatorA("127.0.0.63"))
	enb, mme := upAndRunning(t, h, mmeListener)

	enb.Send(t, initialUE(t, 5))

	_, raw := expectRaw(t, mme, "initiating", s1ap.ProcInitialUEMessage)
	_, mmeENBID := ids(t, raw)

	expectReset(t, mme, s1ap.UEAssociatedLogicalS1ConnectionItem{ENBUES1APID: &mmeENBID})
	expectReset(t, enb, s1ap.UEAssociatedLogicalS1ConnectionItem{ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(5))})

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs = %+v, want none", ues)
	}

	ue := attach(t, enb, mme, 5, 78)

	mme.Quiet(t, 3*timeout)
	enb.Quiet(t, 0)
	enb.Send(t, uplinkNAS(t, ue.ranMMEID, ue.enbUEID))
	expectRaw(t, mme, "initiating", s1ap.ProcUplinkNASTransport)
}

func TestMessagesBeforeS1Setup(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.64")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.64"))
	mme := mmeListener.Accept(t)
	enb := h.connectENB(t)

	enb.Send(t, uplinkNAS(t, 11, 7))

	ind := expect(t, enb, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication)
	if ind.Cause == nil || ind.Cause.Group != s1ap.CauseGroupProtocol || ind.Cause.Value != s1ap.CauseProtocolMessageNotCompatible {
		t.Fatalf("Error Indication cause = %v, want message not compatible with receiver state", ind.Cause)
	}

	assertDiagnostics(t, ind.CriticalityDiagnostics, s1ap.ProcUplinkNASTransport, nil)

	if ind.MMEUES1APID == nil || *ind.MMEUES1APID != 11 || ind.ENBUES1APID == nil || *ind.ENBUES1APID != 7 {
		t.Fatalf("Error Indication IDs = %v/%v, want 11/7", ind.MMEUES1APID, ind.ENBUES1APID)
	}

	enb.Send(t, initiating(s1ap.ProcHandoverPreparation, s1ap.CriticalityReject, bodyOf(t, uplinkNAS(t, 11, 7))))

	fail := expect(t, enb, "unsuccessful", s1ap.ProcHandoverPreparation, s1ap.ParseHandoverPreparationFailure)
	if fail.Cause == nil || fail.Cause.Value != s1ap.CauseProtocolMessageNotCompatible || *fail.MMEUES1APID != 11 ||
		*fail.ENBUES1APID != 7 {
		t.Fatalf("Handover Preparation Failure = %+v", fail)
	}

	mme.Send(t, paging(t, tai(t, "00101", 1)))
	assertDiagnostics(t, expectProtocolError(t, mme, s1ap.CauseProtocolMessageNotCompatible).CriticalityDiagnostics,
		s1ap.ProcPaging, nil)

	mme.Send(t, contextSetup(attached{enbUEID: 6, ranMMEID: 5}, false))

	ics := expect(t, mme, "unsuccessful", s1ap.ProcInitialContextSetup, s1ap.ParseInitialContextSetupFailure)
	if ics.Cause == nil || ics.Cause.Value != s1ap.CauseProtocolMessageNotCompatible || *ics.MMEUES1APID != 5 {
		t.Fatalf("Initial Context Setup Failure = %+v", ics)
	}

	mme.Send(t, &s1ap.ErrorIndication{Cause: &causeUserInactivity})
	mme.Quiet(t, 200*time.Millisecond)

	mme.SendRaw(t, []byte{0xff, 0xff})
	expectProtocolError(t, mme, s1ap.CauseProtocolTransferSyntaxError)
}

func TestInitialUEMessageWithoutNASPDU(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.65")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.65"))
	enb, mme := upAndRunning(t, h, mmeListener)

	b, err := initialUE(t, 9).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := s1ap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Remove(p.Index(s1ap.IDNASPDU)); err != nil {
		t.Fatal(err)
	}

	if b, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	enb.SendRaw(t, b)

	ind := expectProtocolError(t, enb, s1ap.CauseProtocolAbstractSyntaxErrorReject)
	assertDiagnostics(t, ind.CriticalityDiagnostics, s1ap.ProcInitialUEMessage, nil)

	ies := ind.CriticalityDiagnostics.IEsCriticalityDiagnostics
	if len(ies) != 1 || ies[0].IEID != s1ap.IDNASPDU || ies[0].TypeOfError != s1ap.TypeOfErrorMissing {
		t.Fatalf("IE diagnostics = %+v, want the NAS-PDU missing", ies)
	}

	if ind.ENBUES1APID == nil || *ind.ENBUES1APID != 9 {
		t.Fatalf("Error Indication eNB UE S1AP ID = %v, want 9", ind.ENBUES1APID)
	}

	mme.Quiet(t, 200*time.Millisecond)
}
