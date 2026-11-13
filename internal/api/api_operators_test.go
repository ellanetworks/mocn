package api_test

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/ellanetworks/mocn/internal/api"
	"github.com/ellanetworks/mocn/internal/db"
)

const operatorA = `{"name": "Operator A", "pws_operator": true, "plmns": [{"mcc": "001", "mnc": "01"}, {"mcc": "001", "mnc": "002"}],
	"global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "macro", "enb_id": 703710},
	"global_gnb_id": {"plmn": {"mcc": "001", "mnc": "002"}, "gnb_id": 74565, "gnb_id_length": 22},
	"mmes": [{"tnl_addresses": ["10.0.0.1", "::ffff:10.0.0.2"]}, {"tnl_addresses": ["10.0.0.3"]}],
	"amfs": [{"tnl_addresses": ["2001:db8::1"]}]}`

func TestOperators(t *testing.T) {
	a := newTestAPI(t)

	code, result, msg := a.do(t, http.MethodPost, "/api/v1/operators", operatorA)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %q", code, msg)
	}

	want := api.Operator{
		Name: "Operator A", PWSOperator: true,
		PLMNs:       []api.PLMN{{MCC: "001", MNC: "01"}, {MCC: "001", MNC: "002"}},
		GlobalENBID: &api.GlobalENBID{PLMN: api.PLMN{MCC: "001", MNC: "01"}, Type: db.ENBIDMacro, ID: 703710},
		GlobalGNBID: &api.GlobalGNBID{PLMN: api.PLMN{MCC: "001", MNC: "002"}, ID: 74565, Length: 22},
		MMEs:        []api.MME{{TNLAddresses: []string{"10.0.0.1", "10.0.0.2"}}, {TNLAddresses: []string{"10.0.0.3"}}},
		AMFs:        []api.AMF{{TNLAddresses: []string{"2001:db8::1"}}},
	}

	if got := decode[api.Operator](t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("created = %+v, want %+v", got, want)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")
	if got := decode[api.Operator](t, result); code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %d %+v", code, got)
	}

	code, result, msg = a.do(t, http.MethodPost, "/api/v1/operators",
		`{"name": " Operator B ", "plmns": [{"mcc": "002", "mnc": "01"}]}`)
	if code != http.StatusCreated {
		t.Fatalf("create B = %d %q", code, msg)
	}

	if !strings.Contains(string(result), `"global_enb_id":null`) || !strings.Contains(string(result), `"mmes":[]`) ||
		!strings.Contains(string(result), `"amfs":[]`) {
		t.Fatalf("create B = %s", result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators", "")
	if list := decode[api.ListOperatorsResponse](t, result); code != http.StatusOK || len(list.Items) != 2 ||
		list.Items[0].Name != "Operator A" || list.Items[1].Name != "Operator B" {
		t.Fatalf("list = %d %s", code, result)
	}

	for name, tc := range map[string]struct {
		method, path, body string
		want               int
		msg                string
	}{
		"duplicate name":     {http.MethodPost, "/api/v1/operators", `{"name": "Operator A", "plmns": [{"mcc": "003", "mnc": "01"}]}`, http.StatusConflict, "Another operator already has name Operator A"},
		"duplicate PLMN":     {http.MethodPost, "/api/v1/operators", `{"name": "C", "plmns": [{"mcc": "001", "mnc": "01"}]}`, http.StatusConflict, "Another operator already has PLMN 00101"},
		"duplicate PWS":      {http.MethodPost, "/api/v1/operators", `{"name": "C", "pws_operator": true, "plmns": [{"mcc": "003", "mnc": "01"}]}`, http.StatusConflict, "Another operator is already the PWS operator"},
		"rename to existing": {http.MethodPut, "/api/v1/operators/Operator%20B", `{"name": "Operator A", "plmns": [{"mcc": "002", "mnc": "01"}]}`, http.StatusConflict, "Another operator already has name Operator A"},
		"get unknown":        {http.MethodGet, "/api/v1/operators/C", "", http.StatusNotFound, "Operator not found"},
		"update unknown":     {http.MethodPut, "/api/v1/operators/C", `{"name": "C", "plmns": [{"mcc": "003", "mnc": "01"}]}`, http.StatusNotFound, "Operator not found"},
		"delete unknown":     {http.MethodDelete, "/api/v1/operators/C", "", http.StatusNotFound, "Operator not found"},
		"unknown field":      {http.MethodPost, "/api/v1/operators", `{"name": "C", "plmns": [{"mcc": "003", "mnc": "01"}], "x": 1}`, http.StatusBadRequest, "Invalid request data"},
	} {
		if code, _, msg := a.do(t, tc.method, tc.path, tc.body); code != tc.want || msg != tc.msg {
			t.Errorf("%s: %d %q, want %d %q", name, code, msg, tc.want, tc.msg)
		}
	}

	code, _, msg = a.do(t, http.MethodPut, "/api/v1/operators/Operator%20B",
		`{"name": "Operator B", "plmns": [{"mcc": "002", "mnc": "01"}], "global_enb_id": {"plmn": {"mcc": "002", "mnc": "01"}, "enb_id_type": "long_macro", "enb_id": 1}, "mmes": [{"tnl_addresses": ["10.0.0.1"]}]}`)
	if code != http.StatusOK {
		t.Fatalf("same MME address as another operator = %d %q", code, msg)
	}

	code, result, msg = a.do(t, http.MethodPut, "/api/v1/operators/Operator%20B",
		`{"name": "Operator C", "plmns": [{"mcc": "002", "mnc": "01"}], "global_gnb_id": {"plmn": {"mcc": "002", "mnc": "01"}, "gnb_id": 4294967295, "gnb_id_length": 32}, "amfs": [{"tnl_addresses": ["10.0.1.1"]}]}`)
	if code != http.StatusOK || decode[api.Operator](t, result).Name != "Operator C" {
		t.Fatalf("rename = %d %q %s", code, msg, result)
	}

	if code, _, _ := a.do(t, http.MethodGet, "/api/v1/operators/Operator%20B", ""); code != http.StatusNotFound {
		t.Fatalf("GET old name = %d", code)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators/Operator%20C", "")
	if got := decode[api.Operator](t, result); code != http.StatusOK || got.GlobalGNBID == nil || got.GlobalGNBID.ID != 4294967295 ||
		!reflect.DeepEqual(got.AMFs, []api.AMF{{TNLAddresses: []string{"10.0.1.1"}}}) {
		t.Fatalf("GET renamed = %d %s", code, result)
	}

	if code, _, _ := a.do(t, http.MethodDelete, "/api/v1/operators/Operator%20A", ""); code != http.StatusOK {
		t.Fatalf("delete = %d", code)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/operators", "")
	if list := decode[api.ListOperatorsResponse](t, result); code != http.StatusOK || len(list.Items) != 1 {
		t.Fatalf("list after delete = %d %s", code, result)
	}

	for _, action := range []string{"create_operator", "update_operator", "delete_operator"} {
		if !strings.Contains(a.logs.String(), `"action":"`+action+`"`) {
			t.Errorf("no %s audit event", action)
		}
	}
}

func TestOperatorValidation(t *testing.T) {
	a := newTestAPI(t)

	const (
		plmn = `"plmns": [{"mcc": "001", "mnc": "01"}]`
		enb  = `"global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "macro", "enb_id": 1}`
		gnb  = `"global_gnb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "gnb_id": 1, "gnb_id_length": 22}`
	)

	for name, tc := range map[string]struct{ body, msg string }{
		"no name":            {`{"name": "  ", ` + plmn + `}`, "name is required"},
		"long name":          {`{"name": "` + strings.Repeat("é", 65) + `", ` + plmn + `}`, "name must be at most 64 characters"},
		"no PLMN":            {`{"name": "A", "plmns": []}`, "at least one PLMN is required"},
		"short MCC":          {`{"name": "A", "plmns": [{"mcc": "01", "mnc": "01"}]}`, "plmns: mcc must be 3 digits"},
		"letter MCC":         {`{"name": "A", "plmns": [{"mcc": "0a1", "mnc": "01"}]}`, "plmns: mcc must be 3 digits"},
		"short MNC":          {`{"name": "A", "plmns": [{"mcc": "001", "mnc": "1"}]}`, "plmns: mnc must be 2 or 3 digits"},
		"long MNC":           {`{"name": "A", "plmns": [{"mcc": "001", "mnc": "0001"}]}`, "plmns: mnc must be 2 or 3 digits"},
		"duplicate PLMN":     {`{"name": "A", "plmns": [{"mcc": "001", "mnc": "01"}, {"mcc": "001", "mnc": "01"}]}`, "PLMN 00101 is listed twice"},
		"eNB type":           {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "pico", "enb_id": 1}}`, "global_enb_id.enb_id_type must be macro, short_macro or long_macro"},
		"home eNB ID":        {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "home", "enb_id": 1}}`, "global_enb_id.enb_id_type must be macro, short_macro or long_macro"},
		"13 PLMNs":           {`{"name": "A", "plmns": [` + strings.TrimSuffix(strings.Repeat(`{"mcc": "001", "mnc": "01"},`, 13), ",") + `]}`, "an operator can have at most 12 PLMNs"},
		"macro eNB ID":       {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "macro", "enb_id": 1048576}}`, "global_enb_id.enb_id must be between 0 and 1048575 for a macro eNB ID"},
		"short macro eNB ID": {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "short_macro", "enb_id": 262144}}`, "global_enb_id.enb_id must be between 0 and 262143 for a short_macro eNB ID"},
		"long macro eNB ID":  {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "long_macro", "enb_id": 2097152}}`, "global_enb_id.enb_id must be between 0 and 2097151 for a long_macro eNB ID"},
		"negative eNB ID":    {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "enb_id_type": "macro", "enb_id": -1}}`, "global_enb_id.enb_id must be between 0 and 1048575 for a macro eNB ID"},
		"foreign eNB PLMN":   {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "001", "mnc": "02"}, "enb_id_type": "macro", "enb_id": 1}}`, "global_enb_id.plmn must be one of the operator's plmns"},
		"invalid eNB PLMN":   {`{"name": "A", ` + plmn + `, "global_enb_id": {"plmn": {"mcc": "1", "mnc": "02"}, "enb_id_type": "macro", "enb_id": 1}}`, "global_enb_id.plmn: mcc must be 3 digits"},
		"short gNB ID":       {`{"name": "A", ` + plmn + `, "global_gnb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "gnb_id": 1, "gnb_id_length": 21}}`, "global_gnb_id.gnb_id_length must be between 22 and 32"},
		"long gNB ID":        {`{"name": "A", ` + plmn + `, "global_gnb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "gnb_id": 1, "gnb_id_length": 33}}`, "global_gnb_id.gnb_id_length must be between 22 and 32"},
		"gNB ID range":       {`{"name": "A", ` + plmn + `, "global_gnb_id": {"plmn": {"mcc": "001", "mnc": "01"}, "gnb_id": 4194304, "gnb_id_length": 22}}`, "global_gnb_id.gnb_id must be between 0 and 4194303 for a 22-bit gNB ID"},
		"foreign gNB PLMN":   {`{"name": "A", ` + plmn + `, "global_gnb_id": {"plmn": {"mcc": "001", "mnc": "02"}, "gnb_id": 1, "gnb_id_length": 22}}`, "global_gnb_id.plmn must be one of the operator's plmns"},
		"MME without eNB":    {`{"name": "A", ` + plmn + `, "mmes": [{"tnl_addresses": ["10.0.0.1"]}]}`, "mmes require a global_enb_id"},
		"AMF without gNB":    {`{"name": "A", ` + plmn + `, "amfs": [{"tnl_addresses": ["10.0.0.1"]}]}`, "amfs require a global_gnb_id"},
		"MME no address":     {`{"name": "A", ` + plmn + `, ` + enb + `, "mmes": [{"tnl_addresses": []}]}`, "mmes: each MME needs at least one TNL address"},
		"AMF no address":     {`{"name": "A", ` + plmn + `, ` + gnb + `, "amfs": [{}]}`, "amfs: each AMF needs at least one TNL address"},
		"MME hostname":       {`{"name": "A", ` + plmn + `, ` + enb + `, "mmes": [{"tnl_addresses": ["mme.example.com"]}]}`, `mmes: tnl_addresses must be IPv4 or IPv6 addresses, not "mme.example.com"`},
		"MME zone":           {`{"name": "A", ` + plmn + `, ` + enb + `, "mmes": [{"tnl_addresses": ["fe80::1%eth0"]}]}`, `mmes: tnl_addresses must be IPv4 or IPv6 addresses, not "fe80::1%eth0"`},
		"duplicate MME":      {`{"name": "A", ` + plmn + `, ` + enb + `, "mmes": [{"tnl_addresses": ["10.0.0.1", "::ffff:10.0.0.1"]}]}`, "mmes list 10.0.0.1 twice"},
		"MME across nodes":   {`{"name": "A", ` + plmn + `, ` + enb + `, "mmes": [{"tnl_addresses": ["10.0.0.1"]}, {"tnl_addresses": ["10.0.0.2", "10.0.0.1"]}]}`, "mmes list 10.0.0.1 twice"},
		"AMF CIDR":           {`{"name": "A", ` + plmn + `, ` + gnb + `, "amfs": [{"tnl_addresses": ["10.0.0.0/24"]}]}`, `amfs: tnl_addresses must be IPv4 or IPv6 addresses, not "10.0.0.0/24"`},
		"duplicate AMF":      {`{"name": "A", ` + plmn + `, ` + gnb + `, "amfs": [{"tnl_addresses": ["2001:db8::1"]}, {"tnl_addresses": ["2001:DB8::1"]}]}`, "amfs list 2001:db8::1 twice"},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			path := "/api/v1/operators"
			if method == http.MethodPut {
				path += "/A"
			}

			if code, _, msg := a.do(t, method, path, tc.body); code != http.StatusBadRequest || msg != tc.msg {
				t.Errorf("%s %s: %d %q, want 400 %q", method, name, code, msg, tc.msg)
			}
		}
	}

	if code, _, _ := a.do(t, http.MethodGet, "/api/v1/operators", ""); code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
}

func TestOperatorPermissions(t *testing.T) {
	a := newTestAPI(t)

	a.createUser(t, "ro@example.com", db.RoleReadOnly)
	a.createUser(t, "nm@example.com", db.RoleNetworkManager)

	readOnly, _ := a.login(t, "ro@example.com", testPassword)
	networkManager, _ := a.login(t, "nm@example.com", testPassword)

	if code, _, msg := a.doAs(t, networkManager, http.MethodPost, "/api/v1/operators",
		`{"name": "A", "plmns": [{"mcc": "001", "mnc": "01"}]}`); code != http.StatusCreated {
		t.Fatalf("create as network manager = %d %q", code, msg)
	}

	for _, path := range []string{"/api/v1/operators", "/api/v1/operators/A"} {
		if code, _, _ := a.doAs(t, readOnly, http.MethodGet, path, ""); code != http.StatusOK {
			t.Errorf("GET %s as read only = %d", path, code)
		}
	}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/operators", `{"name": "B", "plmns": [{"mcc": "001", "mnc": "02"}]}`},
		{http.MethodPut, "/api/v1/operators/A", `{"name": "A", "plmns": [{"mcc": "001", "mnc": "01"}]}`},
		{http.MethodDelete, "/api/v1/operators/A", ""},
	} {
		if code, _, _ := a.doAs(t, readOnly, tc.method, tc.path, tc.body); code != http.StatusForbidden {
			t.Errorf("%s %s as read only = %d, want 403", tc.method, tc.path, code)
		}
	}

	if code, _, msg := a.doAs(t, networkManager, http.MethodPut, "/api/v1/operators/A",
		`{"name": "A", "plmns": [{"mcc": "001", "mnc": "03"}]}`); code != http.StatusOK {
		t.Fatalf("update as network manager = %d %q", code, msg)
	}

	if code, _, msg := a.doAs(t, networkManager, http.MethodDelete, "/api/v1/operators/A", ""); code != http.StatusOK {
		t.Fatalf("delete as network manager = %d %q", code, msg)
	}

	if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators",
		`{"name": "A", "plmns": [{"mcc": "001", "mnc": "01"}]}`); code != http.StatusCreated {
		t.Fatalf("create as admin = %d %q", code, msg)
	}
}

func TestOperatorPLMNTotals(t *testing.T) {
	a := newTestAPI(t)

	operator := func(name, mcc string, n int, amf bool) string {
		plmns := make([]string, 0, n)
		for i := range n {
			plmns = append(plmns, fmt.Sprintf(`{"mcc": %q, "mnc": "%02d"}`, mcc, i))
		}

		body := fmt.Sprintf(`{"name": %q, "plmns": [%s]`, name, strings.Join(plmns, ", "))
		if amf {
			body += fmt.Sprintf(`, "global_gnb_id": {"plmn": {"mcc": %q, "mnc": "00"}, "gnb_id": 1, "gnb_id_length": 22}, "amfs": [{"tnl_addresses": ["10.0.0.1"]}]`, mcc)
		}

		return body + "}"
	}

	if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators", operator("A", "001", 8, true)); code != http.StatusCreated {
		t.Fatalf("create A = %d %q", code, msg)
	}

	if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators", operator("B", "002", 5, false)); code != http.StatusCreated {
		t.Fatalf("create B = %d %q", code, msg)
	}

	code, _, msg := a.do(t, http.MethodPut, "/api/v1/operators/B", operator("B", "002", 5, true))
	if code != http.StatusBadRequest || msg != "Operators with AMFs can have at most 12 PLMNs in total" {
		t.Fatalf("AMF total over 12 = %d %q", code, msg)
	}

	if code, _, msg := a.do(t, http.MethodPut, "/api/v1/operators/B", operator("B", "002", 4, true)); code != http.StatusOK {
		t.Fatalf("AMF total of 12 = %d %q", code, msg)
	}
}
