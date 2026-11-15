package ng_test

import (
	"testing"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
)

func TestRepeatedNGSetupReinitialisesUEContexts(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.51")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.51"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	gnb.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	reset := expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(assoc.Items(reset.ResetType.PartOfNGInterface)) != 1 || *assoc.Items(reset.ResetType.PartOfNGInterface)[0].RANUENGAPID != ue.amfRANID ||
		!reset.Cause.Equal(ngap.CauseRadioNetworkReleaseDueToNGRANGeneratedReason.Cause()) {
		t.Fatalf("NG Reset = %+v", reset)
	}

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs = %+v", ues)
	}
}
