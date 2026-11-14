package uetable

import (
	"errors"
	"testing"
)

type (
	radio struct{ _ byte }
	core  struct{ _ byte }
	ext   struct{}
)

const (
	maxENBID = 1<<24 - 1
	maxMMEID = 1<<32 - 1
	maxRANID = 1<<32 - 1
	maxAMFID = 1<<40 - 1
)

var errFull = errors.New("no free UE ID")

func ptr[T any](v T) *T {
	return &v
}

func TestUEIDAllocation(t *testing.T) {
	t.Parallel()

	tbl := New[*radio, *core, uint32, uint32, ext](maxENBID, maxMMEID, errFull)
	n, a := &radio{}, &core{}

	tbl.nextCoreID = 1<<32 - 2
	tbl.nextRANID = maxENBID - 1

	u1 := &UE[*radio, *core, uint32, uint32, ext]{RAN: n, RANID: 1, Core: a}
	if err := tbl.Add(u1); err != nil {
		t.Fatal(err)
	}

	if u1.RANCoreID != 1<<32-1 || u1.CoreRANID != maxENBID {
		t.Fatalf("IDs = %d/%d", u1.RANCoreID, u1.CoreRANID)
	}

	tbl.byRANCoreID[0] = u1
	tbl.byCoreRANID[0] = u1

	u2 := &UE[*radio, *core, uint32, uint32, ext]{RAN: n, RANID: 2, Core: a}
	if err := tbl.Add(u2); err != nil {
		t.Fatal(err)
	}

	if u2.RANCoreID != 1 || u2.CoreRANID != 1 {
		t.Fatalf("IDs after wrapping past used ones = %d/%d", u2.RANCoreID, u2.CoreRANID)
	}

	delete(tbl.byRANCoreID, 0)
	delete(tbl.byCoreRANID, 0)

	tbl.SetCoreID(u2, 77)

	if got := tbl.OnCore(a, ptr(uint32(77)), nil); len(got) != 1 || got[0] != u2 {
		t.Fatalf("lookup by MME UE S1AP ID = %v", got)
	}

	if got := tbl.OnRAN(n, ptr(u1.RANCoreID), ptr(uint32(2))); len(got) != 2 {
		t.Fatalf("lookup by both IDs = %v", got)
	}

	if gone := tbl.Remove(u1, u2, u1); len(gone) != 2 {
		t.Fatalf("removed %d", len(gone))
	}

	if len(tbl.byRANCoreID)+len(tbl.byRANID)+len(tbl.byCoreRANID)+len(tbl.byCoreID) != 0 {
		t.Fatal("table not empty")
	}
}

func TestUEIDAllocationWithoutCore(t *testing.T) {
	t.Parallel()

	tbl := New[*radio, *core, uint32, uint32, ext](maxENBID, maxMMEID, errFull)
	n, a := &radio{}, &core{}

	tbl.nextRANID = maxENBID

	relayed := &UE[*radio, *core, uint32, uint32, ext]{RAN: n, RANID: 1, Core: a}
	if err := tbl.Add(relayed); err != nil {
		t.Fatal(err)
	}

	released := &UE[*radio, *core, uint32, uint32, ext]{RAN: n, RANID: 2}
	if err := tbl.Add(released); err != nil {
		t.Fatal(err)
	}

	if relayed.CoreRANID != 0 || released.RANCoreID != relayed.RANCoreID+1 || len(tbl.byCoreRANID) != 1 {
		t.Fatalf("IDs = %d/%d, %d MME-side IDs", released.RANCoreID, relayed.CoreRANID, len(tbl.byCoreRANID))
	}

	if gone := tbl.Remove(released); len(gone) != 1 || tbl.byCoreRANID[0] != relayed {
		t.Fatal("removing a UE without an MME freed an MME-side ID")
	}
}

func TestUEIDAllocationWraps(t *testing.T) {
	t.Parallel()

	ues := New[*radio, *core, uint32, uint64, ext](maxRANID, maxAMFID, errFull)
	ues.nextCoreID, ues.nextRANID = maxAMFID-1, maxRANID-1

	amf := &core{}
	ran := &radio{}

	var got []*UE[*radio, *core, uint32, uint64, ext]

	for i := range 3 {
		u := &UE[*radio, *core, uint32, uint64, ext]{RAN: ran, RANID: uint32(i), Core: amf}
		if err := ues.Add(u); err != nil {
			t.Fatal(err)
		}

		got = append(got, u)
	}

	if got[0].RANCoreID != maxAMFID || got[1].RANCoreID != 0 || got[2].RANCoreID != 1 {
		t.Fatalf("AMF UE NGAP IDs toward the radio = %d, %d, %d", got[0].RANCoreID, got[1].RANCoreID, got[2].RANCoreID)
	}

	if got[0].CoreRANID != maxRANID || got[1].CoreRANID != 0 || got[2].CoreRANID != 1 {
		t.Fatalf("RAN UE NGAP IDs toward the AMF = %d, %d, %d", got[0].CoreRANID, got[1].CoreRANID, got[2].CoreRANID)
	}

	ues.SetCoreID(got[0], 5)
	ues.SetCoreID(got[0], 6)

	if ues.byCoreID[coreKey[*core, uint64]{amf, 5}] != nil || ues.byCoreID[coreKey[*core, uint64]{amf, 6}] != got[0] {
		t.Fatal("AMF UE NGAP ID not replaced")
	}

	if gone := ues.Remove(got...); len(gone) != 3 || len(ues.byRANCoreID)+len(ues.byRANID)+len(ues.byCoreRANID)+len(ues.byCoreID) != 0 {
		t.Fatalf("left %+v", ues)
	}
}
