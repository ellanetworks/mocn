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

func TestListUEConnections(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 13, 10, 0, 0, 0, time.FixedZone("x", 3600))
	plmn := db.PLMN{MCC: "001", MNC: "01"}
	mmeID := uint32(1000)

	a := newS1TestAPI(t, fakeS1{ues: []s1.UE{
		{
			RadioGlobalENBID: db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 411},
			RadioENBUES1APID: new(uint32(7)), RadioMMEUES1APID: 1, MMETNLAddresses: []string{"10.0.0.1", "10.0.0.2"},
			MMEENBUES1APID: 2, MMEUES1APID: &mmeID, OperatorID: 1, OperatorName: "Operator A", PLMN: plmn, Since: since,
			TAI: &s1.TAI{PLMN: plmn, TAC: 1}, ECGI: &s1.ECGI{PLMN: plmn, CellID: 0x19b01},
		},
		{
			RadioGlobalENBID: db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 411},
			RadioENBUES1APID: new(uint32(8)), RadioMMEUES1APID: 3, MMETNLAddresses: []string{"10.0.0.1"},
			MMEENBUES1APID: 4, OperatorName: "Operator A", PLMN: plmn, Since: since,
		},
		{
			RadioGlobalENBID: db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 412},
			RadioENBUES1APID: new(uint32(9)), RadioMMEUES1APID: 5, MMETNLAddresses: []string{"10.0.1.1"},
			MMEENBUES1APID: 6, OperatorName: "Operator B", PLMN: plmn, Since: since,
		},
	}})

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/ue-connections?radio=eNB:001-01:MacroeNB-0019b&operator=Operator%20A", "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}

	want := api.ListUEConnectionsResponse{Items: []api.UEConnection{
		{
			Interface: "s1",
			Radio:     api.UEConnectionRadio{Ref: "eNB:001-01:MacroeNB-0019b", ENBUES1APID: new(uint32(7)), MMEUES1APID: new(uint32(1))},
			MME:       &api.UEConnectionMME{TNLAddresses: []string{"10.0.0.1", "10.0.0.2"}, ENBUES1APID: 2, MMEUES1APID: &mmeID},
			Operator:  "Operator A", PLMN: api.PLMN{MCC: "001", MNC: "01"}, Since: since.UTC(),
			TAI:  &api.TAI{PLMN: api.PLMN{MCC: "001", MNC: "01"}, TAC: "000001"},
			ECGI: &api.ECGI{PLMN: api.PLMN{MCC: "001", MNC: "01"}, CellID: 0x19b01},
		},
		{
			Interface: "s1",
			Radio:     api.UEConnectionRadio{Ref: "eNB:001-01:MacroeNB-0019b", ENBUES1APID: new(uint32(8)), MMEUES1APID: new(uint32(3))},
			MME:       &api.UEConnectionMME{TNLAddresses: []string{"10.0.0.1"}, ENBUES1APID: 4},
			Operator:  "Operator A", PLMN: api.PLMN{MCC: "001", MNC: "01"}, Since: since.UTC(),
		},
	}, Page: 1, PerPage: 25, TotalCount: 2}

	if got := decode[api.ListUEConnectionsResponse](t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %+v, want %+v", got, want)
	}

	for _, field := range []string{`"cell_id":105217`, `"tac":"000001"`, `"interface":"s1"`, `"mme":{"tnl_addresses":["10.0.0.1"],"enb_ue_s1ap_id":4}`} {
		if !strings.Contains(string(result), field) {
			t.Errorf("%s missing from %s", field, result)
		}
	}

	if strings.Contains(string(result), "null") || strings.Count(string(result), `"tai"`) != 1 {
		t.Errorf("GET = %s, want unknown IDs and TAIs left out", result)
	}

	for query, want := range map[string][]uint32{
		"":                                 {7, 8, 9},
		"?radio=eNB:001-01:MacroeNB-0019b": {7, 8},
		"?radio=eNB:001-01:MacroeNB-0019c": {9},
		"?radio=eNB:001-01:MacroeNB-0019d": {},
		"?radio=":                          {},
		"?operator=Operator%20B":           {9},
		"?operator=Operator%20C":           {},
		"?radio=eNB:001-01:MacroeNB-0019c&operator=Operator%20A": {},
	} {
		code, result, _ := a.do(t, http.MethodGet, "/api/v1/ue-connections"+query, "")
		got := []uint32{}

		for _, c := range decode[api.ListUEConnectionsResponse](t, result).Items {
			got = append(got, *c.Radio.ENBUES1APID)
		}

		if code != http.StatusOK || !slices.Equal(got, want) {
			t.Errorf("GET %s = %d %v, want %v", query, code, got, want)
		}
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/ue-connections?page=2&per_page=2", "")
	if got := decode[api.ListUEConnectionsResponse](t, result); code != http.StatusOK || len(got.Items) != 1 ||
		*got.Items[0].Radio.ENBUES1APID != 9 || got.Page != 2 || got.PerPage != 2 || got.TotalCount != 3 {
		t.Fatalf("GET page 2 = %d %s", code, result)
	}

	if code, _, _ := a.do(t, http.MethodGet, "/api/v1/ue-connections?per_page=101", ""); code != http.StatusBadRequest {
		t.Errorf("GET ?per_page=101 = %d, want 400", code)
	}

	empty := newS1TestAPI(t, nil)
	if code, result, _ := empty.do(t, http.MethodGet, "/api/v1/ue-connections", ""); code != http.StatusOK ||
		string(result) != `{"items":[],"page":1,"per_page":25,"total_count":0}` {
		t.Fatalf("GET without S1 = %d %s", code, result)
	}
}
