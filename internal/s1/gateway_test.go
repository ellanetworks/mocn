package s1_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var (
	plmnA = db.PLMN{MCC: "001", MNC: "01"}
	plmnB = db.PLMN{MCC: "002", MNC: "002"}
)

func operatorA(mmes ...string) db.Operator {
	o := db.Operator{
		ID: 1, Name: "A", PLMNs: []db.PLMN{plmnA},
		GlobalENBID: &db.GlobalENBID{PLMN: plmnA, Type: db.ENBIDMacro, ID: 0xabcde},
	}

	for _, m := range mmes {
		o.MMEs = append(o.MMEs, db.CoreNode{TNLAddresses: []string{m}})
	}

	return o
}

func operatorB(mmes ...string) db.Operator {
	o := db.Operator{
		ID: 2, Name: "B", PLMNs: []db.PLMN{plmnB},
		GlobalENBID: &db.GlobalENBID{PLMN: plmnB, Type: db.ENBIDLongMacro, ID: 7},
	}

	for _, m := range mmes {
		o.MMEs = append(o.MMEs, db.CoreNode{TNLAddresses: []string{m}})
	}

	return o
}

func setupRequest(t *testing.T, enbID uint32, name string, drx s1ap.PagingDRX, tas ...s1ap.SupportedTAItem) *s1ap.S1SetupRequest {
	t.Helper()

	return &s1ap.S1SetupRequest{
		GlobalENBID: s1ap.GlobalENBID{
			PLMNIdentity: plmnID(t, "001", "01"), ENBID: s1ap.ENBID{Kind: s1ap.ENBIDMacro, Value: enbID},
		},
		ENBName: &name, SupportedTAs: tas, DefaultPagingDRX: &drx,
	}
}

func ta(t *testing.T, tac uint16, plmns ...string) s1ap.SupportedTAItem {
	t.Helper()

	item := s1ap.SupportedTAItem{TAC: s1ap.TAC(tac)}
	for _, p := range plmns {
		item.BroadcastPLMNs = append(item.BroadcastPLMNs, plmnID(t, p[:3], p[3:]))
	}

	return item
}

func served(t *testing.T, group uint16, codes ...byte) s1ap.ServedGUMMEIs {
	t.Helper()

	item := s1ap.ServedGUMMEIsItem{
		ServedPLMNs:    []s1ap.PLMNIdentity{plmnID(t, "001", "01")},
		ServedGroupIDs: []s1ap.MMEGroupID{{byte(group >> 8), byte(group)}},
	}

	for _, c := range codes {
		item.ServedMMECs = append(item.ServedMMECs, s1ap.MMECode(c))
	}

	return s1ap.ServedGUMMEIs{item}
}

func setUpMME(t *testing.T, mme *peer, wantTAs s1ap.SupportedTAs, wantDRX s1ap.PagingDRX, resp *s1ap.S1SetupResponse) {
	t.Helper()

	req := expect(t, mme, "initiating", s1ap.ProcS1Setup, s1ap.ParseS1SetupRequest)

	want := s1ap.GlobalENBID{
		PLMNIdentity: plmnID(t, "001", "01"), ENBID: s1ap.ENBID{Kind: s1ap.ENBIDMacro, Value: 0xabcde},
	}
	if req.GlobalENBID.PLMNIdentity != want.PLMNIdentity || req.GlobalENBID.ENBID != want.ENBID {
		t.Fatalf("Global eNB ID = %+v, want %+v", req.GlobalENBID, want)
	}

	assertTAs(t, req.SupportedTAs, wantTAs)

	if req.DefaultPagingDRX == nil || *req.DefaultPagingDRX != wantDRX {
		t.Fatalf("Default Paging DRX = %v, want %v", req.DefaultPagingDRX, wantDRX)
	}

	mme.send(t, resp)
}

func assertTAs(t *testing.T, got, want s1ap.SupportedTAs) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("Supported TAs = %+v, want %+v", got, want)
	}

	for i := range got {
		if got[i].TAC != want[i].TAC || !reflect.DeepEqual(got[i].BroadcastPLMNs, want[i].BroadcastPLMNs) {
			t.Fatalf("Supported TAs = %+v, want %+v", got, want)
		}
	}
}

func setUpENB(t *testing.T, enb *peer, req *s1ap.S1SetupRequest) {
	t.Helper()

	enb.send(t, req)
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
}

func TestS1SetupBothWays(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.2")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.2"))
	mme := mmeListener.accept(t)

	mme.quiet(t, 300*time.Millisecond)

	h.eventually(t, "MME connected", func() bool {
		st, ok := h.gw.MMEStatus(1, []string{"127.0.0.2"})
		return ok && st.State == s1.MMEConnected
	})

	enb := h.connectENB(t)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101", "00199")))

	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		MMEName: s1ap.Ptr("mme1"), ServedGUMMEIs: served(t, 0x8001, 1), RelativeMMECapacity: s1ap.Ptr(uint8(10)),
	})

	resp := expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
	if resp.RelativeMMECapacity == nil || *resp.RelativeMMECapacity != 255 {
		t.Fatalf("Relative MME Capacity = %v, want 255", resp.RelativeMMECapacity)
	}

	if !reflect.DeepEqual(resp.ServedGUMMEIs, served(t, 0x8001, 1)) {
		t.Fatalf("Served GUMMEIs = %+v", resp.ServedGUMMEIs)
	}

	st, _ := h.gw.MMEStatus(1, []string{"127.0.0.2"})
	if st.State != s1.MMEUp || st.MMEName != "mme1" || st.RelativeMMECapacity == nil || *st.RelativeMMECapacity != 10 ||
		st.PrimaryTNLAddress != "127.0.0.2" ||
		!reflect.DeepEqual(st.ServedGUMMEIs, []s1.ServedGUMMEIs{{PLMNs: []db.PLMN{plmnA}, GroupIDs: []uint16{0x8001}, MMECodes: []uint8{1}}}) {
		t.Fatalf("MME status = %+v", st)
	}

	nodes := h.gw.RANNodes()
	if len(nodes) != 1 || nodes[0].State != s1.RANNodeUp || nodes[0].Name != "enb1" || nodes[0].DefaultPagingDRX != 128 ||
		nodes[0].GlobalENBID == nil || *nodes[0].GlobalENBID != (db.GlobalENBID{PLMN: plmnA, Type: db.ENBIDMacro, ID: 1}) ||
		!reflect.DeepEqual(nodes[0].SupportedTAs, []s1.TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{plmnA, {MCC: "001", MNC: "99"}}}}) {
		t.Fatalf("RAN nodes = %+v", nodes)
	}

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv256, ta(t, 2, "00101")))

	upd := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, upd.SupportedTAs, s1ap.SupportedTAs{ta(t, 1, "00101"), ta(t, 2, "00101")})

	if upd.DefaultPagingDRX == nil || *upd.DefaultPagingDRX != s1ap.PagingDRXv256 {
		t.Fatalf("Default Paging DRX = %v, want v256", upd.DefaultPagingDRX)
	}

	mme.send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})

	enb2.send(t, &s1ap.ENBConfigurationUpdate{SupportedTAs: s1ap.SupportedTAs{ta(t, 3, "00101")}})
	expect(t, enb2, "successful", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdateAcknowledge)

	upd = expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, upd.SupportedTAs, s1ap.SupportedTAs{ta(t, 1, "00101"), ta(t, 3, "00101")})

	if upd.DefaultPagingDRX != nil {
		t.Fatalf("Default Paging DRX = %v, want absent", *upd.DefaultPagingDRX)
	}

	mme.send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})

	_ = enb2.conn.Abort()

	upd = expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, upd.SupportedTAs, s1ap.SupportedTAs{ta(t, 1, "00101")})

	if upd.DefaultPagingDRX == nil || *upd.DefaultPagingDRX != s1ap.PagingDRXv128 {
		t.Fatalf("Default Paging DRX = %v, want v128", upd.DefaultPagingDRX)
	}

	mme.send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})

	mme.send(t, &s1ap.MMEConfigurationUpdate{ServedGUMMEIs: served(t, 0x8001, 1, 2)})
	expect(t, mme, "successful", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdateAcknowledge)

	mcu := expect(t, enb, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
	if !reflect.DeepEqual(mcu.ServedGUMMEIs, served(t, 0x8001, 1, 2)) {
		t.Fatalf("Served GUMMEIs = %+v", mcu.ServedGUMMEIs)
	}

	enb.send(t, &s1ap.MMEConfigurationUpdateAcknowledge{})

	h.eventually(t, "one RAN node", func() bool { return len(h.gw.RANNodes()) == 1 })

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(h.gw.Collectors()...)

	want := `
# HELP ellamocn_core_associations Core (MME) associations of the configured MMEs, by interface (s1), operator and state (disconnected: no SCTP association; connected: SCTP association up, S1 Setup not done; up: S1 Setup succeeded; setup_failed: the MME rejected S1 Setup).
# TYPE ellamocn_core_associations gauge
ellamocn_core_associations{interface="s1",operator="A",state="connected"} 0
ellamocn_core_associations{interface="s1",operator="A",state="disconnected"} 0
ellamocn_core_associations{interface="s1",operator="A",state="setup_failed"} 0
ellamocn_core_associations{interface="s1",operator="A",state="up"} 1
# HELP ellamocn_core_overloaded Core (MME) associations of the configured MMEs with an ongoing overload action (TS 36.413 clause 8.7.6), by interface (s1) and operator.
# TYPE ellamocn_core_overloaded gauge
ellamocn_core_overloaded{interface="s1",operator="A"} 0
# HELP ellamocn_initial_ue_messages_total INITIAL UE MESSAGEs received from radios, by interface (s1) and result (relayed: sent to an MME; unknown_plmn: no operator serves the PLMN; no_mme: no selectable MME of the operator; rejected: reuses an allocated eNB UE S1AP ID, an Error Indication is sent; invalid: undecodable or missing a mandatory IE; error: internal failure or failed send to the MME).
# TYPE ellamocn_initial_ue_messages_total counter
ellamocn_initial_ue_messages_total{interface="s1",result="error"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="invalid"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="no_mme"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="rejected"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="relayed"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="unknown_plmn"} 0
# HELP ellamocn_overload_messages_total OVERLOAD START and OVERLOAD STOP messages received from MMEs, by interface (s1), procedure (overload_start, overload_stop) and result (relayed: sent to the radios that broadcast one of the operator's PLMNs, including the replay of an ongoing OVERLOAD START to a radio after its S1 Setup; no_radio: no such radio is up; ignored: OVERLOAD STOP for GUMMEIs with no ongoing overload action; invalid: undecodable or with an invalid GUMMEI).
# TYPE ellamocn_overload_messages_total counter
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="invalid"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="no_radio"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_start",result="relayed"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="ignored"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="invalid"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="no_radio"} 0
ellamocn_overload_messages_total{interface="s1",procedure="overload_stop",result="relayed"} 0
# HELP ellamocn_paging_messages_total PAGING messages received from MMEs, by interface (s1) and result (relayed: sent to the radios that serve a TAI of the List of TAIs in one of the operator's PLMNs; no_radio: no radio that is up serves such a TAI; invalid: undecodable).
# TYPE ellamocn_paging_messages_total counter
ellamocn_paging_messages_total{interface="s1",result="invalid"} 0
ellamocn_paging_messages_total{interface="s1",result="no_radio"} 0
ellamocn_paging_messages_total{interface="s1",result="relayed"} 0
# HELP ellamocn_radio_associations Radio (eNodeB) associations, by interface (s1) and state (connected: SCTP association up, no successful S1 Setup; up: S1 Setup succeeded).
# TYPE ellamocn_radio_associations gauge
ellamocn_radio_associations{interface="s1",state="connected"} 0
ellamocn_radio_associations{interface="s1",state="up"} 1
# HELP ellamocn_setup_attempts_total Setup procedures (S1 Setup), by interface (s1), side and result. side=radio: received from radios; result success, invalid (undecodable or invalid request), unknown_plmn (no operator serves a broadcast PLMN), no_mme (no MME of the serving operators is up), mme_failure (the serving operators' MMEs rejected S1 Setup), error (the response could not be built or sent, or the radio left before the answer). side=core: sent to MMEs; result success, failure (the MME rejected the request or answered with an invalid response), timeout (no answer from the MME), error (the request could not be built or sent, or the association dropped before the answer).
# TYPE ellamocn_setup_attempts_total counter
ellamocn_setup_attempts_total{interface="s1",result="error",side="core"} 0
ellamocn_setup_attempts_total{interface="s1",result="error",side="radio"} 0
ellamocn_setup_attempts_total{interface="s1",result="failure",side="core"} 0
ellamocn_setup_attempts_total{interface="s1",result="invalid",side="radio"} 0
ellamocn_setup_attempts_total{interface="s1",result="mme_failure",side="radio"} 0
ellamocn_setup_attempts_total{interface="s1",result="no_mme",side="radio"} 0
ellamocn_setup_attempts_total{interface="s1",result="success",side="core"} 1
ellamocn_setup_attempts_total{interface="s1",result="success",side="radio"} 2
ellamocn_setup_attempts_total{interface="s1",result="timeout",side="core"} 0
ellamocn_setup_attempts_total{interface="s1",result="unknown_plmn",side="radio"} 0
# HELP ellamocn_ue_connections UE-associated logical S1-connections (TS 36.413 clause 3.1) relayed, by interface (s1) and operator.
# TYPE ellamocn_ue_connections gauge
ellamocn_ue_connections{interface="s1",operator="A"} 0
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}

	for _, c := range h.gw.Collectors() {
		if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
			t.Fatalf("lint: %v %v", problems, err)
		}
	}
}

func TestS1SetupFailures(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.3")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.3"), operatorB("127.0.0.4"))
	mme := mmeListener.accept(t)

	enb := h.connectENB(t)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv64, ta(t, 1, "00301")))

	fail := expect(t, enb, "unsuccessful", s1ap.ProcS1Setup, s1ap.ParseS1SetupFailure)
	if fail.Cause == nil || fail.Cause.Group != s1ap.CauseGroupMisc || fail.Cause.Value != s1ap.CauseMiscUnknownPLMN {
		t.Fatalf("cause = %v, want unknown PLMN", fail.Cause)
	}

	mme.quiet(t, 200*time.Millisecond)

	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv64, ta(t, 1, "002002")))

	fail = expect(t, enb, "unsuccessful", s1ap.ProcS1Setup, s1ap.ParseS1SetupFailure)
	if fail.Cause == nil || fail.Cause.Group != s1ap.CauseGroupTransport ||
		fail.Cause.Value != s1ap.CauseTransportResourceUnavailable || fail.TimeToWait == nil {
		t.Fatalf("failure = %+v, want transport resource unavailable with a Time to Wait", fail)
	}

	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv64, ta(t, 1, "00101")))

	expect(t, mme, "initiating", s1ap.ProcS1Setup, s1ap.ParseS1SetupRequest)
	mme.send(t, &s1ap.S1SetupFailure{
		Cause:      &s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscUnknownPLMN},
		TimeToWait: s1ap.Ptr(s1ap.TimeToWaitV1s),
	})

	fail = expect(t, enb, "unsuccessful", s1ap.ProcS1Setup, s1ap.ParseS1SetupFailure)
	if fail.Cause == nil || fail.Cause.Value != s1ap.CauseMiscUnknownPLMN || fail.TimeToWait == nil ||
		*fail.TimeToWait != s1ap.TimeToWaitV1s {
		t.Fatalf("failure = %+v, want the MME's cause and Time to Wait", fail)
	}

	st, _ := h.gw.MMEStatus(1, []string{"127.0.0.3"})
	if st.State != s1.MMESetupFailed {
		t.Fatalf("MME status = %+v", st)
	}

	nodes := h.gw.RANNodes()
	if len(nodes) != 1 || nodes[0].State != s1.RANNodeConnected || nodes[0].GlobalENBID != nil {
		t.Fatalf("RAN nodes = %+v", nodes)
	}

	enb.send(t, &s1ap.ENBConfigurationUpdate{ENBName: s1ap.Ptr("x")})

	upd := expect(t, enb, "unsuccessful", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdateFailure)
	if upd.Cause == nil || upd.Cause.Group != s1ap.CauseGroupProtocol {
		t.Fatalf("cause = %v, want a protocol cause", upd.Cause)
	}

	time.Sleep(time.Second)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv64, ta(t, 1, "00101")))
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv64, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	enb.send(t, &s1ap.ENBConfigurationUpdate{SupportedTAs: s1ap.SupportedTAs{ta(t, 9, "00301")}})

	upd = expect(t, enb, "unsuccessful", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdateFailure)
	if upd.Cause == nil || upd.Cause.Value != s1ap.CauseMiscUnknownPLMN {
		t.Fatalf("cause = %v, want unknown PLMN", upd.Cause)
	}

	if nodes := h.gw.RANNodes(); nodes[0].SupportedTAs[0].TAC != 1 {
		t.Fatalf("rejected update changed the TAs: %+v", nodes[0].SupportedTAs)
	}

	mme.quiet(t, 200*time.Millisecond)

	enb.send(t, rawPDU{&s1ap.InitiatingMessage{
		ProcedureCode: s1ap.ProcS1Setup, Criticality: s1ap.CriticalityReject, Value: []byte{0, 0, 0},
	}})
	expect(t, enb, "unsuccessful", s1ap.ProcS1Setup, s1ap.ParseS1SetupFailure)

	assertSetupAttempts(t, h, map[string]float64{
		"radio/unknown_plmn": 1, "radio/no_mme": 1, "radio/mme_failure": 1, "radio/success": 1, "radio/invalid": 1,
		"core/failure": 1, "core/success": 1,
	})
}

func TestS1SetupTimeout(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.3")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.3"))
	mme := mmeListener.accept(t)

	enb := h.connectENB(t)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv64, ta(t, 1, "00101")))

	expect(t, mme, "initiating", s1ap.ProcS1Setup, s1ap.ParseS1SetupRequest)
	expect(t, enb, "unsuccessful", s1ap.ProcS1Setup, s1ap.ParseS1SetupFailure)

	h.eventually(t, "the MME association to drop", func() bool {
		st, _ := h.gw.MMEStatus(1, []string{"127.0.0.3"})
		return st.State == s1.MMEDisconnected
	})

	assertSetupAttempts(t, h, map[string]float64{"radio/no_mme": 1, "core/timeout": 1})
}

func assertSetupAttempts(t *testing.T, h *harness, want map[string]float64) {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(h.gw.Collectors()...)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]float64{}

	for _, f := range families {
		if f.GetName() != "ellamocn_setup_attempts_total" {
			continue
		}

		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}

			if labels["interface"] != "s1" {
				t.Fatalf("interface = %q", labels["interface"])
			}

			if v := m.GetCounter().GetValue(); v != 0 {
				got[labels["side"]+"/"+labels["result"]] = v
			}
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("setup attempts = %v, want %v", got, want)
	}
}

func TestMMEReconnectsAndFollowsConfig(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.5")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.5"))
	mme := mmeListener.accept(t)

	enb := h.connectENB(t)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv32, ta(t, 1, "00101")))
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv32, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	_ = mme.conn.Abort()

	h.eventually(t, "MME down", func() bool {
		st, _ := h.gw.MMEStatus(1, []string{"127.0.0.5"})
		return st.State != s1.MMEUp
	})

	mme = mmeListener.accept(t)
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv32, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 2), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})

	mcu := expect(t, enb, "initiating", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdate)
	if !reflect.DeepEqual(mcu.ServedGUMMEIs, served(t, 1, 2)) {
		t.Fatalf("Served GUMMEIs = %+v", mcu.ServedGUMMEIs)
	}

	h.ops.set()

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	mme.closed(t)

	if _, ok := h.gw.MMEStatus(1, []string{"127.0.0.5"}); ok {
		t.Fatal("removed MME still has a status")
	}
}
