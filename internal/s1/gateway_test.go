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

func setUpENB(t *testing.T, enb *peer, req *s1ap.S1SetupRequest) *s1ap.S1SetupResponse {
	t.Helper()

	enb.send(t, req)

	return expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
}

func TestS1SetupBothWays(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.2", 0)
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
# HELP ellamocn_ran_nodes_connected RAN nodes whose S1 Setup succeeded.
# TYPE ellamocn_ran_nodes_connected gauge
ellamocn_ran_nodes_connected 1
# HELP ellamocn_s1_associations S1 associations, by side (ran: RAN nodes, mme: configured MMEs) and state.
# TYPE ellamocn_s1_associations gauge
ellamocn_s1_associations{side="mme",state="connected"} 0
ellamocn_s1_associations{side="mme",state="disconnected"} 0
ellamocn_s1_associations{side="mme",state="setup_failed"} 0
ellamocn_s1_associations{side="mme",state="up"} 1
ellamocn_s1_associations{side="ran",state="connected"} 0
ellamocn_s1_associations{side="ran",state="up"} 1
# HELP ellamocn_s1_setup_total S1 Setup procedures, by side (ran: received from RAN nodes, mme: sent to MMEs) and outcome.
# TYPE ellamocn_s1_setup_total counter
ellamocn_s1_setup_total{outcome="failure",side="mme"} 0
ellamocn_s1_setup_total{outcome="failure",side="ran"} 0
ellamocn_s1_setup_total{outcome="success",side="mme"} 1
ellamocn_s1_setup_total{outcome="success",side="ran"} 2
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

	mmeListener := listenMME(t, "127.0.0.3", 0)
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
	if st.State != s1.MMESetupFailed || st.TimeToWait != time.Second || st.LastFailureCause == "" {
		t.Fatalf("MME status = %+v", st)
	}

	nodes := h.gw.RANNodes()
	if len(nodes) != 1 || nodes[0].State != s1.RANNodeConnected || nodes[0].GlobalENBID != nil ||
		nodes[0].LastFailureCause == "" {
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
}

func TestMMEReconnectsAndFollowsConfig(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.5", 0)
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
