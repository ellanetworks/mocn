package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
)

func TestNonUEProceduresTerminatedLocally(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.50")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.50"))
	gnb, amf := upAndRunning(t, h, amfs)

	amf.Send(t, &ngap.PWSCancelRequest{MessageIdentifier: 0x1112, SerialNumber: 1})
	gnb.Quiet(t, 300*time.Millisecond)

	ind := expectProtocolError(t, amf, ngap.CauseProtocolAbstractSyntaxErrorReject)
	assertDiagnostics(t, ind.CriticalityDiagnostics, ngap.ProcPWSCancel, ngap.Ptr(ngap.CriticalityReject))
	amf.Quiet(t, 100*time.Millisecond)
}

func TestRepeatedNGSetupReinitialisesUEContexts(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.51")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.51"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	gnb.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	reset := expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].RANUENGAPID != ue.amfRANID ||
		reset.Cause.Value != ngap.CauseRadioNetworkReleaseDueToNGRANGeneratedReason {
		t.Fatalf("NG Reset = %+v", reset)
	}

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs = %+v", ues)
	}
}
