package s1_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func successful(proc s1ap.ProcedureCode, value []byte) rawPDU {
	return rawPDU{&s1ap.SuccessfulOutcome{ProcedureCode: proc, Criticality: s1ap.CriticalityReject, Value: value}}
}

func unsuccessful(proc s1ap.ProcedureCode, value []byte) rawPDU {
	return rawPDU{&s1ap.UnsuccessfulOutcome{ProcedureCode: proc, Criticality: s1ap.CriticalityReject, Value: value}}
}

func TestInvalidS1SetupAnswersResetTheAssociation(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.82")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.82"))
	mme := mmeListener.Accept(t)

	enb := h.connectENB(t)
	enb.Send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv64, ta(t, 1, "00101")))

	expect(t, mme, "initiating", s1ap.ProcS1Setup, s1ap.ParseS1SetupRequest)
	mme.Send(t, successful(s1ap.ProcS1Setup, []byte{0x00, 0x00, 0x01, 0xff}))
	mme.Closed(t)

	mme = mmeListener.Accept(t)
	expect(t, mme, "initiating", s1ap.ProcS1Setup, s1ap.ParseS1SetupRequest)
	mme.Send(t, unsuccessful(s1ap.ProcS1Setup, []byte{0x00, 0x00, 0x01, 0xff}))
	mme.Closed(t)

	mme = mmeListener.Accept(t)
	expect(t, mme, "initiating", s1ap.ProcS1Setup, s1ap.ParseS1SetupRequest)
	mme.Send(t, &s1ap.S1SetupResponse{ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1))})

	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
	assertSetupAttempts(t, h, map[string]float64{"radio/success": 1, "core/failure": 2, "core/success": 1})
}

func TestS1SetupRequestThatCannotBeBuilt(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	op := operatorA("127.0.0.83")
	op.GlobalENBID.Type = db.ENBIDType("unknown")

	mmeListener := listenMME(t, "127.0.0.83")
	h := startGateway(t, mmeListener.Port(), op)
	mme := mmeListener.Accept(t)

	enb := h.connectENB(t)
	enb.Send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv64, ta(t, 1, "00101")))

	mme.Closed(t)
	expect(t, enb, "unsuccessful", s1ap.ProcS1Setup, s1ap.ParseS1SetupFailure)

	h.eventually(t, "a failed S1 Setup Request counted", func() bool {
		return testCounter(t, h, "core", "error") > 0
	})
}

func TestENBConfigurationUpdateRetriedAfterAFailure(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.84")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.84"))
	_, mme := upAndRunning(t, h, mmeListener)

	mme.Send(t, &s1ap.ENBConfigurationUpdateFailure{
		Cause: s1ap.Ptr(s1ap.CauseMiscUnspecified.Cause()),
	})
	mme.Send(t, &s1ap.MMEConfigurationUpdate{})
	expect(t, mme, "successful", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdateAcknowledge)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 2, "00101")))

	first := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	mme.Send(t, &s1ap.ENBConfigurationUpdateFailure{
		Cause:      s1ap.Ptr(s1ap.CauseMiscUnspecified.Cause()),
		TimeToWait: s1ap.Ptr(s1ap.TimeToWaitV1s),
	})

	mme.Quiet(t, 700*time.Millisecond)

	again := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, again.SupportedTAs, first.SupportedTAs)
	mme.Send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})
	mme.Quiet(t, 1200*time.Millisecond)
}

func TestMMEConfigurationUpdateRetriedAfterTheRadioRejectsIt(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.85")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.85"))
	enb, mme := upAndRunning(t, h, mmeListener)

	mme.Send(t, &s1ap.MMEConfigurationUpdate{ServedGUMMEIs: served(t, 1, 1, 2)})
	expect(t, mme, "successful", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdateAcknowledge)

	first := expect(t, enb, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
	enb.Send(t, &s1ap.MMEConfigurationUpdateFailure{
		Cause:      s1ap.Ptr(s1ap.CauseMiscUnspecified.Cause()),
		TimeToWait: s1ap.Ptr(s1ap.TimeToWaitV1s),
	})

	enb.Quiet(t, 700*time.Millisecond)

	again := expect(t, enb, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
	if len(again.ServedGUMMEIs) != 1 || len(again.ServedGUMMEIs[0].ServedMMECs) != len(first.ServedGUMMEIs[0].ServedMMECs) {
		t.Fatalf("retried MME Configuration Update = %+v, want %+v", again, first)
	}

	enb.Send(t, &s1ap.MMEConfigurationUpdateAcknowledge{})
	enb.Quiet(t, 1200*time.Millisecond)
}

func testCounter(t *testing.T, h *harness, side, result string) float64 {
	t.Helper()

	return testutil.ToFloat64(h.metrics.Setups.WithLabelValues("s1", side, result))
}
