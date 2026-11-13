package s1

import (
	"reflect"
	"testing"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
)

var (
	p1 = db.PLMN{MCC: "001", MNC: "01"}
	p2 = db.PLMN{MCC: "001", MNC: "02"}
	p3 = db.PLMN{MCC: "310", MNC: "410"}
	p4 = db.PLMN{MCC: "999", MNC: "99"}
)

func TestPLMNRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		plmn db.PLMN
		id   s1ap.PLMNIdentity
	}{
		{p1, s1ap.PLMNIdentity{0x00, 0xf1, 0x10}},
		{p3, s1ap.PLMNIdentity{0x13, 0x00, 0x14}},
	} {
		id, err := encodePLMN(tc.plmn)
		if err != nil || id != tc.id {
			t.Fatalf("encode %s = %x %v, want %x", tc.plmn, id, err, tc.id)
		}

		p, err := decodePLMN(tc.id)
		if err != nil || p != tc.plmn {
			t.Fatalf("decode %x = %s %v, want %s", tc.id, p, err, tc.plmn)
		}
	}

	if _, err := encodePLMN(db.PLMN{MCC: "01", MNC: "01"}); err == nil {
		t.Fatal("encoded a 2-digit MCC")
	}

	if _, err := decodePLMN(s1ap.PLMNIdentity{0xa0, 0xf1, 0x10}); err == nil {
		t.Fatal("decoded a non-digit")
	}
}

func TestGlobalENBIDConversion(t *testing.T) {
	for _, typ := range []db.ENBIDType{db.ENBIDMacro, ENBIDHome, db.ENBIDShortMacro, db.ENBIDLongMacro} {
		in := db.GlobalENBID{PLMN: p3, Type: typ, ID: 42}

		id, err := globalENBIDTo(in)
		if err != nil {
			t.Fatal(err)
		}

		out, err := globalENBIDFrom(id)
		if err != nil || out != in {
			t.Fatalf("round trip %+v = %+v %v", in, out, err)
		}
	}

	if _, err := globalENBIDTo(db.GlobalENBID{PLMN: p1, Type: "pico"}); err == nil {
		t.Fatal("converted an unknown eNB ID type")
	}
}

func TestServingOperators(t *testing.T) {
	ops := []operatorView{{id: 1, plmns: []db.PLMN{p1}}, {id: 2, plmns: []db.PLMN{p2, p3}}, {id: 3, plmns: []db.PLMN{p4}}}

	for name, tc := range map[string]struct {
		tas  []TA
		want []int64
	}{
		"none":        {[]TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{{MCC: "002", MNC: "01"}}}}, nil},
		"one":         {[]TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{p3}}}, []int64{2}},
		"shared cell": {[]TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{p1, p2}}}, []int64{1, 2}},
		"two TAs":     {[]TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{p1}}, {TAC: 2, BroadcastPLMNs: []db.PLMN{p4}}}, []int64{1, 3}},
	} {
		if got := servingOperators(tc.tas, ops); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestENBConfigFor(t *testing.T) {
	nodes := []ranSetup{
		{tas: []TA{{TAC: 7, BroadcastPLMNs: []db.PLMN{p2, p1}}, {TAC: 3, BroadcastPLMNs: []db.PLMN{p4}}}, drx: s1ap.PagingDRXv64},
		{tas: []TA{{TAC: 3, BroadcastPLMNs: []db.PLMN{p1}}}, drx: s1ap.PagingDRXv32},
		{tas: []TA{{TAC: 7, BroadcastPLMNs: []db.PLMN{p1}}}, drx: s1ap.PagingDRXv256},
		{tas: []TA{{TAC: 9, BroadcastPLMNs: []db.PLMN{p4}}}, drx: s1ap.PagingDRXv128},
	}

	for name, tc := range map[string]struct {
		plmns []db.PLMN
		nodes []ranSetup
		want  enbConfig
		ok    bool
	}{
		"no RAN node": {plmns: []db.PLMN{p1}},
		"no shared PLMN": {
			plmns: []db.PLMN{p3}, nodes: nodes,
		},
		"union filtered to the operator, longest DRX of the serving nodes": {
			plmns: []db.PLMN{p1, p2}, nodes: nodes, ok: true,
			want: enbConfig{
				tas: []TA{{TAC: 3, BroadcastPLMNs: []db.PLMN{p1}}, {TAC: 7, BroadcastPLMNs: []db.PLMN{p1, p2}}},
				drx: s1ap.PagingDRXv256,
			},
		},
		"DRX ignores nodes that do not serve the operator": {
			plmns: []db.PLMN{p2}, nodes: nodes, ok: true,
			want: enbConfig{tas: []TA{{TAC: 7, BroadcastPLMNs: []db.PLMN{p2}}}, drx: s1ap.PagingDRXv64},
		},
		"other operator": {
			plmns: []db.PLMN{p4}, nodes: nodes, ok: true,
			want: enbConfig{
				tas: []TA{{TAC: 3, BroadcastPLMNs: []db.PLMN{p4}}, {TAC: 9, BroadcastPLMNs: []db.PLMN{p4}}},
				drx: s1ap.PagingDRXv128,
			},
		},
	} {
		got, ok := enbConfigFor(tc.plmns, tc.nodes)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v %v, want %+v %v", name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestENBConfigForLimits(t *testing.T) {
	var (
		plmns []db.PLMN
		nodes []ranSetup
	)

	for i := range 8 {
		p := db.PLMN{MCC: "001", MNC: string(rune('0'+i)) + "1"}
		plmns = append(plmns, p)
		nodes = append(nodes, ranSetup{tas: []TA{{TAC: 1, BroadcastPLMNs: []db.PLMN{p}}}})
	}

	for tac := range 300 {
		nodes = append(nodes, ranSetup{tas: []TA{{TAC: uint16(tac + 2), BroadcastPLMNs: plmns[:1]}}})
	}

	got, ok := enbConfigFor(plmns, nodes)
	if !ok || len(got.tas) != maxnoofTACs || len(got.tas[0].BroadcastPLMNs) != maxnoofBPLMNs {
		t.Fatalf("got %d TAs, %d PLMNs in the first", len(got.tas), len(got.tas[0].BroadcastPLMNs))
	}
}

func TestMergeServedGUMMEIs(t *testing.T) {
	for name, tc := range map[string]struct {
		pools []mmePool
		want  []ServedGUMMEIs
	}{
		"no MME": {},
		"one MME": {
			pools: []mmePool{{plmns: []db.PLMN{p1}, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{p1}, GroupIDs: []uint16{2}, MMECodes: []uint8{1}}}}},
			want:  []ServedGUMMEIs{{PLMNs: []db.PLMN{p1}, GroupIDs: []uint16{2}, MMECodes: []uint8{1}}},
		},
		"merged across operators, LTE pool only, filtered to the operator's PLMNs": {
			pools: []mmePool{
				{plmns: []db.PLMN{p1}, served: []ServedGUMMEIs{
					{PLMNs: []db.PLMN{p1, p4}, GroupIDs: []uint16{0x8001, 2}, MMECodes: []uint8{3, 1}},
					{PLMNs: []db.PLMN{p1}, GroupIDs: []uint16{0xff00}, MMECodes: []uint8{9}},
				}},
				{plmns: []db.PLMN{p1}, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{p1}, GroupIDs: []uint16{2}, MMECodes: []uint8{2}}}},
				{plmns: []db.PLMN{p3}, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{p3}, GroupIDs: []uint16{5}, MMECodes: []uint8{1}}}},
				{plmns: []db.PLMN{p2}, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{p4}, GroupIDs: []uint16{6}, MMECodes: []uint8{6}}}},
				{plmns: []db.PLMN{p2}},
			},
			want: []ServedGUMMEIs{{PLMNs: []db.PLMN{p1, p3}, GroupIDs: []uint16{2, 5, 0x8001}, MMECodes: []uint8{1, 2, 3}}},
		},
	} {
		if got := mergeServedGUMMEIs(tc.pools); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", name, got, tc.want)
		}
	}
}

func TestMergeServedGUMMEIsPLMNLimit(t *testing.T) {
	var pools []mmePool

	for i := range 40 {
		p := db.PLMN{MCC: "001", MNC: string([]byte{'0' + byte(i/10), '0' + byte(i%10)})}
		pools = append(pools, mmePool{plmns: []db.PLMN{p}, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{p}, GroupIDs: []uint16{1}, MMECodes: []uint8{1}}}})
	}

	got := mergeServedGUMMEIs(pools)
	if len(got) != 1 || len(got[0].PLMNs) != maxnoofPLMNsPerMME {
		t.Fatalf("got %+v", got)
	}

	if _, err := gummeisTo(got); err != nil {
		t.Fatal(err)
	}
}

func TestPagingDRXAndTimeToWait(t *testing.T) {
	if got := []int{
		pagingDRXFrames(s1ap.PagingDRXv32), pagingDRXFrames(s1ap.PagingDRXv64), pagingDRXFrames(s1ap.PagingDRXv128),
		pagingDRXFrames(s1ap.PagingDRXv256),
	}; !reflect.DeepEqual(got, []int{32, 64, 128, 256}) {
		t.Fatalf("Paging DRX = %v", got)
	}

	if len(timeToWait) != 6 || timeToWait[s1ap.TimeToWaitV60s].Seconds() != 60 {
		t.Fatalf("Time to Wait = %v", timeToWait)
	}
}
