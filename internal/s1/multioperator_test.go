package s1_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func servedFor(t *testing.T, plmn string, group uint16, codes ...byte) s1ap.ServedGUMMEIs {
	t.Helper()

	s := served(t, group, codes...)
	s[0].ServedPLMNs = []s1ap.PLMNIdentity{plmnID(t, plmn[:3], plmn[3:])}

	return s
}

func answerS1Setup(t *testing.T, mme *peer, wantTAs s1ap.SupportedTAs, resp *s1ap.S1SetupResponse) {
	t.Helper()

	req := expect(t, mme, "initiating", s1ap.ProcS1Setup, s1ap.ParseS1SetupRequest)
	assertTAs(t, req.SupportedTAs, wantTAs)
	mme.send(t, resp)
}

func answerENBConfigurationUpdate(t *testing.T, mme *peer, wantTAs s1ap.SupportedTAs) {
	t.Helper()

	upd := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, upd.SupportedTAs, wantTAs)
	mme.send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})
}

func expectSkipping(t *testing.T, p *peer, skip s1ap.ProcedureCode) frame {
	t.Helper()

	for {
		f := p.nextFrame(t)
		if m, ok := f.pdu.(*s1ap.InitiatingMessage); ok && m.ProcedureCode == skip {
			continue
		}

		return f
	}
}

type twoOperators struct {
	h          *harness
	mmeA, mmeB *peer
	enbA       *peer
	enbB       *peer
	enbAB      *peer
}

func startTwoOperators(t *testing.T, ipA, ipB string) twoOperators {
	t.Helper()

	la := listenMME(t, ipA)
	lb := listenMMEAt(t, ipB, la.port())

	h := startGateway(t, la.port(), operatorA(ipA), operatorB(ipB))
	x := twoOperators{h: h, mmeA: la.accept(t), mmeB: lb.accept(t)}

	x.enbA = h.connectENB(t)
	x.enbA.send(t, setupRequest(t, 1, "enbA", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	answerS1Setup(t, x.mmeA, s1ap.SupportedTAs{ta(t, 1, "00101")}, &s1ap.S1SetupResponse{
		ServedGUMMEIs: servedFor(t, "00101", 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, x.enbA, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	x.enbB = h.connectENB(t)
	x.enbB.send(t, setupRequest(t, 2, "enbB", s1ap.PagingDRXv128, ta(t, 2, "002002")))
	answerS1Setup(t, x.mmeB, s1ap.SupportedTAs{ta(t, 2, "002002")}, &s1ap.S1SetupResponse{
		ServedGUMMEIs: servedFor(t, "002002", 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, x.enbB, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
	expect(t, x.enbA, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
	x.enbA.send(t, &s1ap.MMEConfigurationUpdateAcknowledge{})

	x.enbAB = h.connectENB(t)
	x.enbAB.send(t, setupRequest(t, 3, "enbAB", s1ap.PagingDRXv128, ta(t, 1, "00101", "002002")))
	expect(t, x.enbAB, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
	answerENBConfigurationUpdate(t, x.mmeB, s1ap.SupportedTAs{ta(t, 1, "002002"), ta(t, 2, "002002")})

	return x
}

func initialUEIn(t *testing.T, enbUEID s1ap.ENBUES1APID, plmn string, tac uint16) *s1ap.InitialUEMessage {
	t.Helper()

	msg := initialUE(t, enbUEID)
	msg.TAI = s1ap.TAI{PLMNIdentity: plmnID(t, plmn[:3], plmn[3:]), TAC: s1ap.TAC(tac)}

	return msg
}

func paging(t *testing.T, tais ...s1ap.TAI) *s1ap.Paging {
	t.Helper()

	return &s1ap.Paging{
		UEIdentityIndexValue: s1ap.Ptr(uint16(77)),
		STMSI:                &s1ap.STMSI{MMEC: 1, MTMSI: 0xc0ffee},
		CNDomain:             s1ap.Ptr(s1ap.CNDomainPS),
		TAIList:              tais,
	}
}

func tai(t *testing.T, plmn string, tac uint16) s1ap.TAI {
	t.Helper()

	return s1ap.TAI{PLMNIdentity: plmnID(t, plmn[:3], plmn[3:]), TAC: s1ap.TAC(tac)}
}

func expectSame(t *testing.T, p *peer, want []byte) {
	t.Helper()

	f := p.nextFrame(t)
	if !bytes.Equal(f.raw, want) || f.stream != 0 {
		t.Fatalf("got %s on stream %d, want the message unchanged on stream 0", procedureName(f.pdu), f.stream)
	}
}

func imsiPaging(t *testing.T, tais ...s1ap.TAI) []byte {
	t.Helper()

	b, err := paging(t, tais...).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := s1ap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	imsi := []byte{0x68, 0x00, 0x01, 0x10, 0x32, 0x54, 0x76, 0x98, 0xf0}
	if err := p.SetValue(p.Index(s1ap.IDUEPagingID), imsi); err != nil {
		t.Fatal(err)
	}

	out, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestPagingFanOut(t *testing.T) {
	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.40", "127.0.0.41")

	msg := paging(t, tai(t, "00101", 1), tai(t, "002002", 2), tai(t, "00101", 9))

	want, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	x.mmeA.send(t, msg)
	expectSame(t, x.enbA, want)
	expectSame(t, x.enbAB, want)

	msg = paging(t, tai(t, "002002", 1))
	if want, err = msg.Marshal(); err != nil {
		t.Fatal(err)
	}

	x.mmeB.send(t, msg)
	expectSame(t, x.enbAB, want)

	raw := imsiPaging(t, tai(t, "002002", 2))
	if _, err := x.mmeB.conn.WriteMsg(raw, &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(s1.PPID)}); err != nil {
		t.Fatal(err)
	}

	expectSame(t, x.enbB, raw)

	x.mmeA.send(t, paging(t, tai(t, "00101", 9), tai(t, "002002", 1)))

	for _, p := range []*peer{x.enbA, x.enbB, x.enbAB, x.mmeA, x.mmeB} {
		p.quiet(t, 200*time.Millisecond)
	}

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(x.h.gw.Collectors()...)

	const metrics = `
# HELP ellamocn_paging_messages_total PAGING messages received from MMEs, by interface (s1) and result (relayed: sent to the radios that serve a TAI of the List of TAIs in one of the operator's PLMNs; no_radio: no radio that is up serves such a TAI; invalid: undecodable).
# TYPE ellamocn_paging_messages_total counter
ellamocn_paging_messages_total{interface="s1",result="invalid"} 0
ellamocn_paging_messages_total{interface="s1",result="no_radio"} 1
ellamocn_paging_messages_total{interface="s1",result="relayed"} 3
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(metrics), "ellamocn_paging_messages_total"); err != nil {
		t.Fatal(err)
	}
}

func TestMultiOperatorSelectionAndScoping(t *testing.T) {
	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.42", "127.0.0.43")

	init := initialUEIn(t, 1, "002002", 1)
	init.STMSI = &s1ap.STMSI{MMEC: 1, MTMSI: 0x1234}
	x.enbAB.send(t, init)
	_, raw := expectRaw(t, x.mmeB, "initiating", s1ap.ProcInitialUEMessage)
	_, onB := ids(t, raw)

	init = initialUEIn(t, 2, "00101", 1)
	init.STMSI = &s1ap.STMSI{MMEC: 1, MTMSI: 0x1234}
	x.enbAB.send(t, init)
	_, raw = expectRaw(t, x.mmeA, "initiating", s1ap.ProcInitialUEMessage)
	_, onA := ids(t, raw)

	x.mmeB.send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 500, ENBUES1APID: onB, NASPDU: nas})
	_, raw = expectRaw(t, x.enbAB, "initiating", s1ap.ProcDownlinkNASTransport)
	ranB, _ := ids(t, raw)

	x.mmeA.send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 500, ENBUES1APID: onA, NASPDU: nas})
	_, raw = expectRaw(t, x.enbAB, "initiating", s1ap.ProcDownlinkNASTransport)
	ranA, _ := ids(t, raw)

	ues := x.h.gw.UEs()
	if len(ues) != 2 || ues[0].OperatorName != "B" || ues[1].OperatorName != "A" {
		t.Fatalf("UEs = %+v", ues)
	}

	x.mmeB.send(t, &s1ap.ErrorIndication{
		MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(500)), ENBUES1APID: &onA,
		Cause: &s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnspecified},
	})
	x.enbAB.quiet(t, 200*time.Millisecond)

	cause := &s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscOMIntervention}
	x.mmeB.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{All: true}})
	expect(t, x.mmeB, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)
	expectReset(t, x.enbAB, item(ranB, 1))
	x.mmeA.quiet(t, 200*time.Millisecond)

	if ues := x.h.gw.UEs(); len(ues) != 1 || ues[0].OperatorName != "A" || ues[0].RadioMMEUES1APID != uint32(ranA) {
		t.Fatalf("UEs after a full Reset from operator B = %+v", ues)
	}

	x.mmeA.send(t, &s1ap.MMEConfigurationUpdate{ServedGUMMEIs: servedFor(t, "00101", 1, 1, 2)})
	expect(t, x.mmeA, "successful", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdateAcknowledge)

	for _, enb := range []*peer{x.enbA, x.enbB, x.enbAB} {
		upd := expect(t, enb, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
		want := s1ap.ServedGUMMEIs{{
			ServedPLMNs:    []s1ap.PLMNIdentity{plmnID(t, "001", "01"), plmnID(t, "002", "002")},
			ServedGroupIDs: []s1ap.MMEGroupID{{0, 1}},
			ServedMMECs:    []s1ap.MMECode{1, 2},
		}}

		if !reflect.DeepEqual(upd.ServedGUMMEIs, want) {
			t.Fatalf("merged Served GUMMEIs = %+v, want %+v", upd.ServedGUMMEIs, want)
		}

		enb.send(t, &s1ap.MMEConfigurationUpdateAcknowledge{})
	}

	x.enbAB.send(t, &s1ap.ENBConfigurationUpdate{SupportedTAs: s1ap.SupportedTAs{ta(t, 1, "002002", "00101"), ta(t, 3, "002002")}})
	expect(t, x.enbAB, "successful", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdateAcknowledge)
	answerENBConfigurationUpdate(t, x.mmeB, s1ap.SupportedTAs{ta(t, 1, "002002"), ta(t, 2, "002002"), ta(t, 3, "002002")})
	x.mmeA.quiet(t, 200*time.Millisecond)
}

func gummei(t *testing.T, code byte) s1ap.GUMMEI {
	t.Helper()

	return s1ap.GUMMEI{
		PLMNIdentity: plmnID(t, "001", "01"), MMEGroupID: s1ap.MMEGroupID{0, 1},
		MMECode: s1ap.MMECode(code),
	}
}

func TestOverloadRelayedToTheOperatorsRadios(t *testing.T) {
	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.44", "127.0.0.45")
	status := func() s1.MMEStatus {
		st, _ := x.h.gw.MMEStatus(1, []string{"127.0.0.44"})
		return st
	}

	x.mmeA.send(t, &s1ap.OverloadStart{
		OverloadResponse:               s1ap.OverloadResponse{OverloadAction: s1ap.OverloadActionRejectRRCCRSignalling},
		TrafficLoadReductionIndication: s1ap.Ptr(uint8(50)),
	})

	own := []s1ap.GUMMEI{gummei(t, 1)}

	for _, enb := range []*peer{x.enbA, x.enbAB} {
		start := expect(t, enb, "initiating", s1ap.ProcOverloadStart, s1ap.ParseOverloadStart)
		if start.OverloadResponse.OverloadAction != s1ap.OverloadActionRejectRRCCRSignalling ||
			!reflect.DeepEqual(start.GUMMEIList, own) || start.TrafficLoadReductionIndication == nil ||
			*start.TrafficLoadReductionIndication != 50 {
			t.Fatalf("Overload Start = %+v", start)
		}
	}

	x.enbB.quiet(t, 200*time.Millisecond)

	want := []s1.Overload{{
		GUMMEI: s1.GUMMEI{PLMN: plmnA, MMEGroupID: 1, MMECode: 1}, Action: s1.OverloadRejectRRCCRSignalling,
		TrafficLoadReductionIndication: s1ap.Ptr(uint8(50)),
	}}
	if got := status().Overload; !reflect.DeepEqual(got, want) {
		t.Fatalf("overload = %+v, want %+v", got, want)
	}

	x.mmeA.send(t, &s1ap.OverloadStop{GUMMEIList: []s1ap.GUMMEI{gummei(t, 9)}})
	x.enbA.quiet(t, 200*time.Millisecond)

	x.mmeA.send(t, &s1ap.OverloadStop{})

	for _, enb := range []*peer{x.enbA, x.enbAB} {
		stop := expect(t, enb, "initiating", s1ap.ProcOverloadStop, s1ap.ParseOverloadStop)
		if !reflect.DeepEqual(stop.GUMMEIList, own) {
			t.Fatalf("Overload Stop = %+v", stop)
		}
	}

	if got := status().Overload; len(got) != 0 {
		t.Fatalf("overload after Overload Stop = %+v", got)
	}

	other := []s1ap.GUMMEI{gummei(t, 7), gummei(t, 8)}
	x.mmeA.send(t, &s1ap.OverloadStart{
		OverloadResponse: s1ap.OverloadResponse{OverloadAction: s1ap.OverloadActionRejectNonEmergencyMODT},
		GUMMEIList:       other,
	})

	for _, enb := range []*peer{x.enbA, x.enbAB} {
		start := expect(t, enb, "initiating", s1ap.ProcOverloadStart, s1ap.ParseOverloadStart)
		if !reflect.DeepEqual(start.GUMMEIList, other) || start.TrafficLoadReductionIndication != nil {
			t.Fatalf("Overload Start = %+v", start)
		}
	}

	x.mmeA.send(t, &s1ap.OverloadStart{
		OverloadResponse: s1ap.OverloadResponse{OverloadAction: s1ap.OverloadActionRejectRRCCRSignalling},
		GUMMEIList:       other[1:],
	})

	for _, enb := range []*peer{x.enbA, x.enbAB} {
		expect(t, enb, "initiating", s1ap.ProcOverloadStart, s1ap.ParseOverloadStart)
	}

	if got := status().Overload; len(got) != 2 || got[0].Action != s1.OverloadRejectNonEmergencyMODT ||
		got[1].Action != s1.OverloadRejectRRCCRSignalling {
		t.Fatalf("overload = %+v", got)
	}

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(x.h.gw.Collectors()...)

	const metrics = `
# HELP ellamocn_core_overloaded Core (MME) associations of the configured MMEs with an ongoing overload action (TS 36.413 clause 8.7.6), by interface (s1) and operator.
# TYPE ellamocn_core_overloaded gauge
ellamocn_core_overloaded{interface="s1",operator="A"} 1
ellamocn_core_overloaded{interface="s1",operator="B"} 0
# HELP ellamocn_overload_messages_total OVERLOAD START and OVERLOAD STOP messages received from MMEs, by interface (s1), procedure (overload_start, overload_stop) and result (relayed: sent to the radios that broadcast one of the operator's PLMNs, including the replay of an ongoing OVERLOAD START to a radio after its S1 Setup; no_radio: no such radio is up; ignored: OVERLOAD STOP for GUMMEIs with no ongoing overload action; invalid: undecodable or with an invalid GUMMEI).
# TYPE ellamocn_overload_messages_total counter
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="invalid"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="no_radio"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="relayed"} 3
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="ignored"} 1
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="invalid"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="no_radio"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="relayed"} 1
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(metrics),
		"ellamocn_core_overloaded", "ellamocn_overload_messages_total"); err != nil {
		t.Fatal(err)
	}

	_ = x.mmeA.conn.Abort()

	for _, enb := range []*peer{x.enbA, x.enbAB} {
		f := expectSkipping(t, enb, s1ap.ProcMMEConfigurationUpdate)

		m, ok := f.pdu.(*s1ap.InitiatingMessage)
		if !ok || m.ProcedureCode != s1ap.ProcOverloadStop {
			t.Fatalf("got %s, want Overload Stop", procedureName(f.pdu))
		}

		stop, err := s1ap.ParseOverloadStop(m.Value)
		if err != nil || !reflect.DeepEqual(stop.GUMMEIList, other) {
			t.Fatalf("Overload Stop after the MME association was lost = %+v, %v", stop, err)
		}
	}

	x.h.eventually(t, "overload state cleared", func() bool { return len(status().Overload) == 0 })

	expect(t, x.enbB, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
	x.enbB.quiet(t, 200*time.Millisecond)
}

func TestMMEGUMMEIsForOverload(t *testing.T) {
	got := s1.MMEGUMMEIs([]s1.ServedGUMMEIs{
		{PLMNs: []db.PLMN{plmnA, plmnB}, GroupIDs: []uint16{1, 2}, MMECodes: []uint8{3}},
		{PLMNs: []db.PLMN{plmnA}, GroupIDs: []uint16{9}, MMECodes: []uint8{9}},
	})

	want := []s1.GUMMEI{
		{PLMN: plmnA, MMEGroupID: 1, MMECode: 3},
		{PLMN: plmnA, MMEGroupID: 2, MMECode: 3},
		{PLMN: plmnB, MMEGroupID: 1, MMECode: 3},
		{PLMN: plmnB, MMEGroupID: 2, MMECode: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GUMMEIs = %+v, want %+v", got, want)
	}

	codes := make([]uint8, 200)
	for i := range codes {
		codes[i] = uint8(i)
	}

	if got := s1.MMEGUMMEIs([]s1.ServedGUMMEIs{{PLMNs: []db.PLMN{plmnA, plmnB}, GroupIDs: []uint16{1}, MMECodes: codes}}); len(got) != 256 {
		t.Fatalf("%d GUMMEIs, want 256", len(got))
	}
}

func overloadA(t *testing.T, x twoOperators) {
	t.Helper()

	x.mmeA.send(t, &s1ap.OverloadStart{
		OverloadResponse:               s1ap.OverloadResponse{OverloadAction: s1ap.OverloadActionRejectRRCCRSignalling},
		TrafficLoadReductionIndication: s1ap.Ptr(uint8(50)),
	})

	for _, enb := range []*peer{x.enbA, x.enbAB} {
		expect(t, enb, "initiating", s1ap.ProcOverloadStart, s1ap.ParseOverloadStart)
	}
}

func setUpRadio(t *testing.T, enb *peer, id uint32, name string, tas ...s1ap.SupportedTAItem) {
	t.Helper()

	enb.send(t, setupRequest(t, id, name, s1ap.PagingDRXv128, tas...))
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
}

func expectOverloadStartA(t *testing.T, enb *peer) {
	t.Helper()

	start := expect(t, enb, "initiating", s1ap.ProcOverloadStart, s1ap.ParseOverloadStart)
	if start.OverloadResponse.OverloadAction != s1ap.OverloadActionRejectRRCCRSignalling ||
		!reflect.DeepEqual(start.GUMMEIList, []s1ap.GUMMEI{gummei(t, 1)}) ||
		start.TrafficLoadReductionIndication == nil || *start.TrafficLoadReductionIndication != 50 {
		t.Fatalf("Overload Start = %+v", start)
	}
}

func overloadRelayedCount(t *testing.T, x twoOperators) float64 {
	t.Helper()

	return testutil.ToFloat64(x.h.gw.OverloadCounter().WithLabelValues("s1", "overload_start", "relayed"))
}

func TestOverloadSentToRadioSettingUpDuringOverload(t *testing.T) {
	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.46", "127.0.0.47")
	overloadA(t, x)

	enb := x.h.connectENB(t)
	setUpRadio(t, enb, 4, "enbC", ta(t, 1, "00101"))
	expectOverloadStartA(t, enb)
	enb.quiet(t, 200*time.Millisecond)

	if got := overloadRelayedCount(t, x); got != 2 {
		t.Fatalf("relayed Overload Start = %v, want 2", got)
	}
}

func TestNoOverloadSentToRadioSettingUpWithoutOverload(t *testing.T) {
	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.48", "127.0.0.49")

	enb := x.h.connectENB(t)
	setUpRadio(t, enb, 4, "enbC", ta(t, 1, "00101", "002002"))
	enb.quiet(t, 200*time.Millisecond)

	if got := overloadRelayedCount(t, x); got != 0 {
		t.Fatalf("relayed Overload Start = %v, want 0", got)
	}
}

func TestNoOverloadSentToRadioOfAnotherOperator(t *testing.T) {
	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.50", "127.0.0.51")
	overloadA(t, x)

	enb := x.h.connectENB(t)
	setUpRadio(t, enb, 4, "enbC", ta(t, 2, "002002"))
	enb.quiet(t, 200*time.Millisecond)

	if got := overloadRelayedCount(t, x); got != 1 {
		t.Fatalf("relayed Overload Start = %v, want 1", got)
	}
}

func TestOverloadSentAgainOnRepeatedS1Setup(t *testing.T) {
	skipWithoutSCTP(t)

	x := startTwoOperators(t, "127.0.0.52", "127.0.0.53")
	overloadA(t, x)

	setUpRadio(t, x.enbA, 1, "enbA", ta(t, 1, "00101"))
	expectOverloadStartA(t, x.enbA)
	x.enbA.quiet(t, 200*time.Millisecond)

	setUpRadio(t, x.enbA, 1, "enbA", ta(t, 1, "00101"))
	expectOverloadStartA(t, x.enbA)
	x.enbA.quiet(t, 200*time.Millisecond)

	if got := overloadRelayedCount(t, x); got != 3 {
		t.Fatalf("relayed Overload Start = %v, want 3", got)
	}
}
