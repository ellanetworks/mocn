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
}

func (f fakeS1) MMEStatus(_ int64, tnl []string) (s1.MMEStatus, bool) {
	st, ok := f.mmes[strings.Join(tnl, ",")]
	return st, ok
}

func (f fakeS1) RANNodes() []s1.RANNode {
	return slices.Clone(f.nodes)
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
	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	capacity := uint8(10)
	a := newS1TestAPI(t, fakeS1{mmes: map[string]s1.MMEStatus{
		"10.0.0.1,10.0.0.2": {
			State: s1.MMEUp, Since: since, PrimaryTNLAddress: "10.0.0.2", MMEName: "mme1",
			ServedGUMMEIs:       []s1.ServedGUMMEIs{{PLMNs: []db.PLMN{{MCC: "001", MNC: "01"}}, GroupIDs: []uint16{0x8001}, MMECodes: []uint8{1}}},
			RelativeMMECapacity: &capacity,
		},
		"10.0.0.3": {State: s1.MMESetupFailed, Since: since, LastFailureCause: "misc: unknown-PLMN (5)", TimeToWait: 10 * time.Second},
	}})

	if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators", operatorA); code != http.StatusCreated {
		t.Fatalf("create = %d %q", code, msg)
	}

	want := []api.MME{
		{TNLAddresses: []string{"10.0.0.1", "10.0.0.2"}, Status: &api.MMEStatus{
			State: s1.MMEUp, Since: since, PrimaryTNLAddress: "10.0.0.2", MMEName: "mme1",
			ServedGUMMEIs:       []api.ServedGUMMEIs{{ServedPLMNs: []api.PLMN{{MCC: "001", MNC: "01"}}, ServedGroupIDs: []uint16{0x8001}, ServedMMECs: []uint8{1}}},
			RelativeMMECapacity: &capacity,
		}},
		{TNLAddresses: []string{"10.0.0.3"}, Status: &api.MMEStatus{
			State: s1.MMESetupFailed, Since: since, LastFailureCause: "misc: unknown-PLMN (5)", TimeToWait: 10,
		}},
	}

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")
	if got := decode[api.Operator](t, result); code != http.StatusOK || !reflect.DeepEqual(got.MMEs, want) {
		t.Fatalf("GET = %d %s", code, result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators", "")
	if got := decode[api.ListOperatorsResponse](t, result); code != http.StatusOK || !reflect.DeepEqual(got.Items[0].MMEs, want) {
		t.Fatalf("list = %d %s", code, result)
	}

	body := strings.Replace(operatorA, `"tnl_addresses": ["10.0.0.3"]`, `"tnl_addresses": ["10.0.0.3"], "status": {"state": "up"}`, 1)
	if code, _, msg := a.do(t, http.MethodPut, "/api/v1/operators/Operator%20A", body); code != http.StatusOK {
		t.Fatalf("PUT with a status = %d %q", code, msg)
	}
}

func TestListRANNodes(t *testing.T) {
	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.FixedZone("x", 3600))
	a := newS1TestAPI(t, fakeS1{nodes: []s1.RANNode{
		{
			GlobalENBID: &db.GlobalENBID{PLMN: db.PLMN{MCC: "001", MNC: "01"}, Type: s1.ENBIDHome, ID: 0xabcdef1},
			Name:        "enb1", DefaultPagingDRX: 128, State: s1.RANNodeUp, ConnectedSince: since,
			SupportedTAs: []s1.TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{{MCC: "001", MNC: "01"}, {MCC: "002", MNC: "002"}}}},
			TNLAddresses: []string{"10.0.0.5", "10.0.1.5"},
		},
		{State: s1.RANNodeConnected, ConnectedSince: since, SupportedTAs: []s1.TA{}, TNLAddresses: []string{"10.0.0.6"}, LastFailureCause: "misc: unknown-PLMN (5)"},
	}})

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/ran-nodes", "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}

	want := api.ListRANNodesResponse{Items: []api.RANNode{
		{
			GlobalENBID: &api.GlobalENBID{PLMN: api.PLMN{MCC: "001", MNC: "01"}, Type: "home", ID: 0xabcdef1},
			ENBName:     "enb1", DefaultPagingDRX: 128, State: s1.RANNodeUp, ConnectedSince: since.UTC(),
			SupportedTAs: []api.TA{{TAC: 1, BroadcastPLMNs: []api.PLMN{{MCC: "001", MNC: "01"}, {MCC: "002", MNC: "002"}}}},
			TNLAddresses: []string{"10.0.0.5", "10.0.1.5"},
		},
		{
			State: s1.RANNodeConnected, ConnectedSince: since.UTC(), SupportedTAs: []api.TA{},
			TNLAddresses: []string{"10.0.0.6"}, LastFailureCause: "misc: unknown-PLMN (5)",
		},
	}}

	if got := decode[api.ListRANNodesResponse](t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %+v, want %+v", got, want)
	}

	for _, field := range []string{`"global_enb_id":null`, `"supported_tas":[]`} {
		if !strings.Contains(string(result), field) {
			t.Errorf("%s missing from %s", field, result)
		}
	}

	empty := newS1TestAPI(t, nil)
	if code, result, _ := empty.do(t, http.MethodGet, "/api/v1/ran-nodes", ""); code != http.StatusOK ||
		string(result) != `{"items":[]}` {
		t.Fatalf("GET without S1 = %d %s", code, result)
	}
}
