package ng

import (
	"testing"

	"github.com/ellanetworks/core/ngap"
)

func TestSelectAMF(t *testing.T) {
	t.Parallel()

	a1, a2, a3, a4 := &amfAssoc{}, &amfAssoc{}, &amfAssoc{}, &amfAssoc{}
	g1 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 1}
	g2 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 2}
	g3 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 2, AMFPointer: 1}
	g4 := GUAMI{PLMN: plmnB, AMFRegionID: 1, AMFSetID: 3, AMFPointer: 1}
	support := func(plmn ...PLMNSupport) []PLMNSupport { return plmn }
	cands := []amfCandidate{
		{assoc: a1, served: []GUAMI{g1}, capacity: 0, support: support(PLMNSupport{PLMN: plmnA})},
		{assoc: a2, served: []GUAMI{g2}, capacity: 10, support: support(PLMNSupport{PLMN: plmnA})},
		{assoc: a3, served: []GUAMI{g3}, capacity: 30, support: support(PLMNSupport{PLMN: plmnA})},
		{assoc: a4, served: []GUAMI{g4}, capacity: 255, support: support(PLMNSupport{PLMN: plmnB})},
	}
	first := func(int) int { return 0 }
	last := func(n int) int { return n - 1 }

	for name, tc := range map[string]struct {
		cands []amfCandidate
		id    ueIdentity
		intN  func(int) int
		want  *amfAssoc
		rule  string
	}{
		"GUAMI, capacity 0":           {cands, ueIdentity{plmn: plmnA, guami: &g1}, first, a1, selectedByGUAMI},
		"GUAMI over 5G-S-TMSI":        {cands, ueIdentity{plmn: plmnA, guami: &g2, stmsi: &fiveGSTMSI{setID: 2, pointer: 1}}, first, a2, selectedByGUAMI},
		"unknown GUAMI, same set":     {cands, ueIdentity{plmn: plmnA, guami: &GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 9}}, last, a2, selectedByAMFSet},
		"5G-S-TMSI":                   {cands, ueIdentity{plmn: plmnA, stmsi: &fiveGSTMSI{setID: 2, pointer: 1}}, last, a3, selectedBySTMSI},
		"5G-S-TMSI, unknown pointer":  {cands, ueIdentity{plmn: plmnA, stmsi: &fiveGSTMSI{setID: 1, pointer: 7}}, last, a2, selectedByAMFSet},
		"5G-S-TMSI, unknown set":      {cands, ueIdentity{plmn: plmnA, stmsi: &fiveGSTMSI{setID: 9, pointer: 1}}, first, a2, selectedByCapacity},
		"capacity, first":             {cands, ueIdentity{plmn: plmnA}, first, a2, selectedByCapacity},
		"capacity, last":              {cands, ueIdentity{plmn: plmnA}, last, a3, selectedByCapacity},
		"capacity, other PLMN":        {cands, ueIdentity{plmn: plmnB}, first, a4, selectedByCapacity},
		"reroute to an AMF Set":       {cands, ueIdentity{plmn: plmnA, set: new(uint16(2))}, first, a3, selectedByAMFSet},
		"reroute to an unknown set":   {cands, ueIdentity{plmn: plmnA, set: new(uint16(7))}, first, nil, ""},
		"only capacity 0":             {cands[:1], ueIdentity{plmn: plmnA}, first, nil, ""},
		"no candidates":               {nil, ueIdentity{plmn: plmnA}, first, nil, ""},
		"unavailable GUAMI, same set": {[]amfCandidate{{assoc: a1, served: []GUAMI{g1}, unavailable: []GUAMI{g1}, capacity: 255}, cands[1]}, ueIdentity{plmn: plmnA, guami: &g1}, first, a2, selectedByAMFSet},
		"GUAMI served by two AMFs, weighted first": {[]amfCandidate{
			{assoc: a1, served: []GUAMI{g1}, capacity: 10}, {assoc: a2, served: []GUAMI{g1}, capacity: 30},
		}, ueIdentity{plmn: plmnA, guami: &g1}, func(int) int { return 9 }, a1, selectedByGUAMI},
		"GUAMI served by two AMFs, weighted second": {[]amfCandidate{
			{assoc: a1, served: []GUAMI{g1}, capacity: 10}, {assoc: a2, served: []GUAMI{g1}, capacity: 30},
		}, ueIdentity{plmn: plmnA, guami: &g1}, func(int) int { return 10 }, a2, selectedByGUAMI},
		"5G-S-TMSI served by two AMFs, weighted": {[]amfCandidate{
			{assoc: a1, served: []GUAMI{g1}, capacity: 10}, {assoc: a2, served: []GUAMI{g1}, capacity: 30},
		}, ueIdentity{plmn: plmnA, stmsi: &fiveGSTMSI{setID: 1, pointer: 1}}, last, a2, selectedBySTMSI},
		"GUAMI served by two AMFs of weight 0": {[]amfCandidate{
			{assoc: a1, served: []GUAMI{g1}}, {assoc: a2, served: []GUAMI{g1}},
		}, ueIdentity{plmn: plmnA, guami: &g1}, last, a2, selectedByGUAMI},
		"5G-S-TMSI of another PLMN": {[]amfCandidate{cands[3]}, ueIdentity{plmn: plmnA, stmsi: &fiveGSTMSI{setID: 3, pointer: 1}}, first, nil, ""},
		"AMF Set of another PLMN":   {[]amfCandidate{cands[3]}, ueIdentity{plmn: plmnA, set: new(uint16(3))}, first, nil, ""},
		"AMF Set of another region": {[]amfCandidate{cands[1], {assoc: a3, served: []GUAMI{{PLMN: plmnA, AMFRegionID: 9, AMFSetID: 1, AMFPointer: 3}}, capacity: 255}}, ueIdentity{plmn: plmnA, guami: &GUAMI{PLMN: plmnA, AMFRegionID: 9, AMFSetID: 1, AMFPointer: 7}}, first, a3, selectedByAMFSet},
		"unavailable AMF, capacity": {[]amfCandidate{
			{assoc: a1, served: []GUAMI{g1}, unavailable: []GUAMI{g1}, capacity: 255, support: support(PLMNSupport{PLMN: plmnA})},
			cands[2],
		}, ueIdentity{plmn: plmnA}, first, a3, selectedByCapacity},
	} {
		t.Run(name, func(t *testing.T) {
			got, rule := selectAMF(tc.cands, tc.id, tc.intN)
			if got != tc.want || rule != tc.rule {
				t.Fatalf("selected %p by %q, want %p by %q", got, rule, tc.want, tc.rule)
			}
		})
	}
}

func TestRebindAMF(t *testing.T) {
	t.Parallel()

	a1, a2, a3, lost := &amfAssoc{}, &amfAssoc{}, &amfAssoc{}, &amfAssoc{}
	g1 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 1}
	g2 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 2}
	g3 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 2, AMFPointer: 1}
	first := func(int) int { return 0 }
	set1 := []amfSet{setOf(g1)}

	for name, tc := range map[string]struct {
		cands []amfCandidate
		b     binding
		want  *amfAssoc
	}{
		"GUAMI available": {[]amfCandidate{
			{assoc: a1, served: []GUAMI{g1}, capacity: 1}, {assoc: a2, served: []GUAMI{g2}, capacity: 255},
		}, binding{current: lost, guami: &g1, sets: set1}, a1},
		"GUAMI unavailable, target AMF": {[]amfCandidate{
			{assoc: a1, name: amfName{visible: "amf1"}, served: []GUAMI{g1}, unavailable: []GUAMI{g1}, backups: map[GUAMI]amfName{g1: {utf8: "amf3"}}, capacity: 255},
			{assoc: a2, name: amfName{visible: "amf2"}, served: []GUAMI{g2}, capacity: 255},
			{assoc: a3, name: amfName{visible: "amf-3", utf8: "amf3"}, served: []GUAMI{g3}, capacity: 1},
		}, binding{current: a1, guami: &g1, sets: set1}, a3},
		"failed AMF, backup AMF": {[]amfCandidate{
			{assoc: a2, name: amfName{visible: "amf2"}, served: []GUAMI{g2}, capacity: 255},
			{assoc: a3, name: amfName{visible: "amf3"}, served: []GUAMI{g3}, capacity: 1},
		}, binding{current: lost, guami: &g1, backup: amfName{visible: "amf3"}, sets: set1}, a3},
		"GUAMI unavailable, AMF set": {[]amfCandidate{
			{assoc: a1, served: []GUAMI{g1}, unavailable: []GUAMI{g1}, capacity: 255},
			{assoc: a2, served: []GUAMI{g2}, capacity: 1},
			{assoc: a3, served: []GUAMI{g3}, capacity: 255},
		}, binding{current: a1, guami: &g1, sets: set1}, a2},
		"no GUAMI, same AMF": {[]amfCandidate{
			{assoc: a2, served: []GUAMI{g2}, capacity: 255}, {assoc: a1, served: []GUAMI{g1}, capacity: 1},
		}, binding{current: a1, sets: set1}, a1},
		"no GUAMI, AMF set": {[]amfCandidate{
			{assoc: a3, served: []GUAMI{g3}, capacity: 255}, {assoc: a2, served: []GUAMI{g2}, capacity: 1},
		}, binding{current: lost, sets: set1}, a2},
		"no AMF of the set": {[]amfCandidate{
			{assoc: a3, served: []GUAMI{g3}, capacity: 255},
		}, binding{current: lost, guami: &g1, sets: set1}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := rebindAMF(tc.cands, tc.b, first); got != tc.want {
				t.Fatalf("selected %p, want %p", got, tc.want)
			}
		})
	}
}

func TestSelectAMFByRequestedNSSAIAndOverload(t *testing.T) {
	t.Parallel()

	a1, a2, a3 := &amfAssoc{}, &amfAssoc{}, &amfAssoc{}
	s1, s2 := SNSSAI{SST: 1}, SNSSAI{SST: 2, SD: "010203"}
	g1 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 1}
	g2 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 2}
	g3 := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 3}
	cands := []amfCandidate{
		{assoc: a1, served: []GUAMI{g1}, capacity: 255, support: []PLMNSupport{{PLMN: plmnA, Slices: []SNSSAI{s1}}}},
		{assoc: a2, served: []GUAMI{g2}, capacity: 1, support: []PLMNSupport{{PLMN: plmnA, Slices: []SNSSAI{s1, s2}}}},
		{assoc: a3, served: []GUAMI{g3}, capacity: 1, support: []PLMNSupport{{PLMN: plmnA, Slices: []SNSSAI{s1, s2}}}},
	}
	first := func(int) int { return 0 }
	overloaded := func(i int) []amfCandidate {
		out := append([]amfCandidate(nil), cands...)
		out[i].overloaded = true

		return out
	}

	for name, tc := range map[string]struct {
		cands []amfCandidate
		id    ueIdentity
		want  *amfAssoc
		rule  string
	}{
		"capacity, no Requested NSSAI":       {cands, ueIdentity{plmn: plmnA}, a1, selectedByCapacity},
		"capacity, Requested NSSAI covered":  {cands, ueIdentity{plmn: plmnA, nssai: []SNSSAI{s1, s2}}, a2, selectedByCapacity},
		"capacity, Requested NSSAI unserved": {cands, ueIdentity{plmn: plmnA, nssai: []SNSSAI{{SST: 9}}}, a1, selectedByCapacity},
		"set, Requested NSSAI covered": {cands, ueIdentity{
			plmn: plmnA, guami: &GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 9}, nssai: []SNSSAI{s2},
		}, a2, selectedByAMFSet},
		"GUAMI identifies an AMF regardless of the NSSAI": {cands, ueIdentity{plmn: plmnA, guami: &g1, nssai: []SNSSAI{s2}}, a1, selectedByGUAMI},
		"capacity, overloaded AMF avoided":                {overloaded(0), ueIdentity{plmn: plmnA}, a2, selectedByCapacity},
		"capacity, covering AMF without overload":         {overloaded(1), ueIdentity{plmn: plmnA, nssai: []SNSSAI{s2}}, a3, selectedByCapacity},
		"set fallback ignores overload":                   {overloaded(0), ueIdentity{plmn: plmnA, stmsi: &fiveGSTMSI{setID: 1, pointer: 9}}, a1, selectedByAMFSet},
		"all overloaded":                                  {[]amfCandidate{overloaded(0)[0]}, ueIdentity{plmn: plmnA}, a1, selectedByCapacity},
	} {
		t.Run(name, func(t *testing.T) {
			got, rule := selectAMF(tc.cands, tc.id, first)
			if got != tc.want || rule != tc.rule {
				t.Fatalf("selected %p by %q, want %p by %q", got, rule, tc.want, tc.rule)
			}
		})
	}
}

func TestSelectAMFByGUAMIType(t *testing.T) {
	t.Parallel()

	a1, a2 := &amfAssoc{}, &amfAssoc{}
	g := GUAMI{PLMN: plmnA, AMFRegionID: 1, AMFSetID: 1, AMFPointer: 1}
	cands := []amfCandidate{
		{assoc: a1, served: []GUAMI{g}, types: map[GUAMI]ngap.GUAMIType{g: ngap.GUAMITypeNative}, capacity: 1},
		{assoc: a2, served: []GUAMI{g}, types: map[GUAMI]ngap.GUAMIType{g: ngap.GUAMITypeMapped}, capacity: 1},
	}
	first := func(int) int { return 0 }

	for typ, want := range map[ngap.GUAMIType]*amfAssoc{ngap.GUAMITypeNative: a1, ngap.GUAMITypeMapped: a2} {
		if got, rule := selectAMF(cands, ueIdentity{plmn: plmnA, guami: &g, guamiType: &typ}, first); got != want ||
			rule != selectedByGUAMI {
			t.Fatalf("GUAMI type %d selected %p by %q, want %p", typ, got, rule, want)
		}
	}

	if got, _ := selectAMF(cands, ueIdentity{plmn: plmnA, guami: &g}, first); got != a1 {
		t.Fatal("GUAMI without a type not matched")
	}
}
