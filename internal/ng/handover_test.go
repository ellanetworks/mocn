package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
)

func gnbID(t *testing.T, id uint32) ngap.GlobalRANNodeID {
	t.Helper()

	return gnbNodeID(plmnID(t, "001", "01"), id, 24)
}

func sourceToTarget(t *testing.T, nci ngap.NRCellIdentity) ngap.SourceToTargetTransparentContainer {
	t.Helper()

	c, err := (&ngap.SourceNGRANNodeToTargetNGRANNodeTransparentContainer{
		RRCContainer: ngap.RRCContainer{0x80},
		TargetCellID: ngap.NGRANCGI{NRCGI: &ngap.NRCGI{PLMNIdentity: plmnID(t, "001", "01"), NRCellIdentity: nci}},
		UEHistoryInformation: ngap.UEHistoryInformation{{LastVisitedCellInformation: ngap.LastVisitedCellInformation{
			GERANCell: new(ngap.LastVisitedGERANCellInformation{0x01}),
		}}},
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func handoverRequest(t *testing.T, amfID ngap.AMFUENGAPID, target *ngap.GlobalRANNodeID) *ngap.HandoverRequest {
	t.Helper()

	return handoverRequestTo(t, amfID, target, ngap.SourceToTargetTransparentContainer{0xaa, 0xbb})
}

func handoverRequestTo(t *testing.T, amfID ngap.AMFUENGAPID, target *ngap.GlobalRANNodeID,
	container ngap.SourceToTargetTransparentContainer,
) *ngap.HandoverRequest {
	t.Helper()

	return &ngap.HandoverRequest{
		AMFUENGAPID: amfID, HandoverType: ngap.HandoverTypeIntra5GS,
		Cause:                     new(ngap.CauseRadioNetworkHandoverDesirableForRadio.Cause()),
		UEAggregateMaximumBitRate: ngap.UEAggregateMaximumBitRate{DL: 1000000000, UL: 1000000000},
		UESecurityCapabilities:    ngap.UESecurityCapabilities{NREncryptionAlgorithms: ngap.SecurityAlgorithmsFrom(0x8000), NRIntegrityProtectionAlgorithms: ngap.SecurityAlgorithmsFrom(0x8000)},
		SecurityContext:           ngap.SecurityContext{NextHopChainingCount: 1, NextHopNH: securityKey()},
		PDUSessionResourceSetupListHOReq: ngap.PDUSessionResourceSetupListHOReq{{
			PDUSessionID: 5, SNSSAI: slice.SNSSAI, Transfer: ngap.TransferContainer{0x00},
		}},
		AllowedNSSAI:                       ngap.AllowedNSSAI{{SNSSAI: slice.SNSSAI}},
		SourceToTargetTransparentContainer: container,
		GUAMI:                              guami(t, "00101", 2, 1, 0),
		TargetRANNodeID:                    target,
	}
}

func pathSwitchRequest(t *testing.T, sourceAMFID ngap.AMFUENGAPID, g *ngap.GUAMI) *ngap.PathSwitchRequest {
	t.Helper()

	return &ngap.PathSwitchRequest{
		RANUENGAPID: 9, SourceAMFUENGAPID: sourceAMFID, UserLocationInformation: new(uli(t, "00101", 0x2ab01)),
		UESecurityCapabilities: &ngap.UESecurityCapabilities{NREncryptionAlgorithms: ngap.SecurityAlgorithmsFrom(0xe000), NRIntegrityProtectionAlgorithms: ngap.SecurityAlgorithmsFrom(0xe000)},
		PDUSessionResourceToBeSwitchedDLList: ngap.PDUSessionResourceToBeSwitchedDLList{
			{PDUSessionID: 1, Transfer: ngap.TransferContainer{0x00}},
		},
		SourceAMFGUAMI: g,
	}
}

func withAMFUENGAPID2Before(t *testing.T, got frame, want ngap.AMFUENGAPID, next ngap.ProtocolIEID) frame {
	t.Helper()

	p, err := ngap.ParseRawPDU(got.Raw)
	if err != nil {
		t.Fatal(err)
	}

	i := index(t, p, ngap.IDAMFUENGAPID2)
	if i < 0 || i != index(t, p, next)-1 {
		t.Fatalf("AMF UE NGAP ID 2 at %d, want right before %s", i, next)
	}

	var id ngap.AMFUENGAPID
	if ok, err := p.Get(ngap.IDAMFUENGAPID2, &id); err != nil || !ok || id != want {
		t.Fatalf("AMF UE NGAP ID 2 = %d (%v, %v), want %d", id, ok, err, want)
	}

	if err := p.Remove(i); err != nil {
		t.Fatal(err)
	}

	if got.Raw, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	return got
}

func secondRadio(t *testing.T, h *harness) *peer {
	t.Helper()

	gnb := h.connectGNB(t)
	gnb.Send(t, setupRequest(t, 2, "gnb2", ngap.PagingDRXv128, ta(t, 1, "00101")))
	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	return gnb
}

func TestHandoverBetweenRadiosOfTheGateway(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.93")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.93"))
	gnb1, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb1, amf, 7, 1<<39+5)
	gnb2 := secondRadio(t, h)

	required := &ngap.HandoverRequired{
		AMFUENGAPID: ue.ranAMFID, RANUENGAPID: ue.ranID, HandoverType: ngap.HandoverTypeIntra5GS,
		Cause: new(ngap.CauseRadioNetworkHandoverDesirableForRadio.Cause()),
		TargetID: ngap.TargetID{TargetRANNodeID: &ngap.TargetRANNodeID{
			GlobalRANNodeID: gnbID(t, 2), SelectedTAI: ngap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
		}},
		PDUSessionResourceListHORqd:        ngap.PDUSessionResourceListHORqd{{PDUSessionID: 5, Transfer: ngap.TransferContainer{0x00}}},
		SourceToTargetTransparentContainer: ngap.SourceToTargetTransparentContainer{0xaa, 0xbb},
	}
	gnb1.Send(t, required)

	f, raw := expectRaw(t, amf, "initiating", ngap.ProcHandoverPreparation)
	assertRelayed(t, required, f)

	if amfID, ranID := ueIDs(t, raw); amfID != ue.amfID || ranID != ue.amfRANID {
		t.Fatalf("Handover Required IDs = %d/%d", amfID, ranID)
	}

	target := gnbID(t, 2)
	req := handoverRequest(t, 900, &target)
	amf.Send(t, req)

	f, raw = expectRaw(t, gnb2, "initiating", ngap.ProcHandoverResourceAllocation)
	assertRelayed(t, req, withAMFUENGAPID2Before(t, f, 900, ngap.IDTargetRANNodeID))

	targetAMFID, _ := ueIDs(t, raw)
	if targetAMFID == ue.ranAMFID {
		t.Fatalf("AMF UE NGAP ID toward the target = %d, the source's", targetAMFID)
	}

	if ues := h.gw.UEs(); len(ues) != 2 || ues[1].RadioGlobalGNBID.ID != 2 || ues[1].RadioRANUENGAPID != nil {
		t.Fatalf("UEs = %+v", ues)
	}

	ack := &ngap.HandoverRequestAcknowledge{
		AMFUENGAPID: &targetAMFID, RANUENGAPID: new(ngap.RANUENGAPID(21)),
		PDUSessionResourceAdmittedList:     ngap.PDUSessionResourceAdmittedList{{PDUSessionID: 5, Transfer: ngap.TransferContainer{0x00}}},
		TargetToSourceTransparentContainer: ngap.TargetToSourceTransparentContainer{0xcc},
	}
	gnb2.Send(t, ack)

	f, raw = expectRaw(t, amf, "successful", ngap.ProcHandoverResourceAllocation)
	assertRelayed(t, ack, f)

	amfID, targetRANID := ueIDs(t, raw)
	if amfID != 900 || targetRANID == ue.amfRANID {
		t.Fatalf("Handover Request Acknowledge IDs = %d/%d", amfID, targetRANID)
	}

	command := &ngap.HandoverCommand{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID, HandoverType: ngap.HandoverTypeIntra5GS,
		PDUSessionResourceHandoverList:     ngap.PDUSessionResourceHandoverList{{PDUSessionID: 5, Transfer: ngap.TransferContainer{0x00}}},
		TargetToSourceTransparentContainer: ngap.TargetToSourceTransparentContainer{0xcc},
	}
	amf.Send(t, command)

	f, _ = expectRaw(t, gnb1, "successful", ngap.ProcHandoverPreparation)
	assertRelayed(t, command, f)

	status := &ngap.UplinkRANStatusTransfer{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: ue.ranID, Container: ngap.StatusTransferContainer{0x00}}
	gnb1.Send(t, status)

	f, _ = expectRaw(t, amf, "initiating", ngap.ProcUplinkRANStatusTransfer)
	assertRelayed(t, status, f)

	down := &ngap.DownlinkRANStatusTransfer{AMFUENGAPID: 900, RANUENGAPID: targetRANID, Container: ngap.StatusTransferContainer{0x00}}
	amf.Send(t, down)

	f, raw = expectRaw(t, gnb2, "initiating", ngap.ProcDownlinkRANStatusTransfer)
	assertRelayed(t, down, f)

	if amfID, ranID := ueIDs(t, raw); amfID != targetAMFID || ranID != 21 {
		t.Fatalf("Downlink RAN Status Transfer IDs = %d/%d", amfID, ranID)
	}

	notify := &ngap.HandoverNotify{AMFUENGAPID: targetAMFID, RANUENGAPID: 21, UserLocationInformation: new(uli(t, "00101", 0x2ab01))}
	gnb2.Send(t, notify)

	f, _ = expectRaw(t, amf, "initiating", ngap.ProcHandoverNotification)
	assertRelayed(t, notify, f)

	amf.Send(t, &ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{UENGAPIDPair: &ngap.UENGAPIDPair{AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID}},
		Cause:     new(ngap.CauseRadioNetworkSuccessfulHandover.Cause()),
	})
	expectRaw(t, gnb1, "initiating", ngap.ProcUEContextRelease)

	gnb1.Send(t, &ngap.UEContextReleaseComplete{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID})
	expectRaw(t, amf, "successful", ngap.ProcUEContextRelease)

	h.eventually(t, "the source connection released", func() bool {
		ues := h.gw.UEs()

		return len(ues) == 1 && ues[0].RadioRANUENGAPID != nil && *ues[0].RadioRANUENGAPID == 21 &&
			ues[0].NRCGI != nil && ues[0].NRCGI.CellID == 0x2ab01
	})
}

func TestHandoverFailureFromTheTargetRadio(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.94")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.94"))
	gnb, amf := upAndRunning(t, h, amfs)

	target := gnbID(t, 1)
	amf.Send(t, handoverRequest(t, 900, &target))

	_, raw := expectRaw(t, gnb, "initiating", ngap.ProcHandoverResourceAllocation)
	targetAMFID, _ := ueIDs(t, raw)

	fail := &ngap.HandoverFailure{
		AMFUENGAPID: &targetAMFID,
		Cause:       new(ngap.CauseRadioNetworkHOFailureInTarget.Cause()),
	}
	gnb.Send(t, fail)

	f, raw := expectRaw(t, amf, "unsuccessful", ngap.ProcHandoverResourceAllocation)
	assertRelayed(t, fail, f)

	if amfID, _ := ueIDs(t, raw); amfID != 900 {
		t.Fatalf("Handover Failure AMF UE NGAP ID = %d, want 900", amfID)
	}

	h.eventually(t, "the target connection released", func() bool { return len(h.gw.UEs()) == 0 })
	gnb.Quiet(t, 200*time.Millisecond)
}

func TestHandoverRequestWithoutATargetRadioRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.95")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.95"))
	gnb, amf := upAndRunning(t, h, amfs)

	unknown := gnbID(t, 9)

	for _, target := range []*ngap.GlobalRANNodeID{nil, &unknown} {
		amf.Send(t, handoverRequest(t, 11, target))

		fail := expect(t, amf, "unsuccessful", ngap.ProcHandoverResourceAllocation, ngap.ParseHandoverFailure)
		if fail.AMFUENGAPID == nil || *fail.AMFUENGAPID != 11 || fail.Cause == nil ||
			!fail.Cause.Equal(ngap.CauseRadioNetworkUnknownTargetID.Cause()) {
			t.Fatalf("Handover Failure = %+v", fail)
		}
	}

	gnb.Quiet(t, 200*time.Millisecond)

	if len(h.gw.UEs()) != 0 {
		t.Fatal("UE context kept for a rejected HANDOVER REQUEST")
	}
}

func pathSwitchBetweenRadios(t *testing.T, ip string, newAMFID ngap.AMFUENGAPID) {
	t.Helper()

	amfs := listenAMF(t, ip)
	h := startGateway(t, amfs.Port(), operatorA(ip))
	gnb1, amf := upAndRunning(t, h, amfs)
	ue := attach(t, gnb1, amf, 7, 1<<39+5)
	gnb2 := secondRadio(t, h)

	source := guami(t, "00101", 2, 1, 0)
	gnb2.Send(t, pathSwitchRequest(t, ue.amfID, &source))

	_, raw := expectRaw(t, amf, "initiating", ngap.ProcPathSwitchRequest)

	_, amfRANID := ueIDs(t, raw)
	if amfRANID == ue.amfRANID {
		t.Fatalf("RAN UE NGAP ID toward the AMF = %d, the old connection's", amfRANID)
	}

	if id, _, _ := raw.SourceAMFUENGAPID(); id != ue.amfID {
		t.Fatalf("Source AMF UE NGAP ID = %d, want %d", id, ue.amfID)
	}

	ack := &ngap.PathSwitchRequestAcknowledge{
		AMFUENGAPID: &newAMFID, RANUENGAPID: &amfRANID,
		SecurityContext:                ngap.SecurityContext{NextHopChainingCount: 2, NextHopNH: securityKey()},
		PDUSessionResourceSwitchedList: ngap.PDUSessionResourceSwitchedList{{PDUSessionID: 1, Transfer: ngap.TransferContainer{0x00}}},
		AllowedNSSAI:                   ngap.AllowedNSSAI{{SNSSAI: slice.SNSSAI}},
	}
	amf.Send(t, ack)

	f, raw := expectRaw(t, gnb2, "successful", ngap.ProcPathSwitchRequest)
	assertRelayed(t, ack, withAMFUENGAPID2(t, f, newAMFID))

	ranAMFID, ranID := ueIDs(t, raw)
	if ranAMFID == ue.ranAMFID || ranID != 9 {
		t.Fatalf("Path Switch Request Acknowledge IDs = %d/%d", ranAMFID, ranID)
	}

	release := expect(t, gnb1, "initiating", ngap.ProcUEContextRelease, ngap.ParseUEContextReleaseCommand)
	if ids := release.UENGAPIDs.UENGAPIDPair; ids == nil || ids.AMFUENGAPID != ue.ranAMFID || ids.RANUENGAPID != ue.ranID ||
		release.Cause == nil || !release.Cause.Equal(ngap.CauseRadioNetworkSuccessfulHandover.Cause()) {
		t.Fatalf("UE Context Release Command to the source = %+v", release)
	}

	if ues := h.gw.UEs(); len(ues) != 1 || ues[0].RadioGlobalGNBID.ID != 2 || *ues[0].RadioRANUENGAPID != 9 {
		t.Fatalf("UEs = %+v", ues)
	}

	gnb1.Send(t, &ngap.UEContextReleaseRequest{
		AMFUENGAPID: ue.ranAMFID, RANUENGAPID: ue.ranID,
		Cause: new(ngap.CauseRadioNetworkSuccessfulHandover.Cause()),
	})
	gnb1.Send(t, &ngap.UEContextReleaseComplete{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID})

	amf.Quiet(t, 200*time.Millisecond)

	amf.Send(t, &ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{AMFUENGAPID: new(newAMFID)},
		Cause:     new(ngap.CauseNASNormalRelease.Cause()),
	})

	_, raw = expectRaw(t, gnb2, "initiating", ngap.ProcUEContextRelease)

	got, _, err := raw.UENGAPIDs()
	if err != nil || got.AMFUENGAPID == nil || *got.AMFUENGAPID != ranAMFID {
		t.Fatalf("UE Context Release Command IDs = %+v, %v", got, err)
	}

	gnb1.Quiet(t, 0)
}

func TestPathSwitchBetweenRadiosOfTheGateway(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	pathSwitchBetweenRadios(t, "127.0.1.96", 1<<39+5)
}

func TestPathSwitchWithANewAMFUENGAPIDReleasesTheOldConnection(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	pathSwitchBetweenRadios(t, "127.0.1.103", 77)
}

func TestPathSwitchRequestFailureReleasesTheNewConnection(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.97")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.97"))
	gnb, amf := upAndRunning(t, h, amfs)

	source := guami(t, "00101", 2, 1, 0)
	gnb.Send(t, pathSwitchRequest(t, 4000, &source))

	_, raw := expectRaw(t, amf, "initiating", ngap.ProcPathSwitchRequest)
	_, amfRANID := ueIDs(t, raw)

	transfer, err := (&ngap.PathSwitchRequestUnsuccessfulTransfer{
		Cause: ngap.CauseRadioNetworkUnspecified.Cause(),
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	fail := &ngap.PathSwitchRequestFailure{
		AMFUENGAPID: new(ngap.AMFUENGAPID(4000)), RANUENGAPID: &amfRANID,
		PDUSessionResourceReleased: ngap.PDUSessionResourceReleasedListPSFail{{PDUSessionID: 1, Transfer: transfer}},
	}
	amf.Send(t, fail)

	f, raw := expectRaw(t, gnb, "unsuccessful", ngap.ProcPathSwitchRequest)
	assertRelayed(t, fail, f)

	if _, ranID := ueIDs(t, raw); ranID != 9 {
		t.Fatalf("RAN UE NGAP ID = %d, want 9", ranID)
	}

	h.eventually(t, "the new connection released", func() bool { return len(h.gw.UEs()) == 0 })
}

func expectPathSwitchRejected(t *testing.T, gnb *peer, want ngap.Cause) {
	t.Helper()

	psf := expect(t, gnb, "unsuccessful", ngap.ProcPathSwitchRequest, ngap.ParsePathSwitchRequestFailure)
	if psf.AMFUENGAPID == nil || *psf.AMFUENGAPID != 33 || psf.RANUENGAPID == nil || *psf.RANUENGAPID != 9 ||
		len(psf.PDUSessionResourceReleased) != 1 {
		t.Fatalf("Path Switch Request Failure = %+v", psf)
	}

	transfer, err := ngap.ParsePathSwitchRequestUnsuccessfulTransfer(psf.PDUSessionResourceReleased[0].Transfer)
	if err != nil || !transfer.Cause.Equal(want) {
		t.Fatalf("Path Switch Request Unsuccessful Transfer = %+v, %v", transfer, err)
	}
}

func TestPathSwitchRequestWithoutASourceAMFGUAMIToTheOnlyAMFSet(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.99")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.99"))
	gnb, amf := upAndRunning(t, h, amfs)

	req := pathSwitchRequest(t, 4000, nil)
	gnb.Send(t, req)

	f, raw := expectRaw(t, amf, "initiating", ngap.ProcPathSwitchRequest)
	assertRelayed(t, req, f)

	_, amfRANID := ueIDs(t, raw)

	ack := &ngap.PathSwitchRequestAcknowledge{
		AMFUENGAPID: new(ngap.AMFUENGAPID(4000)), RANUENGAPID: &amfRANID,
		SecurityContext:                ngap.SecurityContext{NextHopChainingCount: 2, NextHopNH: securityKey()},
		PDUSessionResourceSwitchedList: ngap.PDUSessionResourceSwitchedList{{PDUSessionID: 1, Transfer: ngap.TransferContainer{0x00}}},
		AllowedNSSAI:                   ngap.AllowedNSSAI{{SNSSAI: slice.SNSSAI}},
	}
	amf.Send(t, ack)

	f, _ = expectRaw(t, gnb, "successful", ngap.ProcPathSwitchRequest)
	assertRelayed(t, ack, withAMFUENGAPID2(t, f, 4000))

	if ues := h.gw.UEs(); len(ues) != 1 || ues[0].OperatorName != "A" || ues[0].PLMN != plmnA {
		t.Fatalf("UEs = %+v", ues)
	}
}

func TestPathSwitchRequestWithoutAServingAMFRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.98")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.98"))
	gnb, amf := upAndRunning(t, h, amfs)

	otherSet := guami(t, "00101", 2, 9, 0)
	gnb.Send(t, pathSwitchRequest(t, 33, &otherSet))
	expectPathSwitchRejected(t, gnb, ngap.CauseTransportResourceUnavailable.Cause())

	req := &ngap.PathSwitchRequest{
		RANUENGAPID: 9, SourceAMFUENGAPID: 33, UserLocationInformation: new(uli(t, "00202", 0x2ab01)),
		UESecurityCapabilities: &ngap.UESecurityCapabilities{NREncryptionAlgorithms: ngap.SecurityAlgorithmsFrom(0xe000), NRIntegrityProtectionAlgorithms: ngap.SecurityAlgorithmsFrom(0xe000)},
		PDUSessionResourceToBeSwitchedDLList: ngap.PDUSessionResourceToBeSwitchedDLList{
			{PDUSessionID: 1, Transfer: ngap.TransferContainer{0x00}},
		},
	}
	gnb.Send(t, req)
	expectPathSwitchRejected(t, gnb, ngap.CauseMiscUnknownPLMNOrSNPN.Cause())

	amf.Quiet(t, 200*time.Millisecond)

	if len(h.gw.UEs()) != 0 {
		t.Fatal("UE context kept for a rejected PATH SWITCH REQUEST")
	}
}

func TestPathSwitchRequestWithoutASourceAMFGUAMIAndTwoAMFSetsRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs1 := listenAMF(t, "127.0.1.100")
	amfs2 := listenAMFAt(t, "127.0.1.101", amfs1.Port())
	h := startGateway(t, amfs1.Port(), operatorA("127.0.1.100", "127.0.1.101"))

	gnb := h.connectGNB(t)
	gnb.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	amf1, _ := setupAMF(t, amfs1, setupResponse(t, "amf1", 50, "00101", guami(t, "00101", 2, 1, 1)))
	amf2, _ := setupAMF(t, amfs2, setupResponse(t, "amf2", 50, "00101", guami(t, "00101", 2, 2, 1)))

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	settle(t, h, gnb, "127.0.1.100", "127.0.1.101")

	gnb.Send(t, pathSwitchRequest(t, 33, nil))
	expectPathSwitchRejected(t, gnb, ngap.CauseTransportResourceUnavailable.Cause())

	amf1.Quiet(t, 200*time.Millisecond)
	amf2.Quiet(t, 0)
}

func TestHandoverRequestRoutedByTheTargetCell(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.102")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.102"))
	gnb1, amf := upAndRunning(t, h, amfs)
	gnb2 := secondRadio(t, h)

	req := handoverRequestTo(t, 900, nil, sourceToTarget(t, 2<<12|0x5))
	amf.Send(t, req)

	f, _ := expectRaw(t, gnb2, "initiating", ngap.ProcHandoverResourceAllocation)
	assertRelayed(t, req, withAMFUENGAPID2(t, f, 900))

	for _, nci := range []ngap.NRCellIdentity{9 << 12, 0x5} {
		amf.Send(t, handoverRequestTo(t, 901, nil, sourceToTarget(t, nci)))

		fail := expect(t, amf, "unsuccessful", ngap.ProcHandoverResourceAllocation, ngap.ParseHandoverFailure)
		if fail.Cause == nil || !fail.Cause.Equal(ngap.CauseRadioNetworkUnknownTargetID.Cause()) {
			t.Fatalf("Handover Failure = %+v", fail)
		}
	}

	gnb1.Quiet(t, 200*time.Millisecond)
}
