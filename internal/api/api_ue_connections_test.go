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
	since := time.Date(2026, 11, 13, 10, 0, 0, 0, time.FixedZone("x", 3600))
	plmn := db.PLMN{MCC: "001", MNC: "01"}
	mmeID := uint32(1000)

	a := newS1TestAPI(t, fakeS1{ues: []s1.UE{
		{
			RadioGlobalENBID: db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 411},
			RadioENBUES1APID: 7, RadioMMEUES1APID: 1, MMETNLAddresses: []string{"10.0.0.1", "10.0.0.2"},
			MMEENBUES1APID: 2, MMEUES1APID: &mmeID, OperatorID: 1, OperatorName: "Operator A", PLMN: plmn, Since: since,
			TAI: &s1.TAI{PLMN: plmn, TAC: 1}, ECGI: &s1.ECGI{PLMN: plmn, CellID: 0x19b01},
		},
		{
			RadioGlobalENBID: db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 411},
			RadioENBUES1APID: 8, RadioMMEUES1APID: 3, MMETNLAddresses: []string{"10.0.0.1"},
			MMEENBUES1APID: 4, OperatorName: "Operator A", PLMN: plmn, Since: since,
		},
		{
			RadioGlobalENBID: db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 412},
			RadioENBUES1APID: 9, RadioMMEUES1APID: 5, MMETNLAddresses: []string{"10.0.1.1"},
			MMEENBUES1APID: 6, OperatorName: "Operator B", PLMN: plmn, Since: since,
		},
	}})

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/ue-connections?radio=001-01:macro:411&operator=Operator%20A", "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}

	want := api.ListUEConnectionsResponse{Items: []api.UEConnection{
		{
			Radio:    api.UEConnectionRadio{Ref: "001-01:macro:411", ENBUES1APID: 7, MMEUES1APID: 1},
			MME:      api.UEConnectionMME{TNLAddresses: []string{"10.0.0.1", "10.0.0.2"}, ENBUES1APID: 2, MMEUES1APID: &mmeID},
			Operator: "Operator A", PLMN: api.PLMN{MCC: "001", MNC: "01"}, Since: since.UTC(),
			TAI:  &api.TAI{PLMN: api.PLMN{MCC: "001", MNC: "01"}, TAC: 1},
			ECGI: &api.ECGI{PLMN: api.PLMN{MCC: "001", MNC: "01"}, CellID: 0x19b01},
		},
		{
			Radio:    api.UEConnectionRadio{Ref: "001-01:macro:411", ENBUES1APID: 8, MMEUES1APID: 3},
			MME:      api.UEConnectionMME{TNLAddresses: []string{"10.0.0.1"}, ENBUES1APID: 4},
			Operator: "Operator A", PLMN: api.PLMN{MCC: "001", MNC: "01"}, Since: since.UTC(),
		},
	}}

	if got := decode[api.ListUEConnectionsResponse](t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %+v, want %+v", got, want)
	}

	for _, field := range []string{`"mme_ue_s1ap_id":null`, `"tai":null`, `"ecgi":null`, `"cell_id":105217`} {
		if !strings.Contains(string(result), field) {
			t.Errorf("%s missing from %s", field, result)
		}
	}

	for query, want := range map[string][]uint32{
		"":                        {7, 8, 9},
		"?radio=001-01:macro:411": {7, 8},
		"?radio=001-01:macro:412": {9},
		"?radio=001-01:macro:413": {},
		"?radio=":                 {},
		"?operator=Operator%20B":  {9},
		"?operator=Operator%20C":  {},
		"?radio=001-01:macro:412&operator=Operator%20A": {},
	} {
		code, result, _ := a.do(t, http.MethodGet, "/api/v1/ue-connections"+query, "")
		got := []uint32{}

		for _, c := range decode[api.ListUEConnectionsResponse](t, result).Items {
			got = append(got, c.Radio.ENBUES1APID)
		}

		if code != http.StatusOK || !slices.Equal(got, want) {
			t.Errorf("GET %s = %d %v, want %v", query, code, got, want)
		}
	}

	empty := newS1TestAPI(t, nil)
	if code, result, _ := empty.do(t, http.MethodGet, "/api/v1/ue-connections", ""); code != http.StatusOK ||
		string(result) != `{"items":[]}` {
		t.Fatalf("GET without S1 = %d %s", code, result)
	}
}
