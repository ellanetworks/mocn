package api_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ellanetworks/mocn/internal/api"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
)

type fakeS1 struct {
	mmes  map[string]s1.MMEStatus
	nodes []s1.RANNode
	ues   []s1.UE
}

func (f fakeS1) MMEStatus(_ int64, tnl []string) (s1.MMEStatus, bool) {
	st, ok := f.mmes[strings.Join(tnl, ",")]
	return st, ok
}

func (f fakeS1) RANNodes() []s1.RANNode {
	return slices.Clone(f.nodes)
}

func (f fakeS1) UEs() []s1.UE {
	return slices.Clone(f.ues)
}

func newS1TestAPI(t *testing.T, view api.S1View) *testAPI {
	t.Helper()

	store := openStore(t)
	a := &testAPI{store: store, now: testNow}
	cfg := testConfig(t, store)
	cfg.S1 = view
	a.handler = api.NewHandler(cfg)
	a.token = initialize(t, a.handler)

	return a
}

func TestOperatorMMEStatus(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	capacity, reduction := uint8(10), uint8(40)
	a := newS1TestAPI(t, fakeS1{mmes: map[string]s1.MMEStatus{
		"10.0.0.1,10.0.0.2": {
			State: s1.MMEUp, Since: since, PrimaryTNLAddress: "10.0.0.2", MMEName: "mme1",
			ServedGUMMEIs:       []s1.ServedGUMMEIs{{PLMNs: []db.PLMN{{MCC: "001", MNC: "01"}}, GroupIDs: []uint16{0x8001}, MMECodes: []uint8{1}}},
			RelativeMMECapacity: &capacity,
			Overload: []s1.Overload{{
				GUMMEI: s1.GUMMEI{PLMN: db.PLMN{MCC: "001", MNC: "01"}, MMEGroupID: 0x8001, MMECode: 1},
				Action: s1.OverloadRejectRRCCRSignalling, TrafficLoadReductionIndication: &reduction,
			}},
		},
		"10.0.0.3": {State: s1.MMESetupFailed, Since: since},
	}, ues: []s1.UE{
		{OperatorID: 1, MMETNLAddresses: []string{"10.0.0.1", "10.0.0.2"}},
		{OperatorID: 1, MMETNLAddresses: []string{"10.0.0.1", "10.0.0.2"}},
		{OperatorID: 1, MMETNLAddresses: []string{"10.0.0.4"}},
		{OperatorID: 2, MMETNLAddresses: []string{"10.0.0.3"}},
	}})

	if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators", operatorA); code != http.StatusCreated {
		t.Fatalf("create = %d %q", code, msg)
	}

	want := []api.MME{
		{TNLAddresses: []string{"10.0.0.1", "10.0.0.2"}, Status: &api.MMEStatus{
			State: s1.MMEUp, Since: since, PrimaryTNLAddress: "10.0.0.2", MMEName: "mme1",
			ServedGUMMEIs:       []api.ServedGUMMEIsItem{{ServedPLMNs: []api.PLMN{{MCC: "001", MNC: "01"}}, ServedGroupIDs: []uint16{0x8001}, ServedMMECs: []int{1}}},
			RelativeMMECapacity: &capacity, UEConnections: 2,
			Overload: []api.MMEOverload{{
				GUMMEI:         api.GUMMEI{PLMN: api.PLMN{MCC: "001", MNC: "01"}, MMEGroupID: 0x8001, MMECode: 1},
				OverloadAction: s1.OverloadRejectRRCCRSignalling, TrafficLoadReductionIndication: &reduction,
			}},
		}},
		{TNLAddresses: []string{"10.0.0.3"}, Status: &api.MMEStatus{
			State: s1.MMESetupFailed, Since: since,
		}},
	}

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")
	if got := decode[api.Operator](t, result); code != http.StatusOK || !reflect.DeepEqual(got.MMEs, want) ||
		got.UEConnections == nil || *got.UEConnections != 2 {
		t.Fatalf("GET = %d %s", code, result)
	}

	if !strings.Contains(string(result), `"state":"setup_failed","since":"2026-11-12T10:00:00Z","ue_connections":0`) {
		t.Fatalf("GET = %s, want ue_connections 0 on the MME without UE connections", result)
	}

	if !strings.Contains(string(result), `"overload":[{"gummei":{"plmn":{"mcc":"001","mnc":"01"},"mme_group_id":32769,"mme_code":1},`+
		`"overload_action":"reject_rrc_cr_signalling","traffic_load_reduction_indication":40}]`) {
		t.Fatalf("GET = %s, want the overload state", result)
	}

	if !strings.Contains(string(result), `"served_mmecs":[1]`) {
		t.Fatalf("GET = %s, want served_mmecs as a JSON array of numbers", result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators", "")
	if got := decode[api.ListOperatorsResponse](t, result); code != http.StatusOK || !reflect.DeepEqual(got.Items[0].MMEs, want) ||
		got.Items[0].UEConnections == nil || *got.Items[0].UEConnections != 2 {
		t.Fatalf("list = %d %s", code, result)
	}

	body := strings.Replace(operatorA, `"tnl_addresses": ["10.0.0.3"]`,
		`"tnl_addresses": ["10.0.0.3"], "status": {"state": "up", "ue_connections": 7}`, 1)
	body = strings.Replace(body, `"name": "Operator A"`, `"name": "Operator A", "ue_connections": 7`, 1)

	code, result, msg := a.do(t, http.MethodPut, "/api/v1/operators/Operator%20A", body)
	if got := decode[api.Operator](t, result); code != http.StatusOK || *got.UEConnections != 2 || !reflect.DeepEqual(got.MMEs, want) {
		t.Fatalf("PUT with a status and ue_connections = %d %q %s, want them ignored", code, msg, result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")
	if got := decode[api.Operator](t, result); code != http.StatusOK || *got.UEConnections != 2 || !reflect.DeepEqual(got.MMEs, want) {
		t.Fatalf("GET after PUT = %d %s", code, result)
	}
}

func TestListRadios(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.FixedZone("x", 3600))
	a := newS1TestAPI(t, fakeS1{nodes: []s1.RANNode{
		{
			GlobalENBID: &db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDHome, ID: 0xabcdef1},
			Name:        "enb1", DefaultPagingDRX: 128, State: s1.RANNodeUp, ConnectedSince: since,
			SupportedTAs: []s1.TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{{MCC: "001", MNC: "01"}, {MCC: "002", MNC: "002"}}}},
			TNLAddresses: []string{"10.0.0.5", "10.0.1.5"},
		},
		{State: s1.RANNodeConnected, ConnectedSince: since, SupportedTAs: []s1.TA{}, TNLAddresses: []string{"10.0.0.6"}},
	}, ues: []s1.UE{
		{RadioGlobalENBID: db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDHome, ID: 0xabcdef1}},
		{RadioGlobalENBID: db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDHome, ID: 0xabcdef1}},
		{RadioGlobalENBID: db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDHome, ID: 1}},
	}})

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/radios", "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}

	want := api.ListRadiosResponse{Items: []api.Radio{
		{
			Ref:         "eNB:001-01:HomeeNB-abcdef1",
			GlobalENBID: &api.GlobalENBID{PLMN: api.PLMN{MCC: "001", MNC: "01"}, Type: "home", ID: 0xabcdef1},
			Type:        "eNB", Name: "enb1", DefaultPagingDRX: 128, State: "up", ConnectedAt: since.UTC(),
			SupportedTAs: []api.SupportedTA{{TAC: "000001", BroadcastPLMNs: []api.BroadcastPLMN{
				{PLMN: api.PLMN{MCC: "001", MNC: "01"}}, {PLMN: api.PLMN{MCC: "002", MNC: "002"}},
			}}},
			TNLAddresses: []string{"10.0.0.5", "10.0.1.5"}, UEConnections: 2,
		},
		{
			Type: "eNB", State: "connected", ConnectedAt: since.UTC(), SupportedTAs: []api.SupportedTA{},
			TNLAddresses: []string{"10.0.0.6"},
		},
	}, Page: 1, PerPage: 25, TotalCount: 2}

	if got := decode[api.ListRadiosResponse](t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %+v, want %+v", got, want)
	}

	for _, field := range []string{
		`"name":"enb1"`, `"connected_at":"2026-11-12T09:00:00Z"`, `"tac":"000001"`, `"supported_tas":[]`, `"ue_connections":0`,
		`"broadcast_plmns":[{"plmn":{"mcc":"001","mnc":"01"}},{"plmn":{"mcc":"002","mnc":"002"}}]`,
	} {
		if !strings.Contains(string(result), field) {
			t.Errorf("%s missing from %s", field, result)
		}
	}

	empty := newS1TestAPI(t, nil)
	if code, result, _ := empty.do(t, http.MethodGet, "/api/v1/radios", ""); code != http.StatusOK ||
		string(result) != `{"items":[],"page":1,"per_page":25,"total_count":0}` {
		t.Fatalf("GET without S1 = %d %s", code, result)
	}

	if strings.Contains(string(result), "ue_connections_by_operator") {
		t.Errorf("list = %s, want no ue_connections_by_operator", result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/radios?page=2&per_page=1", "")
	if got := decode[api.ListRadiosResponse](t, result); code != http.StatusOK ||
		!reflect.DeepEqual(got, api.ListRadiosResponse{Items: want.Items[1:], Page: 2, PerPage: 1, TotalCount: 2}) {
		t.Fatalf("GET page 2 = %d %s", code, result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/radios?page=3&per_page=1", "")
	if got := decode[api.ListRadiosResponse](t, result); code != http.StatusOK || len(got.Items) != 0 || got.TotalCount != 2 {
		t.Fatalf("GET page 3 = %d %s", code, result)
	}

	for _, q := range []string{"page=0", "per_page=0", "per_page=101", "page=x"} {
		if code, _, _ := a.do(t, http.MethodGet, "/api/v1/radios?"+q, ""); code != http.StatusBadRequest {
			t.Errorf("GET ?%s = %d, want 400", q, code)
		}
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/radios/eNB:001-01:HomeeNB-abcdef1", "")
	if got := decode[api.Radio](t, result); code != http.StatusOK || !reflect.DeepEqual(got, want.Items[0]) ||
		strings.Contains(string(result), "ue_connections_by_operator") {
		t.Fatalf("GET one = %d %s, want no ue_connections_by_operator without operators", code, result)
	}

	for _, ref := range []string{"eNB:001-01:HomeeNB-0000001", "001-01:home:180150001", "x"} {
		if code, _, _ := a.do(t, http.MethodGet, "/api/v1/radios/"+ref, ""); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", ref, code)
		}
	}

	if code, _, _ := empty.do(t, http.MethodGet, "/api/v1/radios/eNB:001-01:HomeeNB-abcdef1", ""); code != http.StatusNotFound {
		t.Errorf("GET one without S1 = %d, want 404", code)
	}
}

func TestRadioUEConnectionsByOperator(t *testing.T) {
	t.Parallel()

	radio := db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDMacro, ID: 1}
	other := db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDMacro, ID: 2}
	a := newS1TestAPI(t, fakeS1{nodes: []s1.RANNode{
		{
			GlobalENBID: &radio, State: s1.RANNodeUp,
			SupportedTAs: []s1.TA{
				{TAC: 1, BroadcastPLMNs: []db.PLMN{{MCC: "003", MNC: "03"}, {MCC: "001", MNC: "01"}}},
				{TAC: 2, BroadcastPLMNs: []db.PLMN{{MCC: "001", MNC: "002"}, {MCC: "999", MNC: "99"}}},
			},
		},
		{GlobalENBID: &other, State: s1.RANNodeUp, SupportedTAs: []s1.TA{}},
	}, ues: []s1.UE{
		{RadioGlobalENBID: radio, OperatorID: 1, OperatorName: "stale name"},
		{RadioGlobalENBID: radio, OperatorID: 1},
		{RadioGlobalENBID: radio, OperatorID: 2},
		{RadioGlobalENBID: radio, OperatorID: 9, OperatorName: "Operator D"},
		{RadioGlobalENBID: radio, OperatorID: 10},
		{RadioGlobalENBID: other, OperatorID: 3},
	}})

	for _, body := range []string{
		operatorA,
		`{"name": "Operator B", "plmns": [{"mcc": "002", "mnc": "02"}]}`,
		`{"name": "Operator C", "plmns": [{"mcc": "003", "mnc": "03"}]}`,
		`{"name": "Operator E", "plmns": [{"mcc": "005", "mnc": "05"}]}`,
	} {
		if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators", body); code != http.StatusCreated {
			t.Fatalf("create = %d %q", code, msg)
		}
	}

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/radios/eNB:001-01:MacroeNB-00001", "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d %s", code, result)
	}

	want := []api.OperatorUEConnections{
		{Operator: "Operator A", UEConnections: 2},
		{Operator: "Operator B", UEConnections: 1},
		{Operator: "Operator C", UEConnections: 0},
		{Operator: "Operator D", UEConnections: 1},
	}
	if got := decode[api.Radio](t, result).UEConnectionsByOperator; !reflect.DeepEqual(got, want) {
		t.Fatalf("ue_connections_by_operator = %+v, want %+v", got, want)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/radios/eNB:001-01:MacroeNB-00002", "")
	if !strings.Contains(string(result), `"ue_connections_by_operator":[{"operator":"Operator C","ue_connections":1}]`) {
		t.Fatalf("GET other = %d %s", code, result)
	}
}

func TestOperatorUEConnectionsByRadio(t *testing.T) {
	t.Parallel()

	enb := func(id uint32) *db.GlobalENBID {
		return &db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDMacro, ID: id}
	}
	tas := func(plmn db.PLMN) []s1.TA {
		return []s1.TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{{MCC: "999", MNC: "99"}, plmn}}}
	}
	own, foreign := db.PLMN{MCC: "001", MNC: "01"}, db.PLMN{MCC: "999", MNC: "98"}
	a := newS1TestAPI(t, fakeS1{nodes: []s1.RANNode{
		{GlobalENBID: enb(1), Name: "enb1", SupportedTAs: tas(own)},
		{GlobalENBID: enb(2), SupportedTAs: tas(db.PLMN{MCC: "001", MNC: "002"})},
		{GlobalENBID: enb(3), Name: "aaa", SupportedTAs: tas(foreign)},
		{GlobalENBID: enb(4), Name: "unrelated", SupportedTAs: tas(foreign)},
		{SupportedTAs: tas(own)},
	}, ues: []s1.UE{
		{RadioGlobalENBID: *enb(1), OperatorID: 1},
		{RadioGlobalENBID: *enb(1), OperatorID: 1},
		{RadioGlobalENBID: *enb(1), OperatorID: 2},
		{RadioGlobalENBID: *enb(3), OperatorID: 1},
		{RadioGlobalENBID: *enb(5), OperatorID: 1},
	}})

	enbID := func(id int64) *api.GlobalENBID {
		return &api.GlobalENBID{PLMN: api.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDMacro, ID: id}
	}
	want := []api.RadioUEConnections{
		{Ref: "eNB:001-01:MacroeNB-00003", Type: "eNB", GlobalENBID: enbID(3), Name: "aaa", UEConnections: 1},
		{Ref: "eNB:001-01:MacroeNB-00002", Type: "eNB", GlobalENBID: enbID(2), UEConnections: 0},
		{Ref: "eNB:001-01:MacroeNB-00005", Type: "eNB", UEConnections: 1},
		{Ref: "eNB:001-01:MacroeNB-00001", Type: "eNB", GlobalENBID: enbID(1), Name: "enb1", UEConnections: 2},
	}

	code, result, msg := a.do(t, http.MethodPost, "/api/v1/operators", operatorA)
	if got := decode[api.Operator](t, result); code != http.StatusCreated || !reflect.DeepEqual(got.UEConnectionsByRadio, want) ||
		got.UEConnections == nil || *got.UEConnections != 0 {
		t.Fatalf("create = %d %q %s", code, msg, result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")
	if got := decode[api.Operator](t, result); code != http.StatusOK || !reflect.DeepEqual(got.UEConnectionsByRadio, want) {
		t.Fatalf("GET = %d %s", code, result)
	}

	if !strings.Contains(string(result), `{"ref":"eNB:001-01:MacroeNB-00002","type":"eNB","global_enb_id":`+
		`{"plmn":{"mcc":"001","mnc":"01"},"enb_id_type":"macro","enb_id":2},"ue_connections":0}`) {
		t.Fatalf("GET = %s, want a radio without a name and with 0 UE connections", result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators", "")
	if code != http.StatusOK || strings.Contains(string(result), "ue_connections_by_radio") {
		t.Fatalf("list = %d %s, want no ue_connections_by_radio", code, result)
	}

	body := strings.Replace(operatorA, `"name": "Operator A"`,
		`"name": "Operator A", "ue_connections_by_radio": [{"ref": "x", "type": "eNB", "ue_connections": 7}]`, 1)

	code, result, msg = a.do(t, http.MethodPut, "/api/v1/operators/Operator%20A", body)
	if got := decode[api.Operator](t, result); code != http.StatusOK || !reflect.DeepEqual(got.UEConnectionsByRadio, want) {
		t.Fatalf("PUT with ue_connections_by_radio = %d %q %s, want it ignored", code, msg, result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")
	if got := decode[api.Operator](t, result); code != http.StatusOK || !reflect.DeepEqual(got.UEConnectionsByRadio, want) {
		t.Fatalf("GET after PUT = %d %s", code, result)
	}

	empty := newS1TestAPI(t, nil)
	if code, result, msg := empty.do(t, http.MethodPost, "/api/v1/operators", operatorA); code != http.StatusCreated ||
		strings.Contains(string(result), "ue_connections") {
		t.Fatalf("create without S1 = %d %q %s", code, msg, result)
	}

	code, result, _ = empty.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")
	if code != http.StatusOK || strings.Contains(string(result), "ue_connections") {
		t.Fatalf("GET without S1 = %d %s", code, result)
	}
}
