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
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/s1"
)

type fakeNG struct {
	amfs  map[string]ng.AMFStatus
	nodes []ng.RANNode
	ues   []ng.UE
}

func (f fakeNG) AMFStatus(_ int64, tnl []string) (ng.AMFStatus, bool) {
	st, ok := f.amfs[strings.Join(tnl, ",")]
	return st, ok
}

func (f fakeNG) RANNodes() []ng.RANNode {
	return slices.Clone(f.nodes)
}

func (f fakeNG) UEs() []ng.UE {
	return slices.Clone(f.ues)
}

func newNGTestAPI(t *testing.T, s1View api.S1View, ngView api.NGView) *testAPI {
	t.Helper()

	store := openStore(t)
	a := &testAPI{store: store, now: testNow}
	cfg := testConfig(t, store)
	cfg.S1, cfg.NG = s1View, ngView
	a.handler = api.NewHandler(cfg)
	a.token = initialize(t, a.handler)

	return a
}

func TestOperatorAMFStatus(t *testing.T) {
	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	capacity, reduction := uint8(10), uint8(40)
	plmn := db.PLMN{MCC: "001", MNC: "002"}
	action := ng.OverloadRejectRRCCRSignalling
	g1 := ng.GUAMI{PLMN: plmn, AMFRegionID: 2, AMFSetID: 1, AMFPointer: 3}

	a := newNGTestAPI(t, nil, fakeNG{amfs: map[string]ng.AMFStatus{
		"2001:db8::1": {
			State: ng.AMFUp, Since: since, PrimaryTNLAddress: "2001:db8::1", AMFName: "amf1",
			ServedGUAMIs: []ng.GUAMI{g1}, UnavailableGUAMIs: []ng.GUAMI{g1}, RelativeAMFCapacity: &capacity,
			PLMNSupport: []ng.PLMNSupport{{PLMN: plmn, Slices: []ng.SNSSAI{{SST: 1}, {SST: 2, SD: "0a0b0c"}}}},
			Overload: &ng.Overload{
				Action: &action, TrafficLoadReductionIndication: &reduction,
				Slices: []ng.SliceOverload{{SNSSAIs: []ng.SNSSAI{{SST: 1}}}},
			},
		},
	}, ues: []ng.UE{
		{OperatorID: 1, AMFTNLAddresses: []string{"2001:db8::1"}, RadioGlobalGNBID: db.GlobalGNBID{PLMN: plmn, ID: 1, Length: 22}},
		{OperatorID: 1, AMFTNLAddresses: []string{"2001:db8::2"}, RadioGlobalGNBID: db.GlobalGNBID{PLMN: plmn, ID: 1, Length: 22}},
	}})

	if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators", operatorA); code != http.StatusCreated {
		t.Fatalf("create = %d %q", code, msg)
	}

	guami := api.GUAMI{PLMN: api.PLMN{MCC: "001", MNC: "002"}, AMFRegionID: 2, AMFSetID: 1, AMFPointer: 3}
	want := []api.AMF{{TNLAddresses: []string{"2001:db8::1"}, Status: &api.AMFStatus{
		State: ng.AMFUp, Since: since, PrimaryTNLAddress: "2001:db8::1", AMFName: "amf1",
		ServedGUAMIs: []api.GUAMI{guami}, UnavailableGUAMIs: []api.GUAMI{guami}, RelativeAMFCapacity: &capacity,
		PLMNSupport: []api.PLMNSupport{{PLMN: api.PLMN{MCC: "001", MNC: "002"}, SNSSAIs: []api.SNSSAI{{SST: 1}, {SST: 2, SD: "0a0b0c"}}}},
		Overload: &api.AMFOverload{
			OverloadAction: &action, TrafficLoadReductionIndication: &reduction,
			Slices: []api.SliceOverload{{SNSSAIs: []api.SNSSAI{{SST: 1}}}},
		},
		UEConnections: 1,
	}}}

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/operators/Operator%20A", "")

	got := decode[api.Operator](t, result)
	if code != http.StatusOK || !reflect.DeepEqual(got.AMFs, want) || got.UEConnections == nil || *got.UEConnections != 1 {
		t.Fatalf("GET = %d %s", code, result)
	}

	if !reflect.DeepEqual(got.UEConnectionsByRadio, []api.RadioUEConnections{{Radio: "001-002:gnb22:1", UEConnections: 2}}) {
		t.Fatalf("ue_connections_by_radio = %+v", got.UEConnectionsByRadio)
	}

	for _, field := range []string{
		`"served_guamis":[{"plmn":{"mcc":"001","mnc":"002"},"amf_region_id":2,"amf_set_id":1,"amf_pointer":3}]`,
		`"plmn_support":[{"plmn":{"mcc":"001","mnc":"002"},"snssais":[{"sst":1},{"sst":2,"sd":"0a0b0c"}]}]`,
		`"overload":{"overload_action":"reject_rrc_cr_signalling","traffic_load_reduction_indication":40,"slices":[{"snssais":[{"sst":1}]}]}`,
	} {
		if !strings.Contains(string(result), field) {
			t.Errorf("%s missing from %s", field, result)
		}
	}
}

func TestRadiosAndUEConnectionsOverBothInterfaces(t *testing.T) {
	enbSince := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	gnbSince := enbSince.Add(time.Minute)
	plmn := db.PLMN{MCC: "001", MNC: "01"}
	enb := db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 411}
	gnb := db.GlobalGNBID{PLMN: plmn, ID: 412, Length: 24}
	amfID := uint64(1 << 39)

	a := newNGTestAPI(t,
		fakeS1{nodes: []s1.RANNode{{
			GlobalENBID: &enb, Name: "enb1", State: s1.RANNodeUp, ConnectedSince: enbSince,
			SupportedTAs: []s1.TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{plmn}}}, TNLAddresses: []string{"10.0.0.5"},
		}}, ues: []s1.UE{{RadioGlobalENBID: enb, OperatorID: 1, OperatorName: "Operator A", PLMN: plmn, Since: enbSince}}},
		fakeNG{nodes: []ng.RANNode{{
			GlobalGNBID: &gnb, Name: "gnb1", State: ng.RANNodeUp, ConnectedSince: gnbSince, DefaultPagingDRX: 128,
			SupportedTAs: []ng.TA{{TAC: 0x10203, BroadcastPLMNs: []ng.BroadcastPLMN{{PLMN: plmn, Slices: []ng.SNSSAI{{SST: 1}}}}}},
			TNLAddresses: []string{"10.0.0.6"},
		}}, ues: []ng.UE{{
			RadioGlobalGNBID: gnb, RadioRANUENGAPID: 7, RadioAMFUENGAPID: 9, AMFTNLAddresses: []string{"10.0.1.1"},
			AMFRANUENGAPID: 3, AMFUENGAPID: &amfID, OperatorID: 1, OperatorName: "Operator A", PLMN: plmn, Since: gnbSince,
			TAI: &ng.TAI{PLMN: plmn, TAC: 0x10203}, NRCGI: &ng.NRCGI{PLMN: plmn, CellID: 0x123456789},
		}}})

	if code, _, msg := a.do(t, http.MethodPost, "/api/v1/operators",
		`{"name": "Operator A", "plmns": [{"mcc": "001", "mnc": "01"}]}`); code != http.StatusCreated {
		t.Fatalf("create = %d %q", code, msg)
	}

	code, result, _ := a.do(t, http.MethodGet, "/api/v1/radios", "")

	got := decode[api.ListRadiosResponse](t, result)
	if code != http.StatusOK || len(got.Items) != 2 {
		t.Fatalf("GET = %d %s", code, result)
	}

	want := api.Radio{
		Ref: "001-01:gnb24:412", Interface: "ng",
		GlobalGNBID: &api.GlobalGNBID{PLMN: api.PLMN{MCC: "001", MNC: "01"}, ID: 412, Length: 24},
		RANNodeName: "gnb1", DefaultPagingDRX: 128, State: "up", ConnectedSince: gnbSince,
		SupportedTAs: []api.TA{{TAC: 0x10203, BroadcastPLMNs: []api.PLMN{{MCC: "001", MNC: "01"}}}},
		TNLAddresses: []string{"10.0.0.6"}, UEConnections: 1,
	}

	if got.Items[0].Interface != "s1" || !reflect.DeepEqual(got.Items[1], want) {
		t.Fatalf("radios = %+v", got.Items)
	}

	want.UEConnectionsByOperator = []api.OperatorUEConnections{{Operator: "Operator A", UEConnections: 1}}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/radios/001-01:gnb24:412", "")
	if one := decode[api.Radio](t, result); code != http.StatusOK || !reflect.DeepEqual(one, want) {
		t.Fatalf("GET one = %d %s", code, result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/ue-connections?radio=001-01:gnb24:412", "")

	conns := decode[api.ListUEConnectionsResponse](t, result)
	wantConn := api.UEConnection{
		Interface: "ng",
		Radio:     api.UEConnectionRadio{Ref: "001-01:gnb24:412", RANUENGAPID: new(uint32(7)), AMFUENGAPID: new(uint64(9))},
		AMF:       &api.UEConnectionAMF{TNLAddresses: []string{"10.0.1.1"}, RANUENGAPID: 3, AMFUENGAPID: &amfID},
		Operator:  "Operator A", PLMN: api.PLMN{MCC: "001", MNC: "01"}, Since: gnbSince,
		TAI:   &api.TAI{PLMN: api.PLMN{MCC: "001", MNC: "01"}, TAC: 0x10203},
		NRCGI: &api.NRCGI{PLMN: api.PLMN{MCC: "001", MNC: "01"}, CellID: 0x123456789},
	}

	if code != http.StatusOK || len(conns.Items) != 1 || !reflect.DeepEqual(conns.Items[0], wantConn) {
		t.Fatalf("GET UE connections = %d %s", code, result)
	}

	if strings.Contains(string(result), `"mme"`) || strings.Contains(string(result), `"ecgi"`) ||
		!strings.Contains(string(result), `"amf_ue_ngap_id":549755813888`) {
		t.Fatalf("GET UE connections = %s", result)
	}

	code, result, _ = a.do(t, http.MethodGet, "/api/v1/ue-connections", "")
	if all := decode[api.ListUEConnectionsResponse](t, result); code != http.StatusOK || len(all.Items) != 2 ||
		all.Items[0].Interface != "s1" || all.Items[1].Interface != "ng" {
		t.Fatalf("GET all UE connections = %d %s", code, result)
	}
}
