package s1

import (
	"testing"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
)

func TestSelectMME(t *testing.T) {
	plmnA := db.PLMN{MCC: "001", MNC: "01"}
	plmnB := db.PLMN{MCC: "001", MNC: "02"}

	idA, err := encodePLMN(plmnA)
	if err != nil {
		t.Fatal(err)
	}

	first, second, third := &MMEAssociation{}, &MMEAssociation{}, &MMEAssociation{}
	cands := []mmeCandidate{
		{assoc: first, capacity: 1, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{plmnA}, GroupIDs: []uint16{1}, MMECodes: []uint8{1}}}},
		{assoc: second, capacity: 3, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{plmnA}, GroupIDs: []uint16{2}, MMECodes: []uint8{2}}}},
		{assoc: third, capacity: 0, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{plmnB}, GroupIDs: []uint16{3}, MMECodes: []uint8{3}}}},
	}

	fixed := func(v int) func(int) int { return func(int) int { return v } }

	tests := []struct {
		name string
		id   ueIdentity
		intN func(int) int
		want *MMEAssociation
		rule string
	}{
		{
			name: "GUMMEI before S-TMSI",
			id: ueIdentity{
				plmn:   plmnA,
				gummei: &s1ap.GUMMEI{PLMNIdentity: idA, MMEGroupID: s1ap.MMEGroupID{0, 2}, MMECode: 2},
				stmsi:  &s1ap.STMSI{MMEC: 1},
			},
			want: second, rule: selectedByGUMMEI,
		},
		{
			name: "GUMMEI with an unknown group falls back to the S-TMSI",
			id: ueIdentity{
				plmn:   plmnA,
				gummei: &s1ap.GUMMEI{PLMNIdentity: idA, MMEGroupID: s1ap.MMEGroupID{0, 9}, MMECode: 2},
				stmsi:  &s1ap.STMSI{MMEC: 1},
			},
			want: first, rule: selectedBySTMSI,
		},
		{
			name: "S-TMSI MMEC served only for another PLMN",
			id:   ueIdentity{plmn: plmnA, stmsi: &s1ap.STMSI{MMEC: 3}},
			intN: fixed(0), want: first, rule: selectedByCapacity,
		},
		{
			name: "capacity, first slot",
			id:   ueIdentity{plmn: plmnA},
			intN: fixed(0), want: first, rule: selectedByCapacity,
		},
		{
			name: "S-TMSI MMEC of an MME with capacity 0",
			id:   ueIdentity{plmn: plmnB, stmsi: &s1ap.STMSI{MMEC: 3}},
			want: third, rule: selectedBySTMSI,
		},
		{
			name: "capacity, last slot",
			id:   ueIdentity{plmn: plmnA},
			intN: fixed(3), want: second, rule: selectedByCapacity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intN := tt.intN
			if intN == nil {
				intN = func(int) int { t.Fatal("selection by capacity"); return 0 }
			}

			got, rule := selectMME(cands, tt.id, intN)
			if got != tt.want || rule != tt.rule {
				t.Fatalf("selectMME = %p by %q, want %p by %q", got, rule, tt.want, tt.rule)
			}
		})
	}

	var total int

	selectMME(cands, ueIdentity{plmn: plmnA}, func(n int) int { total = n; return n - 1 })

	if total != 4 {
		t.Fatalf("capacity draw over %d, want 4", total)
	}

	for pick := range 4 {
		if got, _ := selectMME(cands, ueIdentity{plmn: plmnA}, fixed(pick)); got == third {
			t.Fatalf("draw %d selected an MME with capacity 0", pick)
		}
	}

	zero := []mmeCandidate{{assoc: first}, {assoc: third}}
	if got, rule := selectMME(zero, ueIdentity{plmn: plmnA}, func(int) int { t.Fatal("draw over capacity 0"); return 0 }); got != nil || rule != "" {
		t.Fatal("selected an MME when every capacity is 0")
	}

	if got, _ := selectMME(nil, ueIdentity{plmn: plmnA}, fixed(0)); got != nil {
		t.Fatal("selected an MME from none")
	}
}

func TestUEIDAllocation(t *testing.T) {
	tbl := newUETable()
	n, a := &RANAssociation{}, &MMEAssociation{}

	tbl.nextMMEID = 1<<32 - 2
	tbl.nextENBID = maxENBUES1APID - 1

	u1 := &ueContext{ran: n, enbID: 1, mme: a}
	if err := tbl.add(u1); err != nil {
		t.Fatal(err)
	}

	if u1.ranMMEID != 1<<32-1 || u1.mmeENBID != maxENBUES1APID {
		t.Fatalf("IDs = %d/%d", u1.ranMMEID, u1.mmeENBID)
	}

	tbl.byRANMMEID[0] = u1
	tbl.byMMEENBID[0] = u1

	u2 := &ueContext{ran: n, enbID: 2, mme: a}
	if err := tbl.add(u2); err != nil {
		t.Fatal(err)
	}

	if u2.ranMMEID != 1 || u2.mmeENBID != 1 {
		t.Fatalf("IDs after wrapping past used ones = %d/%d", u2.ranMMEID, u2.mmeENBID)
	}

	delete(tbl.byRANMMEID, 0)
	delete(tbl.byMMEENBID, 0)

	tbl.setMMEID(u2, 77)

	if got := tbl.onMME(a, s1ap.Ptr(s1ap.MMEUES1APID(77)), nil); len(got) != 1 || got[0] != u2 {
		t.Fatalf("lookup by MME UE S1AP ID = %v", got)
	}

	if got := tbl.onRAN(n, s1ap.Ptr(u1.ranMMEID), s1ap.Ptr(s1ap.ENBUES1APID(2))); len(got) != 2 {
		t.Fatalf("lookup by both IDs = %v", got)
	}

	if gone := tbl.remove(u1, u2, u1); len(gone) != 2 {
		t.Fatalf("removed %d", len(gone))
	}

	if len(tbl.byRANMMEID)+len(tbl.byRANENBID)+len(tbl.byMMEENBID)+len(tbl.byMMEID) != 0 {
		t.Fatal("table not empty")
	}
}

func TestUEIDAllocationWithoutMME(t *testing.T) {
	tbl := newUETable()
	n, a := &RANAssociation{}, &MMEAssociation{}

	tbl.nextENBID = maxENBUES1APID

	relayed := &ueContext{ran: n, enbID: 1, mme: a}
	if err := tbl.add(relayed); err != nil {
		t.Fatal(err)
	}

	released := &ueContext{ran: n, enbID: 2}
	if err := tbl.add(released); err != nil {
		t.Fatal(err)
	}

	if relayed.mmeENBID != 0 || released.ranMMEID != relayed.ranMMEID+1 || len(tbl.byMMEENBID) != 1 {
		t.Fatalf("IDs = %d/%d, %d MME-side IDs", released.ranMMEID, relayed.mmeENBID, len(tbl.byMMEENBID))
	}

	if gone := tbl.remove(released); len(gone) != 1 || tbl.byMMEENBID[0] != relayed {
		t.Fatal("removing a UE without an MME freed an MME-side ID")
	}
}
