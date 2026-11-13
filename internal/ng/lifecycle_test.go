package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
)

func TestNonUEProceduresTerminatedLocally(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.50")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.50"))
	gnb, amf := upAndRunning(t, h, amfs)

	amf.send(t, &ngap.PWSCancelRequest{MessageIdentifier: 0x1112, SerialNumber: 1})
	gnb.quiet(t, 300*time.Millisecond)
	amf.quiet(t, 100*time.Millisecond)
}

func TestRepeatedNGSetupReinitialisesUEContexts(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.51")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.51"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

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
