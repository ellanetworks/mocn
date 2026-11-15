package s1_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
)

func pathSwitchRequest(t *testing.T, enbID s1ap.ENBUES1APID, sourceMMEID s1ap.MMEUES1APID, g *s1ap.GUMMEI) *s1ap.PathSwitchRequest {
	t.Helper()

	return &s1ap.PathSwitchRequest{
		ENBUES1APID: enbID, SourceMMEUES1APID: sourceMMEID,
		ERABToBeSwitchedDL: []s1ap.ERABToBeSwitchedDLItem{{
			ERABID: 5, TransportLayerAddress: tlaOf("10.0.0.2"), GTPTEID: 7,
		}},
		EUTRANCGI:              &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 0x2ab01},
		TAI:                    &s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
		UESecurityCapabilities: &s1ap.UESecurityCapabilities{EncryptionAlgorithms: s1ap.SecurityAlgorithms{Value: 0x8000}, IntegrityProtectionAlgorithms: s1ap.SecurityAlgorithms{Value: 0xc000}},
		SourceMMEGUMMEI:        g,
	}
}

func bodyOfRawPDU(t *testing.T, p *s1ap.RawPDU) []byte {
	t.Helper()

	b, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func withMMEUES1APID2(t *testing.T, got frame, want s1ap.MMEUES1APID) frame {
	t.Helper()

	p, err := s1ap.ParseRawPDU(got.Raw)
	if err != nil {
		t.Fatal(err)
	}

	i := indexOf(t, p, s1ap.IDMMEUES1APID2)
	if i != indexOf(t, p, s1ap.IDSecurityContext)+1 {
		t.Fatalf("MME UE S1AP ID 2 at %d, want right after the Security Context", i)
	}

	var id s1ap.MMEUES1APID
	if ok, err := p.Get(s1ap.IDMMEUES1APID2, &id); err != nil || !ok || id != want {
		t.Fatalf("MME UE S1AP ID 2 = %d (%v, %v), want %d", id, ok, err, want)
	}

	if ie, _ := p.IE(i); ie.Criticality != s1ap.CriticalityIgnore {
		t.Fatalf("MME UE S1AP ID 2 criticality %s, want ignore", ie.Criticality)
	}

	if err := p.Remove(i); err != nil {
		t.Fatal(err)
	}

	got.Raw = bodyOfRawPDU(t, p)

	return got
}

func TestHandoverOutOfTheGateway(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.78")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.78"))
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 3, 300)

	required := &s1ap.HandoverRequired{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID, HandoverType: s1ap.HandoverTypeIntraLTE,
		Cause: s1ap.Ptr(s1ap.CauseRadioNetworkHandoverDesirableForRadio.Cause()),
		TargetID: s1ap.TargetID{TargeteNBID: &s1ap.TargeteNBID{
			GlobalENBID: s1ap.GlobalENBID{PLMNIdentity: plmnID(t, "001", "01"), ENBID: s1ap.ENBID{MacroENBID: s1ap.Ptr(s1ap.MacroENBID(0x77))}},
			SelectedTAI: s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 9},
		}},
		SourceToTarget: s1ap.TransparentContainer{0x01, 0x02},
	}
	enb.Send(t, required)

	f, raw := expectRaw(t, mme, "initiating", s1ap.ProcHandoverPreparation)
	assertRelayed(t, required, f)

	if mmeID, enbID := ids(t, raw); mmeID != ue.mmeUEID || enbID != ue.mmeENBID {
		t.Fatalf("Handover Required IDs = %d/%d, want %d/%d", mmeID, enbID, ue.mmeUEID, ue.mmeENBID)
	}

	command := &s1ap.HandoverCommand{
		MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID, HandoverType: s1ap.HandoverTypeIntraLTE,
		TargetToSource: s1ap.TransparentContainer{0x03},
	}
	mme.Send(t, command)

	f, raw = expectRaw(t, enb, "successful", s1ap.ProcHandoverPreparation)
	assertRelayed(t, command, f)

	if mmeID, enbID := ids(t, raw); mmeID != ue.ranMMEID || enbID != ue.enbUEID {
		t.Fatalf("Handover Command IDs = %d/%d, want %d/%d", mmeID, enbID, ue.ranMMEID, ue.enbUEID)
	}

	status := &s1ap.ENBStatusTransfer{MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID, Container: s1ap.StatusTransferContainer{0x00, 0x00}}
	enb.Send(t, status)

	f, _ = expectRaw(t, mme, "initiating", s1ap.ProcENBStatusTransfer)
	assertRelayed(t, status, f)

	release := &s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{UES1APIDPair: &s1ap.UES1APIDPair{MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID}},
		Cause:     s1ap.Ptr(s1ap.CauseRadioNetworkSuccessfulHandover.Cause()),
	}
	mme.Send(t, release)
	expectRaw(t, enb, "initiating", s1ap.ProcUEContextRelease)

	enb.Send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: &ue.ranMMEID, ENBUES1APID: &ue.enbUEID})
	expectRaw(t, mme, "successful", s1ap.ProcUEContextRelease)

	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestHandoverCancelled(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.79")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.79"))
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 3, 300)

	cancel := &s1ap.HandoverCancel{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID,
		Cause: s1ap.Ptr(s1ap.CauseRadioNetworkHandoverCancelled.Cause()),
	}
	enb.Send(t, cancel)

	f, _ := expectRaw(t, mme, "initiating", s1ap.ProcHandoverCancel)
	assertRelayed(t, cancel, f)

	ack := &s1ap.HandoverCancelAcknowledge{MMEUES1APID: &ue.mmeUEID, ENBUES1APID: &ue.mmeENBID}
	mme.Send(t, ack)

	f, _ = expectRaw(t, enb, "successful", s1ap.ProcHandoverCancel)
	assertRelayed(t, ack, f)

	if len(h.gw.UEs()) != 1 {
		t.Fatal("UE context released on a cancelled handover")
	}
}

func handoverRequest(t *testing.T, mmeID s1ap.MMEUES1APID, cellID uint32) *s1ap.HandoverRequest {
	t.Helper()

	container, err := (&s1ap.SourceeNBToTargeteNBTransparentContainer{
		RRCContainer:         s1ap.RRCContainer{0x80},
		TargetCellID:         s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: s1ap.CellIdentity(cellID)},
		UEHistoryInformation: s1ap.UEHistoryInformation{{UTRANCell: new(s1ap.LastVisitedUTRANCellInformation{0x01})}},
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return &s1ap.HandoverRequest{
		MMEUES1APID: mmeID, HandoverType: s1ap.HandoverTypeIntraLTE,
		Cause:  s1ap.Ptr(s1ap.CauseRadioNetworkHandoverDesirableForRadio.Cause()),
		UEAMBR: s1ap.UEAggregateMaximumBitRate{DL: 1000000000, UL: 500000000},
		ERABToBeSetup: []s1ap.ERABToBeSetupItemHOReq{{
			ERABID: 5, TransportLayerAddress: tlaOf("10.45.0.1"), GTPTEID: 0x1234,
			QoS: s1ap.ERABLevelQoSParameters{QCI: 9, ARP: s1ap.AllocationAndRetentionPriority{
				PriorityLevel: 15, PreemptionCapability: s1ap.PreemptionShallNotTrigger,
				PreemptionVulnerability: s1ap.PreemptionNotPreemptable,
			}},
		}},
		SourceToTarget:         container,
		UESecurityCapabilities: s1ap.UESecurityCapabilities{EncryptionAlgorithms: s1ap.SecurityAlgorithms{Value: 0x8000}, IntegrityProtectionAlgorithms: s1ap.SecurityAlgorithms{Value: 0xc000}},
		SecurityContext:        s1ap.SecurityContext{NextHopChainingCount: 1, NextHopParameter: securityKey()},
	}
}

func TestHandoverBetweenRadiosOfTheGateway(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.76")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.76"))
	enb1, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb1, mme, 3, 300)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	req := handoverRequest(t, 900, 2<<8|0x01)
	mme.Send(t, req)

	f, raw := expectRaw(t, enb2, "initiating", s1ap.ProcHandoverResourceAllocation)
	assertRelayed(t, req, servingMME(t, f, 1, 1, 900))

	targetMMEID, _, err := raw.MMEUES1APID()
	if err != nil || targetMMEID == ue.ranMMEID {
		t.Fatalf("MME UE S1AP ID toward the target = %d, %v", targetMMEID, err)
	}

	if ues := h.gw.UEs(); len(ues) != 2 || ues[1].RadioGlobalENBID.ID != 2 || ues[1].RadioENBUES1APID != nil {
		t.Fatalf("UEs = %+v", ues)
	}

	ack := &s1ap.HandoverRequestAcknowledge{
		MMEUES1APID: &targetMMEID, ENBUES1APID: new(s1ap.ENBUES1APID(21)),
		ERABAdmitted:   []s1ap.ERABAdmittedItem{{ERABID: 5, TransportLayerAddress: tlaOf("10.0.0.2"), GTPTEID: 9}},
		TargetToSource: s1ap.TransparentContainer{0xcc},
	}
	enb2.Send(t, ack)

	f, raw = expectRaw(t, mme, "successful", s1ap.ProcHandoverResourceAllocation)
	assertRelayed(t, ack, f)

	mmeID, targetENBID := ids(t, raw)
	if mmeID != 900 || targetENBID == ue.mmeENBID {
		t.Fatalf("Handover Request Acknowledge IDs = %d/%d", mmeID, targetENBID)
	}

	notify := &s1ap.HandoverNotify{
		MMEUES1APID: targetMMEID, ENBUES1APID: 21,
		EUTRANCGI: &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 2<<8 | 0x01},
		TAI:       &s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
	}
	enb2.Send(t, notify)

	f, raw = expectRaw(t, mme, "initiating", s1ap.ProcHandoverNotification)
	assertRelayed(t, notify, f)

	if mmeID, enbID := ids(t, raw); mmeID != 900 || enbID != targetENBID {
		t.Fatalf("Handover Notify IDs = %d/%d", mmeID, enbID)
	}

	h.eventually(t, "the target connection known", func() bool {
		ues := h.gw.UEs()
		return len(ues) == 2 && ues[1].RadioENBUES1APID != nil && *ues[1].RadioENBUES1APID == 21
	})
}

func TestHandoverFailureFromTheTargetRadio(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.83")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.83"))
	enb, mme := upAndRunning(t, h, mmeListener)

	mme.Send(t, handoverRequest(t, 900, 1<<8|0x07))

	_, raw := expectRaw(t, enb, "initiating", s1ap.ProcHandoverResourceAllocation)

	targetMMEID, _, err := raw.MMEUES1APID()
	if err != nil {
		t.Fatal(err)
	}

	fail := &s1ap.HandoverFailure{
		MMEUES1APID: &targetMMEID,
		Cause:       s1ap.Ptr(s1ap.CauseRadioNetworkNoRadioResourcesInTargetCell.Cause()),
	}
	enb.Send(t, fail)

	f, raw := expectRaw(t, mme, "unsuccessful", s1ap.ProcHandoverResourceAllocation)
	assertRelayed(t, fail, f)

	if mmeID, _, _ := raw.MMEUES1APID(); mmeID != 900 {
		t.Fatalf("Handover Failure MME UE S1AP ID = %d, want 900", mmeID)
	}

	h.eventually(t, "the target connection released", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestHandoverRequestWithoutATargetRadioRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.84")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.84"))
	enb, mme := upAndRunning(t, h, mmeListener)

	for _, msg := range []interface{ Marshal() ([]byte, error) }{
		handoverRequest(t, 11, 9<<8|0x01),
		initiating(s1ap.ProcHandoverResourceAllocation, s1ap.CriticalityReject, bodyOf(t, uplinkNAS(t, 11, 7))),
	} {
		mme.Send(t, msg)

		fail := expect(t, mme, "unsuccessful", s1ap.ProcHandoverResourceAllocation, s1ap.ParseHandoverFailure)
		if fail.MMEUES1APID == nil || *fail.MMEUES1APID != 11 || fail.Cause == nil ||
			!fail.Cause.Equal(s1ap.CauseRadioNetworkUnknownTargetID.Cause()) {
			t.Fatalf("Handover Failure = %+v", fail)
		}
	}

	enb.Quiet(t, 200*time.Millisecond)

	if len(h.gw.UEs()) != 0 {
		t.Fatal("UE context kept for a rejected HANDOVER REQUEST")
	}
}

func TestPathSwitchBetweenRadiosOfTheGateway(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.80")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.80"))
	enb1, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb1, mme, 3, 300)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	enb2.Send(t, pathSwitchRequest(t, 9, ue.mmeUEID, new(gummei(t, 1))))

	_, raw := expectRaw(t, mme, "initiating", s1ap.ProcPathSwitchRequest)

	_, mmeENBID := ids(t, raw)
	if mmeENBID == ue.mmeENBID {
		t.Fatalf("eNB UE S1AP ID toward the MME = %d, the old connection's", mmeENBID)
	}

	if source, _, _ := raw.SourceMMEUES1APID(); source != ue.mmeUEID {
		t.Fatalf("Source MME UE S1AP ID = %d, want %d", source, ue.mmeUEID)
	}

	ack := &s1ap.PathSwitchRequestAcknowledge{
		MMEUES1APID: &ue.mmeUEID, ENBUES1APID: &mmeENBID,
		SecurityContext: s1ap.SecurityContext{NextHopChainingCount: 2, NextHopParameter: securityKey()},
	}
	mme.Send(t, ack)

	f, raw := expectRaw(t, enb2, "successful", s1ap.ProcPathSwitchRequest)
	f = withMMEUES1APID2(t, f, ue.mmeUEID)
	assertRelayed(t, ack, f)

	ranMMEID, enbID := ids(t, raw)
	if ranMMEID == ue.ranMMEID || enbID != 9 {
		t.Fatalf("Path Switch Request Acknowledge IDs = %d/%d", ranMMEID, enbID)
	}

	release := expect(t, enb1, "initiating", s1ap.ProcUEContextRelease, s1ap.ParseUEContextReleaseCommand)
	if ids := release.UES1APIDs; ids.UES1APIDPair == nil || ids.UES1APIDPair.MMEUES1APID != ue.ranMMEID || ids.UES1APIDPair.ENBUES1APID != ue.enbUEID ||
		release.Cause == nil || !release.Cause.Equal(s1ap.CauseRadioNetworkSuccessfulHandover.Cause()) {
		t.Fatalf("UE Context Release Command to the source = %+v", release)
	}

	if ues := h.gw.UEs(); len(ues) != 1 || *ues[0].RadioENBUES1APID != 9 || ues[0].RadioGlobalENBID.ID != 2 {
		t.Fatalf("UEs = %+v", ues)
	}

	enb1.Send(t, &s1ap.UEContextReleaseRequest{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: ue.enbUEID,
		Cause:                      s1ap.Ptr(s1ap.CauseRadioNetworkSuccessfulHandover.Cause()),
		GWContextReleaseIndication: new(s1ap.GWContextReleaseIndicationTrue),
	})
	enb1.Send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: &ue.ranMMEID, ENBUES1APID: &ue.enbUEID})

	mme.Quiet(t, 200*time.Millisecond)

	mme.Send(t, &s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{MMEUES1APID: s1ap.Ptr(ue.mmeUEID)},
		Cause:     s1ap.Ptr(s1ap.CauseNASNormalRelease.Cause()),
	})

	_, raw = expectRaw(t, enb2, "initiating", s1ap.ProcUEContextRelease)

	got, _, err := raw.UES1APIDs()
	if err != nil || mmeIDOf(got) != ranMMEID {
		t.Fatalf("UE Context Release Command IDs = %+v, %v", got, err)
	}

	enb1.Quiet(t, 0)
}

func TestPathSwitchRequestFailureReleasesTheNewConnection(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.81")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.81"))
	enb, mme := upAndRunning(t, h, mmeListener)

	enb.Send(t, pathSwitchRequest(t, 9, 4000, new(gummei(t, 1))))

	_, raw := expectRaw(t, mme, "initiating", s1ap.ProcPathSwitchRequest)
	_, mmeENBID := ids(t, raw)

	fail := &s1ap.PathSwitchRequestFailure{
		MMEUES1APID: new(s1ap.MMEUES1APID(4000)), ENBUES1APID: &mmeENBID,
		Cause: s1ap.Ptr(s1ap.CauseRadioNetworkUnknownMMEUES1APID.Cause()),
	}
	mme.Send(t, fail)

	f, raw := expectRaw(t, enb, "unsuccessful", s1ap.ProcPathSwitchRequest)
	assertRelayed(t, fail, f)

	if _, enbID := ids(t, raw); enbID != 9 {
		t.Fatalf("eNB UE S1AP ID = %d, want 9", enbID)
	}

	h.eventually(t, "the new connection released", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestPathSwitchRequestWithoutAServingMMERejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.82")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.82"))
	enb, mme := upAndRunning(t, h, mmeListener)

	for _, g := range []*s1ap.GUMMEI{nil, new(gummei(t, 9))} {
		enb.Send(t, pathSwitchRequest(t, 9, 33, g))

		psf := expect(t, enb, "unsuccessful", s1ap.ProcPathSwitchRequest, s1ap.ParsePathSwitchRequestFailure)
		if psf.MMEUES1APID == nil || *psf.MMEUES1APID != 33 || psf.ENBUES1APID == nil || *psf.ENBUES1APID != 9 ||
			psf.Cause == nil || !psf.Cause.Equal(s1ap.CauseTransportResourceUnavailable.Cause()) {
			t.Fatalf("Path Switch Request Failure = %+v", psf)
		}
	}

	mme.Quiet(t, 200*time.Millisecond)

	if len(h.gw.UEs()) != 0 {
		t.Fatal("UE context kept for a rejected PATH SWITCH REQUEST")
	}
}
