package ng

import (
	"slices"
	"testing"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
)

var (
	plmnA = db.PLMN{MCC: "001", MNC: "01"}
	plmnB = db.PLMN{MCC: "002", MNC: "002"}
	plmnC = db.PLMN{MCC: "003", MNC: "03"}
	sst1  = SNSSAI{SST: 1}
	sst2  = SNSSAI{SST: 2, SD: "010203"}
)

func TestGlobalGNBIDConversion(t *testing.T) {
	id := db.GlobalGNBID{PLMN: plmnB, ID: 0x3fffff, Length: 22}

	wire, err := globalGNBIDTo(id)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := globalGNBIDFrom(wire); err != nil || got != id {
		t.Fatalf("round trip = %+v, %v", got, err)
	}

	wire.Kind = ngap.RANNodeIDMacroNgENB
	if _, err := globalGNBIDFrom(wire); err == nil {
		t.Fatal("ng-eNB accepted as a gNB")
	}
}

func TestSNSSAIConversion(t *testing.T) {
	for _, s := range []SNSSAI{sst1, sst2} {
		wire, err := snssaiTo(s)
		if err != nil {
			t.Fatal(err)
		}

		if got := snssaiFrom(wire); got != s {
			t.Fatalf("round trip %+v = %+v", s, got)
		}
	}

	if _, err := snssaiTo(SNSSAI{SST: 1, SD: "0102"}); err == nil {
		t.Fatal("2-octet SD accepted")
	}
}

func TestGNBConfigFor(t *testing.T) {
	nodes := []ranSetup{
		{tas: []TA{{TAC: 7, BroadcastPLMNs: []BroadcastPLMN{{PLMN: plmnC, Slices: []SNSSAI{sst1}}, {PLMN: plmnA, Slices: []SNSSAI{sst1}}}}}, drx: ngap.PagingDRXv64},
		{tas: []TA{
			{TAC: 3, BroadcastPLMNs: []BroadcastPLMN{{PLMN: plmnA, Slices: []SNSSAI{sst2}}}},
			{TAC: 7, BroadcastPLMNs: []BroadcastPLMN{{PLMN: plmnA, Slices: []SNSSAI{sst2, sst1}}, {PLMN: plmnB, Slices: []SNSSAI{sst1}}}},
		}, drx: ngap.PagingDRXv128},
		{tas: []TA{{TAC: 9, BroadcastPLMNs: []BroadcastPLMN{{PLMN: plmnC, Slices: []SNSSAI{sst1}}}}}, drx: ngap.PagingDRXv256},
	}

	cfg, ok := gnbConfigFor([]db.PLMN{plmnB, plmnA}, nodes)
	if !ok {
		t.Fatal("no configuration")
	}

	want := gnbConfig{drx: ngap.PagingDRXv128, tas: []TA{
		{TAC: 3, BroadcastPLMNs: []BroadcastPLMN{{PLMN: plmnA, Slices: []SNSSAI{sst2}}}},
		{TAC: 7, BroadcastPLMNs: []BroadcastPLMN{{PLMN: plmnB, Slices: []SNSSAI{sst1}}, {PLMN: plmnA, Slices: []SNSSAI{sst1, sst2}}}},
	}}

	if !cfg.equal(want) {
		t.Fatalf("config = %+v, want %+v", cfg, want)
	}

	if _, ok := gnbConfigFor([]db.PLMN{{MCC: "999", MNC: "99"}}, nodes); ok {
		t.Fatal("configuration without a TA of the operator")
	}
}

func TestMergeAMFConfig(t *testing.T) {
	gA := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 0}
	gA2 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 1}
	gB := GUAMI{PLMN: plmnB, AMFRegionID: 2, AMFSetID: 3, AMFPointer: 0}
	gForeign := GUAMI{PLMN: plmnC, AMFRegionID: 9, AMFSetID: 9, AMFPointer: 9}

	cfg := mergeAMFConfig([]amfPool{
		{plmns: []db.PLMN{plmnA}, served: []GUAMI{gA, gForeign}, support: []PLMNSupport{{PLMN: plmnA, Slices: []SNSSAI{sst1}}, {PLMN: plmnC, Slices: []SNSSAI{sst1}}}},
		{plmns: []db.PLMN{plmnA}, served: []GUAMI{gA, gA2}, support: []PLMNSupport{{PLMN: plmnA, Slices: []SNSSAI{sst2, sst1}}}},
		{plmns: []db.PLMN{plmnB}, served: []GUAMI{gB}, support: []PLMNSupport{{PLMN: plmnB, Slices: []SNSSAI{sst1}}}},
	})

	want := amfConfig{
		served:  []GUAMI{gA, gA2, gB},
		support: []PLMNSupport{{PLMN: plmnA, Slices: []SNSSAI{sst1, sst2}}, {PLMN: plmnB, Slices: []SNSSAI{sst1}}},
	}

	if !cfg.equal(want) {
		t.Fatalf("config = %+v, want %+v", cfg, want)
	}
}

func TestMergeAMFConfigLimits(t *testing.T) {
	var pools []amfPool

	for i := range 300 {
		p := db.PLMN{MCC: "001", MNC: "01"}
		if i < 20 {
			p = db.PLMN{MCC: "001", MNC: string([]byte{'1', byte('0' + i/10), byte('0' + i%10)})}
		}

		pools = append(pools, amfPool{
			plmns:   []db.PLMN{p},
			served:  []GUAMI{{PLMN: p, AMFRegionID: uint8(i), AMFSetID: uint16(i)}},
			support: []PLMNSupport{{PLMN: p, Slices: []SNSSAI{sst1}}},
		})
	}

	cfg := mergeAMFConfig(pools)
	if len(cfg.served) != maxnoofServedGUAMIs || len(cfg.support) != maxnoofPLMNs {
		t.Fatalf("served %d GUAMIs and %d PLMNs, want %d and %d", len(cfg.served), len(cfg.support), maxnoofServedGUAMIs,
			maxnoofPLMNs)
	}
}

func TestServingOperators(t *testing.T) {
	ops := []operatorView{{id: 1, plmns: []db.PLMN{plmnA}}, {id: 2, plmns: []db.PLMN{plmnB}}, {id: 3, plmns: []db.PLMN{plmnC}}}
	tas := []TA{{TAC: 1, BroadcastPLMNs: []BroadcastPLMN{{PLMN: plmnC}, {PLMN: plmnA}}}}

	if got := servingOperators(tas, ops); !slices.Equal(got, []int64{1, 3}) {
		t.Fatalf("serving = %v", got)
	}
}

func TestPagingDRXAndTimeToWait(t *testing.T) {
	if got := pagingDRXFrames(ngap.PagingDRXv256); got != 256 {
		t.Fatalf("v256 = %d frames", got)
	}

	if got := timeToWait[ngap.TimeToWaitV60s].Seconds(); got != 60 {
		t.Fatalf("v60s = %v", got)
	}
}
