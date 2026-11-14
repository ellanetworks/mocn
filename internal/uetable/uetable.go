package uetable

import (
	"cmp"
	"slices"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/tracing"
)

type ID interface {
	~uint32 | ~uint64
}

type UE[R, A comparable, N, C ID, X any] struct {
	RAN         R
	RANID       N
	RANIDKnown  bool
	RANCoreID   C
	Core        A
	CoreRANID   N
	CoreID      C
	CoreIDKnown bool
	OperatorID  int64
	PLMN        db.PLMN
	Since       time.Time
	Release     *time.Timer
	TraceID     tracing.RANTraceID
	Ext         X
}

type ranKey[R comparable, N ID] struct {
	ran R
	id  N
}

type coreKey[A comparable, C ID] struct {
	core A
	id   C
}

type Table[R, A comparable, N, C ID, X any] struct {
	byRANCoreID map[C]*UE[R, A, N, C, X]
	byRANID     map[ranKey[R, N]]*UE[R, A, N, C, X]
	byCoreRANID map[N]*UE[R, A, N, C, X]
	byCoreID    map[coreKey[A, C]]*UE[R, A, N, C, X]
	maxRANID    N
	maxCoreID   C
	nextCoreID  C
	nextRANID   N
	errFull     error
}

func New[R, A comparable, N, C ID, X any](maxRANID N, maxCoreID C, errFull error) *Table[R, A, N, C, X] {
	return &Table[R, A, N, C, X]{
		byRANCoreID: map[C]*UE[R, A, N, C, X]{},
		byRANID:     map[ranKey[R, N]]*UE[R, A, N, C, X]{},
		byCoreRANID: map[N]*UE[R, A, N, C, X]{},
		byCoreID:    map[coreKey[A, C]]*UE[R, A, N, C, X]{},
		maxRANID:    maxRANID,
		maxCoreID:   maxCoreID,
		errFull:     errFull,
	}
}

func (t *Table[R, A, N, C, X]) Add(u *UE[R, A, N, C, X]) error {
	var none A

	if u.Core != none && uint64(len(t.byCoreRANID)) > uint64(t.maxRANID) {
		return t.errFull
	}

	for {
		t.nextCoreID = (t.nextCoreID + 1) & t.maxCoreID
		if _, used := t.byRANCoreID[t.nextCoreID]; !used {
			break
		}
	}

	u.RANCoreID = t.nextCoreID
	t.byRANCoreID[u.RANCoreID] = u

	if u.RANIDKnown {
		t.byRANID[ranKey[R, N]{u.RAN, u.RANID}] = u
	}

	if u.Core == none {
		return nil
	}

	for {
		t.nextRANID = (t.nextRANID + 1) & t.maxRANID
		if _, used := t.byCoreRANID[t.nextRANID]; !used {
			break
		}
	}

	u.CoreRANID = t.nextRANID
	t.byCoreRANID[u.CoreRANID] = u

	return nil
}

func (t *Table[R, A, N, C, X]) SetRANID(u *UE[R, A, N, C, X], id N) bool {
	key := ranKey[R, N]{u.RAN, id}
	if other := t.byRANID[key]; other != nil && other != u {
		return false
	}

	t.dropRANID(u)
	u.RANID, u.RANIDKnown = id, true
	t.byRANID[key] = u

	return true
}

func (t *Table[R, A, N, C, X]) SetCoreID(u *UE[R, A, N, C, X], id C) {
	if u.CoreIDKnown {
		t.dropCoreID(u)
	} else if u.Release != nil {
		u.Release.Stop()
		u.Release = nil
	}

	u.CoreID, u.CoreIDKnown = id, true
	t.byCoreID[coreKey[A, C]{u.Core, id}] = u
}

func (t *Table[R, A, N, C, X]) Rebind(u *UE[R, A, N, C, X], core A) {
	t.dropCoreID(u)
	u.Core, u.CoreIDKnown = core, false
}

func (t *Table[R, A, N, C, X]) Detach(u *UE[R, A, N, C, X]) {
	var none A

	if u.Core != none && t.byCoreRANID[u.CoreRANID] == u {
		delete(t.byCoreRANID, u.CoreRANID)
	}

	t.dropCoreID(u)
	u.Core, u.CoreIDKnown = none, false
}

func (t *Table[R, A, N, C, X]) dropRANID(u *UE[R, A, N, C, X]) {
	key := ranKey[R, N]{u.RAN, u.RANID}
	if u.RANIDKnown && t.byRANID[key] == u {
		delete(t.byRANID, key)
	}
}

func (t *Table[R, A, N, C, X]) dropCoreID(u *UE[R, A, N, C, X]) {
	key := coreKey[A, C]{u.Core, u.CoreID}
	if u.CoreIDKnown && t.byCoreID[key] == u {
		delete(t.byCoreID, key)
	}
}

func (t *Table[R, A, N, C, X]) Remove(us ...*UE[R, A, N, C, X]) []*UE[R, A, N, C, X] {
	var (
		out  []*UE[R, A, N, C, X]
		none A
	)

	for _, u := range us {
		if u == nil || t.byRANCoreID[u.RANCoreID] != u || slices.Contains(out, u) {
			continue
		}

		delete(t.byRANCoreID, u.RANCoreID)
		t.dropRANID(u)

		if u.Core != none {
			delete(t.byCoreRANID, u.CoreRANID)
		}

		if u.Release != nil {
			u.Release.Stop()
		}

		u.TraceID = tracing.RANTraceID{}
		t.dropCoreID(u)

		out = append(out, u)
	}

	return out
}

func (t *Table[R, A, N, C, X]) Where(keep func(*UE[R, A, N, C, X]) bool) []*UE[R, A, N, C, X] {
	var out []*UE[R, A, N, C, X]

	for _, u := range t.byRANCoreID {
		if keep(u) {
			out = append(out, u)
		}
	}

	slices.SortFunc(out, func(a, b *UE[R, A, N, C, X]) int { return cmp.Compare(a.RANCoreID, b.RANCoreID) })

	return out
}

func (t *Table[R, A, N, C, X]) OnRAN(ran R, coreID *C, ranID *N) []*UE[R, A, N, C, X] {
	var out []*UE[R, A, N, C, X]

	if coreID != nil {
		if u := t.byRANCoreID[*coreID]; u != nil && u.RAN == ran {
			out = append(out, u)
		}
	}

	if ranID != nil {
		if u := t.byRANID[ranKey[R, N]{ran, *ranID}]; u != nil && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}

	return out
}

func (t *Table[R, A, N, C, X]) OnCore(core A, coreID *C, ranID *N) []*UE[R, A, N, C, X] {
	var out []*UE[R, A, N, C, X]

	if ranID != nil {
		if u := t.byCoreRANID[*ranID]; u != nil && u.Core == core {
			out = append(out, u)
		}
	}

	if coreID != nil {
		if u := t.byCoreID[coreKey[A, C]{core, *coreID}]; u != nil && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}

	return out
}

func (t *Table[R, A, N, C, X]) ByRANCoreID(id C) *UE[R, A, N, C, X] {
	return t.byRANCoreID[id]
}

func (t *Table[R, A, N, C, X]) ByRANID(ran R, id N) *UE[R, A, N, C, X] {
	return t.byRANID[ranKey[R, N]{ran, id}]
}

func (t *Table[R, A, N, C, X]) ByCoreRANID(id N) *UE[R, A, N, C, X] {
	return t.byCoreRANID[id]
}

func (t *Table[R, A, N, C, X]) ByCoreID(core A, id C) *UE[R, A, N, C, X] {
	return t.byCoreID[coreKey[A, C]{core, id}]
}

func (t *Table[R, A, N, C, X]) CountByOperator() map[int64]int {
	var none A

	counts := map[int64]int{}

	for _, u := range t.byRANCoreID {
		if u.Core != none {
			counts[u.OperatorID]++
		}
	}

	return counts
}
