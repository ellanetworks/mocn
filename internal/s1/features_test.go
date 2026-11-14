package s1_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/s1"
)

func twoMMEs(t *testing.T, ip1, ip2 string) (*harness, *peer, *peer, *peer) {
	t.Helper()

	first := listenMME(t, ip1)
	second := listenMMEAt(t, ip2, first.Port())

	h := startGatewayWith(t, s1.Config{CorePort: first.Port(), IntN: func(int) int { return 0 }}, operatorA(ip1, ip2))

	mme1, mme2 := first.Accept(t), second.Accept(t)
	enb := h.connectENB(t)
	enb.Send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	for i, m := range []*peer{mme1, mme2} {
		setUpMME(t, m, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
			ServedGUMMEIs: served(t, uint16(i+1), byte(i+1)), RelativeMMECapacity: s1ap.Ptr(uint8(10)),
		})
	}

	resp := expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
	if len(resp.ServedGUMMEIs[0].ServedGroupIDs) < 2 {
		expect(t, enb, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
		enb.Send(t, &s1ap.MMEConfigurationUpdateAcknowledge{})
	}

	h.eventually(t, "both MMEs up", func() bool {
		a, _ := h.gw.MMEStatus(1, []string{ip1})
		b, _ := h.gw.MMEStatus(1, []string{ip2})

		return a.State == s1.MMEUp && b.State == s1.MMEUp
	})

	return h, enb, mme1, mme2
}

func TestRerouteNASRequest(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, enb, mme1, mme2 := twoMMEs(t, "127.0.0.70", "127.0.0.71")
	ue := attach(t, enb, mme1, 4, 500)

	enb.Send(t, initialUE(t, 5))

	f, raw := expectRaw(t, mme1, "initiating", s1ap.ProcInitialUEMessage)
	_, mmeENBID := ids(t, raw)

	mme1.Send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 501, ENBUES1APID: mmeENBID, NASPDU: nas})
	_, dl := expectRaw(t, enb, "initiating", s1ap.ProcDownlinkNASTransport)
	ranMMEID, _ := ids(t, dl)

	mme1.Send(t, &s1ap.RerouteNASRequest{
		ENBUES1APID: mmeENBID, MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(501)), S1Message: s1ap.S1Message(f.Raw),
		MMEGroupID: s1ap.MMEGroupID{0, 2}, UEUsageType: s1ap.Ptr(uint8(5)),
	})

	_, rerouted := expectRaw(t, mme2, "initiating", s1ap.ProcInitialUEMessage)
	if _, e := ids(t, rerouted); e != mmeENBID {
		t.Fatalf("rerouted eNB UE S1AP ID = %d, want %d", e, mmeENBID)
	}

	var group s1ap.MMEGroupID
	if ok, err := rerouted.Get(s1ap.IDMMEGroupID, &group); err != nil || !ok || group != (s1ap.MMEGroupID{0, 2}) {
		t.Fatalf("MME Group ID = %v (%v, %v)", group, ok, err)
	}

	if i, j := rerouted.Index(s1ap.IDMMEGroupID), rerouted.Index(s1ap.IDUEUsageType); i < rerouted.Index(s1ap.IDRRCEstablishmentCause) ||
		j != i+1 {
		t.Fatalf("MME Group ID at %d and UE Usage Type at %d are out of order", i, j)
	}

	if _, err := s1ap.ParseInitialUEMessage(bodyOfRaw(t, rerouted)); err != nil {
		t.Fatalf("rerouted INITIAL UE MESSAGE: %v", err)
	}

	enb.Send(t, uplinkNAS(t, ranMMEID, 5))
	mme1.Quiet(t, 200*time.Millisecond)
	mme2.Quiet(t, 0)

	mme2.Send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 900, ENBUES1APID: mmeENBID, NASPDU: nas})

	_, dl = expectRaw(t, enb, "initiating", s1ap.ProcDownlinkNASTransport)
	if m, e := ids(t, dl); m != ranMMEID || e != 5 {
		t.Fatalf("Downlink NAS Transport IDs after reroute = %d/%d, want %d/5", m, e, ranMMEID)
	}

	enb.Send(t, uplinkNAS(t, ranMMEID, 5))
	_, ul := expectRaw(t, mme2, "initiating", s1ap.ProcUplinkNASTransport)

	if m, e := ids(t, ul); m != 900 || e != mmeENBID {
		t.Fatalf("Uplink NAS Transport IDs after reroute = %d/%d", m, e)
	}

	mme1.Send(t, &s1ap.RerouteNASRequest{
		ENBUES1APID: ue.mmeENBID, S1Message: s1ap.S1Message(f.Raw), MMEGroupID: s1ap.MMEGroupID{0, 9},
	})

	_, back := expectRaw(t, mme1, "initiating", s1ap.ProcInitialUEMessage)
	if _, e := ids(t, back); e != ue.mmeENBID {
		t.Fatalf("rerouted eNB UE S1AP ID = %d, want %d", e, ue.mmeENBID)
	}

	if ok, err := back.Get(s1ap.IDMMEGroupID, &group); err != nil || !ok || group != (s1ap.MMEGroupID{0, 9}) {
		t.Fatalf("MME Group ID = %v (%v, %v)", group, ok, err)
	}

	mme2.Quiet(t, 200*time.Millisecond)
	enb.Quiet(t, 0)

	mme1.Send(t, &s1ap.RerouteNASRequest{
		ENBUES1APID: ue.mmeENBID, S1Message: s1ap.S1Message(nas), MMEGroupID: s1ap.MMEGroupID{0, 2},
	})
	expectReset(t, enb, s1ap.UEAssociatedLogicalS1ConnectionItem{ENBUES1APID: s1ap.Ptr(ue.enbUEID)})
	mme2.Quiet(t, 200*time.Millisecond)

	ues := h.gw.UEs()
	if len(ues) != 1 || ues[0].MMETNLAddresses[0] != "127.0.0.71" {
		t.Fatalf("UEs = %+v", ues)
	}
}

func bodyOfRaw(t *testing.T, p *s1ap.RawPDU) []byte {
	t.Helper()

	b, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	pdu, err := s1ap.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}

	return pdu.(*s1ap.InitiatingMessage).Value
}

func TestErrorIndicationFromRadioForAReroutedUE(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, enb, mme1, mme2 := twoMMEs(t, "127.0.0.72", "127.0.0.73")

	enb.Send(t, initialUE(t, 5))

	f, raw := expectRaw(t, mme1, "initiating", s1ap.ProcInitialUEMessage)
	_, mmeENBID := ids(t, raw)

	mme1.Send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 501, ENBUES1APID: mmeENBID, NASPDU: nas})
	_, dl := expectRaw(t, enb, "initiating", s1ap.ProcDownlinkNASTransport)
	ranMMEID, _ := ids(t, dl)

	mme1.Send(t, &s1ap.RerouteNASRequest{ENBUES1APID: mmeENBID, S1Message: s1ap.S1Message(f.Raw), MMEGroupID: s1ap.MMEGroupID{0, 2}})
	expectRaw(t, mme2, "initiating", s1ap.ProcInitialUEMessage)

	enb.Send(t, &s1ap.ErrorIndication{
		MMEUES1APID: &ranMMEID, ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(5)), Cause: &s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownPairUES1APID},
	})

	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
	mme1.Quiet(t, 200*time.Millisecond)
	mme2.Quiet(t, 0)
}

func TestErrorIndicationWithOnlyTheMMEUEID(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.74")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.74"))
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 3, 77)

	other := s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscUnspecified}
	mme.Send(t, &s1ap.ErrorIndication{MMEUES1APID: &ue.mmeUEID, Cause: &other})
	enb.Quiet(t, 200*time.Millisecond)

	if len(h.gw.UEs()) != 1 {
		t.Fatal("UE released on an Error Indication without an AP ID cause")
	}

	cause := s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownENBUES1APID}
	mme.Send(t, &s1ap.ErrorIndication{MMEUES1APID: &ue.mmeUEID, Cause: &cause})

	expectReset(t, enb, item(ue.ranMMEID, ue.enbUEID))
	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
	mme.Quiet(t, 200*time.Millisecond)
}

func TestInitialContextSetupRequestKeepsTheGUMMEIOfTheMME(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.75")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.75"))
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	ics := contextSetup(ue, true)
	ics.GUMMEI = &s1ap.GUMMEI{PLMNIdentity: plmnID(t, "001", "01"), MMEGroupID: s1ap.MMEGroupID{0, 9}, MMECode: 9}
	ics.MMEUES1APID2 = s1ap.Ptr(s1ap.MMEUES1APID(4))
	mme.Send(t, ics)

	f, _ := expectRaw(t, enb, "initiating", s1ap.ProcInitialContextSetup)
	servingMME(t, f, 9, 9, 1000)

	pdu, err := s1ap.Unmarshal(f.Raw)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s1ap.ParseInitialContextSetupRequest(pdu.(*s1ap.InitiatingMessage).Value)
	if err != nil {
		t.Fatal(err)
	}

	if got.MMEUES1APID2 == nil || *got.MMEUES1APID2 != 1000 || got.GUMMEI == nil || *got.GUMMEI != *ics.GUMMEI {
		t.Fatalf("Initial Context Setup Request = %+v", got)
	}
}

func TestHandoverAndPathSwitchRequestsFail(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.76")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.76"))
	enb, mme := upAndRunning(t, h, mmeListener)

	reject := s1ap.CriticalityReject
	mme.Send(t, initiating(s1ap.ProcHandoverResourceAllocation, reject, bodyOf(t, uplinkNAS(t, 11, 7))))

	fail := expect(t, mme, "unsuccessful", s1ap.ProcHandoverResourceAllocation, s1ap.ParseHandoverFailure)
	if fail.MMEUES1APID == nil || *fail.MMEUES1APID != 11 || fail.Cause == nil ||
		fail.Cause.Group != s1ap.CauseGroupRadioNetwork || fail.Cause.Value != s1ap.CauseRadioNetworkHOFailureInTarget {
		t.Fatalf("Handover Failure = %+v", fail)
	}

	enb.Send(t, &s1ap.PathSwitchRequest{
		ENBUES1APID: 9, SourceMMEUES1APID: 33,
		ERABToBeSwitchedDL: []s1ap.ERABToBeSwitchedDLItem{{
			ERABID: 5, TransportLayerAddress: s1ap.TransportLayerAddress{10, 0, 0, 1}, GTPTEID: 1,
		}},
		EUTRANCGI:              &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 0x19b01},
		TAI:                    &s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
		UESecurityCapabilities: &s1ap.UESecurityCapabilities{EncryptionAlgorithms: 0x8000, IntegrityProtectionAlgorithms: 0xc000},
	})

	psf := expect(t, enb, "unsuccessful", s1ap.ProcPathSwitchRequest, s1ap.ParsePathSwitchRequestFailure)
	if psf.MMEUES1APID == nil || *psf.MMEUES1APID != 33 || psf.ENBUES1APID == nil || *psf.ENBUES1APID != 9 ||
		psf.Cause == nil || psf.Cause.Value != s1ap.CauseProtocolMessageNotCompatible {
		t.Fatalf("Path Switch Request Failure = %+v", psf)
	}

	mme.Quiet(t, 200*time.Millisecond)
}

func TestDuplicateGlobalENBIDRejected(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.77")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.77"))
	upAndRunning(t, h, mmeListener)

	enb2 := h.connectENBFrom(t, "127.0.0.86")
	enb2.Send(t, setupRequest(t, 1, "copy", s1ap.PagingDRXv128, ta(t, 2, "00101")))

	fail := expect(t, enb2, "unsuccessful", s1ap.ProcS1Setup, s1ap.ParseS1SetupFailure)
	if fail.Cause == nil || fail.Cause.Group != s1ap.CauseGroupProtocol || fail.Cause.Value != s1ap.CauseProtocolSemanticError {
		t.Fatalf("S1 Setup Failure = %+v, want semantic error", fail)
	}

	nodes := h.gw.RANNodes()
	if len(nodes) != 2 || nodes[0].State != s1.RANNodeUp || nodes[1].GlobalENBID != nil {
		t.Fatalf("RAN nodes = %+v", nodes)
	}

	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 2, "00101")))
}

func TestDuplicateGlobalENBIDFromTheSameAddressesReplacesTheOldAssociation(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.87")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.87"))
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 1, 100)

	rebooted := h.connectENB(t)
	setUpENB(t, rebooted, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	enb.Closed(t)

	r := expectReset(t, mme, item(100, ue.mmeENBID))
	if r.Cause == nil || r.Cause.Group != s1ap.CauseGroupTransport || r.Cause.Value != s1ap.CauseTransportResourceUnavailable {
		t.Fatalf("Reset cause = %v", r.Cause)
	}

	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })

	nodes := h.gw.RANNodes()
	if len(nodes) != 1 || nodes[0].State != s1.RANNodeUp || nodes[0].GlobalENBID == nil {
		t.Fatalf("RAN nodes = %+v", nodes)
	}
}

func TestOverloadReplayedWhenAnENBConfigurationUpdateAddsAPLMN(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.78", "127.0.0.79")
	overloadA(t, x)

	x.enbB.Send(t, &s1ap.ENBConfigurationUpdate{SupportedTAs: s1ap.SupportedTAs{ta(t, 2, "002002"), ta(t, 3, "00101")}})
	expect(t, x.enbB, "successful", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdateAcknowledge)
	expectOverloadStartA(t, x.enbB)

	x.enbB.Send(t, &s1ap.ENBConfigurationUpdate{ENBName: s1ap.Ptr("renamed")})
	expect(t, x.enbB, "successful", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdateAcknowledge)
	x.enbB.Quiet(t, 200*time.Millisecond)
}

func TestMMEConfigurationUpdateEndsTheOverloadOfGUMMEIsNoLongerServed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.80", "127.0.0.81")
	overloadA(t, x)

	x.mmeA.Send(t, &s1ap.MMEConfigurationUpdate{ServedGUMMEIs: servedFor(t, "00101", 3, 1)})
	expect(t, x.mmeA, "successful", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdateAcknowledge)

	for _, enb := range []*peer{x.enbA, x.enbAB} {
		f := expectSkipping(t, enb, s1ap.ProcMMEConfigurationUpdate)

		m, ok := f.PDU.(*s1ap.InitiatingMessage)
		if !ok || m.ProcedureCode != s1ap.ProcOverloadStop {
			t.Fatalf("got %s, want Overload Stop", procedureName(f.PDU))
		}

		stop, err := s1ap.ParseOverloadStop(m.Value)
		if err != nil || !reflect.DeepEqual(stop.GUMMEIList, []s1ap.GUMMEI{gummei(t, 1)}) {
			t.Fatalf("Overload Stop = %+v, %v", stop, err)
		}
	}

	if st, _ := x.h.gw.MMEStatus(1, []string{"127.0.0.80"}); len(st.Overload) != 0 {
		t.Fatalf("overload = %+v, want none", st.Overload)
	}
}
