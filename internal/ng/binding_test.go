package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type twoAMFs struct {
	h          *harness
	gnb        *peer
	amf1, amf2 *peer
}

func startTwoAMFs(t *testing.T, ip1, ip2 string, g1, g2 ngap.GUAMI) twoAMFs {
	t.Helper()

	amfs1 := listenAMF(t, ip1)
	amfs2 := listenAMFAt(t, ip2, amfs1.Port())
	h := startGatewayWith(t, ng.Config{CorePort: amfs1.Port(), IntN: func(int) int { return 0 }}, operatorA(ip1, ip2))

	gnb := h.connectGNB(t)
	gnb.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	amf1, _ := setupAMF(t, amfs1, setupResponse(t, "amf1", 255, "00101", g1))
	amf2, _ := setupAMF(t, amfs2, setupResponse(t, "amf2", 255, "00101", g2))

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	settle(t, h, gnb, ip1, ip2)

	return twoAMFs{h: h, gnb: gnb, amf1: amf1, amf2: amf2}
}

func setUpContext(t *testing.T, gnb, amf *peer, ue attached) {
	t.Helper()

	amf.Send(t, contextSetup(t, ue))
	expectRaw(t, gnb, "initiating", ngap.ProcInitialContextSetup)
}

func expectUplinkOn(t *testing.T, gnb, amf *peer, ue attached) {
	t.Helper()

	gnb.Send(t, uplinkNAS(t, ue.ranAMFID, ue.ranID))

	_, raw := expectRaw(t, amf, "initiating", ngap.ProcUplinkNASTransport)
	if a, r := ueIDs(t, raw); a != ue.amfID || r != ue.amfRANID {
		t.Fatalf("IDs toward the AMF = %d/%d, want %d/%d", a, r, ue.amfID, ue.amfRANID)
	}
}

func TestAMFLossRebindsToAnotherAMFOfTheSet(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoAMFs(t, "127.0.1.70", "127.0.1.71", guami(t, "00101", 2, 1, 0), guami(t, "00101", 2, 1, 2))

	ue := attach(t, x.gnb, x.amf1, 1, 10)
	setUpContext(t, x.gnb, x.amf1, ue)

	_ = x.amf1.Conn.Abort()

	expect(t, x.gnb, "initiating", ngap.ProcAMFConfigurationUpdate, ngap.ParseAMFConfigurationUpdate)
	x.gnb.Send(t, &ngap.AMFConfigurationUpdateAcknowledge{})
	x.gnb.Quiet(t, 300*time.Millisecond)
	expectUplinkOn(t, x.gnb, x.amf2, ue)

	x.amf2.Send(t, &ngap.DownlinkNASTransport{AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID, NASPDU: nas})

	_, raw := expectRaw(t, x.gnb, "initiating", ngap.ProcDownlinkNASTransport)
	if a, r := ueIDs(t, raw); a != ue.ranAMFID || r != ue.ranID {
		t.Fatalf("IDs toward the radio = %d/%d", a, r)
	}

	if ues := x.h.gw.UEs(); len(ues) != 1 || ues[0].AMFTNLAddresses[0] != "127.0.1.71" {
		t.Fatalf("UEs = %+v", ues)
	}
}

func TestAMFStatusIndicationRebindsToTheBackupAMF(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	g1 := guami(t, "00101", 2, 1, 0)
	x := startTwoAMFs(t, "127.0.1.72", "127.0.1.73", g1, guami(t, "00101", 2, 1, 2))

	ue := attach(t, x.gnb, x.amf1, 1, 10)
	setUpContext(t, x.gnb, x.amf1, ue)

	x.amf1.Send(t, &ngap.AMFStatusIndication{UnavailableGUAMIList: ngap.UnavailableGUAMIList{{
		GUAMI: g1, TimerApproachForGUAMIRemoval: ngap.Ptr(ngap.TimerApproachApplyTimer), BackupAMFName: ngap.Ptr(ngap.Name("amf2")),
	}}})

	ind := expect(t, x.gnb, "initiating", ngap.ProcAMFStatusIndication, ngap.ParseAMFStatusIndication)
	if len(ind.UnavailableGUAMIList) != 1 || ind.UnavailableGUAMIList[0].GUAMI != g1 ||
		ind.UnavailableGUAMIList[0].BackupAMFName != nil || ind.UnavailableGUAMIList[0].TimerApproachForGUAMIRemoval != nil {
		t.Fatalf("AMF Status Indication = %+v", ind.UnavailableGUAMIList)
	}

	expectUplinkOn(t, x.gnb, x.amf2, ue)
	x.amf1.Quiet(t, 200*time.Millisecond)

	x.amf1.Send(t, &ngap.AMFConfigurationUpdate{ServedGUAMIList: ngap.ServedGUAMIList{{GUAMI: g1}}})
	expect(t, x.amf1, "successful", ngap.ProcAMFConfigurationUpdate, ngap.ParseAMFConfigurationUpdateAcknowledge)

	upd := expect(t, x.gnb, "initiating", ngap.ProcAMFConfigurationUpdate, ngap.ParseAMFConfigurationUpdate)
	if len(upd.ServedGUAMIList) != 2 {
		t.Fatalf("AMF Configuration Update = %+v", upd)
	}

	x.gnb.Send(t, &ngap.AMFConfigurationUpdateAcknowledge{})
	x.gnb.Quiet(t, 300*time.Millisecond)
}

func TestUETNLABindingReleaseTerminated(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoAMFs(t, "127.0.1.74", "127.0.1.75", guami(t, "00101", 2, 1, 0), guami(t, "00101", 2, 1, 2))

	ue := attach(t, x.gnb, x.amf1, 1, 10)

	x.amf1.Send(t, &ngap.UETNLABindingReleaseRequest{AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID})

	x.gnb.Quiet(t, 300*time.Millisecond)

	if ues := x.h.gw.UEs(); len(ues) != 1 {
		t.Fatalf("UEs = %+v, want the UE kept", ues)
	}

	expectUplinkOn(t, x.gnb, x.amf1, ue)
	x.amf2.Quiet(t, 200*time.Millisecond)
}

func TestUETNLABindingReleaseWithoutRANUENGAPIDRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.83")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.83"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	amf.Send(t, initiating(ngap.ProcUETNLABindingRelease, ngap.CriticalityIgnore, bodyOf(t, &ngap.ErrorIndication{
		AMFUENGAPID: &ue.amfID, Cause: &causeUnknownLocal,
	})))

	ind := expectProtocolError(t, amf, ngap.CauseProtocolAbstractSyntaxErrorReject)
	assertDiagnostics(t, ind.CriticalityDiagnostics, ngap.ProcUETNLABindingRelease, nil)

	if ind.AMFUENGAPID == nil || *ind.AMFUENGAPID != ue.amfID {
		t.Fatalf("Error Indication AMF UE NGAP ID = %v, want %d", ind.AMFUENGAPID, ue.amfID)
	}

	gnb.Quiet(t, 200*time.Millisecond)
	expectUplinkOn(t, gnb, amf, ue)
}

func TestDownlinkFromAnotherAMFSetRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoAMFs(t, "127.0.1.76", "127.0.1.77", guami(t, "00101", 2, 1, 0), guami(t, "00101", 2, 9, 0))

	ue := attach(t, x.gnb, x.amf1, 1, 10)

	x.amf2.Send(t, &ngap.DownlinkNASTransport{AMFUENGAPID: 2, RANUENGAPID: ue.amfRANID, NASPDU: nas})

	ind := expect(t, x.amf2, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || !ind.Cause.Equal(ngap.CauseRadioNetworkUnknownLocalUENGAPID.Cause()) {
		t.Fatalf("Error Indication = %+v", ind)
	}

	x.gnb.Quiet(t, 200*time.Millisecond)
	expectUplinkOn(t, x.gnb, x.amf1, ue)
}

func TestUEContextReleaseCommandFromAnotherAMFOfTheSet(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoAMFs(t, "127.0.1.78", "127.0.1.79", guami(t, "00101", 2, 1, 0), guami(t, "00101", 2, 1, 2))

	ue := attach(t, x.gnb, x.amf1, 1, 10)

	x.amf2.Send(t, &ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{AMFUENGAPID: new(ue.amfID)}, Cause: &causeUnknownLocal,
	})

	expectRaw(t, x.gnb, "initiating", ngap.ProcUEContextRelease)
	x.gnb.Send(t, &ngap.UEContextReleaseComplete{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID})
	expectRaw(t, x.amf2, "successful", ngap.ProcUEContextRelease)

	x.h.eventually(t, "the UE released", func() bool { return len(x.h.gw.UEs()) == 0 })
	x.amf1.Quiet(t, 200*time.Millisecond)
}

func TestOverloadReplayedAndClearedOnAMFLoss(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.80")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.80"))
	gnb, amf := upAndRunning(t, h, amfs)

	amf.Send(t, &ngap.OverloadStart{
		AMFOverloadResponse: &ngap.OverloadResponse{OverloadAction: new(ngap.OverloadActionPermitEmergencySessionsAndMTServicesOnly)},
	})

	want := ngap.GUAMIList{{GUAMI: guami(t, "00101", 2, 1, 0)}}

	var gnb2 *peer

	for _, g := range []*peer{gnb, nil} {
		if g == nil {
			g = h.connectGNB(t)
			g.Send(t, setupRequest(t, 2, "gnb2", ngap.PagingDRXv128, ta(t, 1, "00101")))
			expect(t, g, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
			gnb2 = g
		}

		start := expect(t, g, "initiating", ngap.ProcOverloadStart, ngap.ParseOverloadStart)
		if len(start.GUAMIList) != 1 || start.GUAMIList[0].GUAMI != want[0].GUAMI || start.AMFOverloadResponse == nil ||
			*start.AMFOverloadResponse.OverloadAction != ngap.OverloadActionPermitEmergencySessionsAndMTServicesOnly {
			t.Fatalf("Overload Start = %+v", start)
		}
	}

	if got := testutil.ToFloat64(h.gw.OverloadCounter().WithLabelValues("ng", "overload_start", "relayed")); got != 2 {
		t.Fatalf("relayed Overload Start = %v, want 2", got)
	}

	_ = amf.Conn.Abort()

	for _, g := range []*peer{gnb, gnb2} {
		stop := expect(t, g, "initiating", ngap.ProcOverloadStop, ngap.ParseOverloadStop)
		if len(stop.GUAMIList) != 1 || stop.GUAMIList[0].GUAMI != want[0].GUAMI {
			t.Fatalf("Overload Stop = %+v", stop)
		}
	}
}

func TestRerouteWithAPartiallyAllowedSNSSAIAlsoAllowed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoAMFs(t, "127.0.1.81", "127.0.1.82", guami(t, "00101", 2, 1, 0), guami(t, "00101", 2, 9, 0))

	x.gnb.Send(t, initialUE(t, 5, "00101"))

	f, raw := expectRaw(t, x.amf1, "initiating", ngap.ProcInitialUEMessage)
	_, amfRANID := ueIDs(t, raw)

	x.amf1.Send(t, &ngap.RerouteNASRequest{
		RANUENGAPID: amfRANID, NGAPMessage: ngap.NGAPMessage(f.Raw), AMFSetID: 9,
		AllowedNSSAI:          ngap.AllowedNSSAI{{SNSSAI: slice.SNSSAI}},
		PartiallyAllowedNSSAI: ngap.PartiallyAllowedNSSAI{{SNSSAI: slice.SNSSAI}},
	})

	expectReset(t, x.gnb, connection(nil, 5))
	x.amf2.Quiet(t, 200*time.Millisecond)

	if ues := x.h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs = %+v", ues)
	}
}
