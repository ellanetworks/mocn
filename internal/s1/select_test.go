package s1

import (
	"testing"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
)

func TestSelectMME(t *testing.T) {
	t.Parallel()

	plmnA := db.PLMN{MCC: "001", MNC: "01"}
	plmnB := db.PLMN{MCC: "001", MNC: "02"}

	idA, err := plmnid.Encode[s1ap.PLMNIdentity](plmnA)
	if err != nil {
		t.Fatal(err)
	}

	first, second, third := &mmeAssoc{}, &mmeAssoc{}, &mmeAssoc{}
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
		want *mmeAssoc
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

func TestOverloadedMMEsAvoidedByCapacitySelection(t *testing.T) {
	t.Parallel()

	plmn := db.PLMN{MCC: "001", MNC: "01"}
	busy, idle := &mmeAssoc{}, &mmeAssoc{}
	servedBy := func(code uint8) []ServedGUMMEIs {
		return []ServedGUMMEIs{{PLMNs: []db.PLMN{plmn}, GroupIDs: []uint16{1}, MMECodes: []uint8{code}}}
	}

	cands := []mmeCandidate{
		{assoc: busy, capacity: 255, served: servedBy(1), overloaded: true},
		{assoc: idle, capacity: 1, served: servedBy(2)},
	}

	first := func(int) int { return 0 }

	if got, rule := selectMME(cands, ueIdentity{plmn: plmn}, first); got != idle || rule != selectedByCapacity {
		t.Fatalf("selected %p by %s, want the MME without overload", got, rule)
	}

	if got, _ := selectMME(cands, ueIdentity{plmn: plmn, stmsi: &s1ap.STMSI{MMEC: 1}}, first); got != busy {
		t.Fatal("an overloaded MME identified by the S-TMSI was not selected")
	}

	cands[1].overloaded = true
	if got, _ := selectMME(cands, ueIdentity{plmn: plmn}, first); got != busy {
		t.Fatal("no MME selected when all are overloaded")
	}

	cands[1].overloaded, cands[1].capacity = false, 0
	if got, _ := selectMME(cands, ueIdentity{plmn: plmn}, first); got != busy {
		t.Fatal("no MME selected when the only MME without overload has no capacity")
	}
}

func TestRerouteTarget(t *testing.T) {
	t.Parallel()

	plmn := db.PLMN{MCC: "001", MNC: "01"}

	id, err := plmnid.Encode[s1ap.PLMNIdentity](plmn)
	if err != nil {
		t.Fatal(err)
	}

	a, b, c := &mmeAssoc{}, &mmeAssoc{}, &mmeAssoc{}
	cands := []mmeCandidate{
		{assoc: a, capacity: 255, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{plmn}, GroupIDs: []uint16{1}, MMECodes: []uint8{1}}}},
		{assoc: b, capacity: 255, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{plmn}, GroupIDs: []uint16{2}, MMECodes: []uint8{2}}}},
		{assoc: c, capacity: 1, served: []ServedGUMMEIs{{PLMNs: []db.PLMN{plmn}, GroupIDs: []uint16{2, 3}, MMECodes: []uint8{3}}}},
	}

	first := func(int) int { return 0 }

	if got := rerouteTarget(cands, plmn, 2, nil, first); got != b {
		t.Fatal("no MME of the MME group selected by capacity")
	}

	guti := &s1ap.AdditionalGUTI{GUMMEI: s1ap.GUMMEI{PLMNIdentity: id, MMEGroupID: s1ap.MMEGroupID{0, 3}, MMECode: 3}}
	if got := rerouteTarget(cands, plmn, 2, guti, first); got != c {
		t.Fatal("the MME of the MME group identified by the Additional GUTI not selected")
	}

	guti.GUMMEI.MMEGroupID, guti.GUMMEI.MMECode = s1ap.MMEGroupID{0, 1}, 1
	if got := rerouteTarget(cands, plmn, 2, guti, first); got != b {
		t.Fatal("an MME outside the MME group selected by the Additional GUTI")
	}

	if got := rerouteTarget(cands, db.PLMN{MCC: "001", MNC: "02"}, 2, nil, first); got != nil {
		t.Fatal("an MME of another PLMN selected")
	}
}
