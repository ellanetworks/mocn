package ng

import (
	"testing"

	"github.com/ellanetworks/core/ngap"
)

func TestSelectAMF(t *testing.T) {
	a1, a2, a3, a4 := &AMFAssociation{}, &AMFAssociation{}, &AMFAssociation{}, &AMFAssociation{}
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
		want  *AMFAssociation
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

func TestUEIDAllocation(t *testing.T) {
	ues := newUETable()
	ues.nextAMFID, ues.nextRANID = maxAMFUENGAPID-1, maxRANUENGAPID-1

	amf := &AMFAssociation{}
	ran := &RANAssociation{}

	var got []*ueContext

	for i := range 3 {
		u := &ueContext{ran: ran, ranID: ngap.RANUENGAPID(i), amf: amf}
		if err := ues.add(u); err != nil {
			t.Fatal(err)
		}

		got = append(got, u)
	}

	if got[0].ranAMFID != maxAMFUENGAPID || got[1].ranAMFID != 0 || got[2].ranAMFID != 1 {
		t.Fatalf("AMF UE NGAP IDs toward the radio = %d, %d, %d", got[0].ranAMFID, got[1].ranAMFID, got[2].ranAMFID)
	}

	if got[0].amfRANID != maxRANUENGAPID || got[1].amfRANID != 0 || got[2].amfRANID != 1 {
		t.Fatalf("RAN UE NGAP IDs toward the AMF = %d, %d, %d", got[0].amfRANID, got[1].amfRANID, got[2].amfRANID)
	}

	ues.setAMFID(got[0], 5)
	ues.setAMFID(got[0], 6)

	if ues.byAMFID[amfUEKey{amf, 5}] != nil || ues.byAMFID[amfUEKey{amf, 6}] != got[0] {
		t.Fatal("AMF UE NGAP ID not replaced")
	}

	if gone := ues.remove(got...); len(gone) != 3 || len(ues.byRANAMFID)+len(ues.byRANID)+len(ues.byAMFRANID)+len(ues.byAMFID) != 0 {
		t.Fatalf("left %+v", ues)
	}
}
