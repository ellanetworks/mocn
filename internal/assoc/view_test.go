package assoc

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/ellanetworks/mocn/internal/db"
)

var (
	viewPLMNs = []db.PLMN{{MCC: "001", MNC: "01"}, {MCC: "002", MNC: "02"}, {MCC: "003", MNC: "03"}}
	viewTACs  = []uint32{1, 2, 3}
)

type viewWorld struct {
	t      *testing.T
	g      *testGateway
	rng    *rand.Rand
	radios []*testNode
	next   int
}

func pickSome[T any](rng *rand.Rand, from []T) []T {
	var out []T

	for _, v := range from {
		if rng.IntN(2) == 0 {
			out = append(out, v)
		}
	}

	return out
}

func (w *viewWorld) setup() testSetup {
	return testSetup{
		ref: fmt.Sprintf("eNB:%d", w.rng.IntN(8)), plmns: pickSome(w.rng, viewPLMNs), tacs: pickSome(w.rng, viewTACs),
	}
}

func (w *viewWorld) radio() *testNode {
	if len(w.radios) == 0 {
		return nil
	}

	return w.radios[w.rng.IntN(len(w.radios))]
}

func (w *viewWorld) operators() []db.Operator {
	var ops []db.Operator

	for id := range int64(3) {
		if w.rng.IntN(4) == 0 {
			continue
		}

		ops = append(ops, db.Operator{
			ID: id + 1, Name: fmt.Sprint(id + 1), PLMNs: pickSome(w.rng, viewPLMNs), GlobalENBID: &db.GlobalENBID{},
			MMEs: []db.CoreNode{{TNLAddresses: []string{fmt.Sprintf("10.9.0.%d", id+1)}}},
		})
	}

	return ops
}

func (w *viewWorld) event() string {
	ctx := w.t.Context()
	n := w.radio()

	switch w.rng.IntN(10) {
	case 0:
		w.next++
		w.radios = append(w.radios, addRadio(w.g, fmt.Sprintf("10.0.0.%d", w.next)))

		return "connect"
	case 1, 2:
		if n == nil {
			return "none"
		}

		_, _ = w.g.BeginSetup(ctx, n, w.setup())

		return "setup"
	case 3:
		if n == nil {
			return "none"
		}

		w.g.mu.Lock()
		if n.Setup != nil && !n.Up {
			w.g.setUpLocked(ctx, n, testRadio{})
		}
		w.g.mu.Unlock()

		return "up"
	case 4:
		if n == nil {
			return "none"
		}

		next := w.setup()
		_ = w.g.Reconfigure(ctx, n, func(s testSetup) (testSetup, bool) {
			s.plmns, s.tacs = next.plmns, next.tacs
			return s, true
		}, func([]int64) {})

		return "reconfigure"
	case 5:
		if n == nil {
			return "none"
		}

		w.g.SetupRejected(ctx, n, SetupInvalid, RadioSetupFailure[testSetup]{Cause: "Semantic error"})

		return "reject"
	case 6:
		if n == nil {
			return "none"
		}

		w.g.TakeDown(n)

		return "take down"
	case 7:
		if n == nil {
			return "none"
		}

		w.g.radioDisconnected(ctx, n.conn)
		w.radios = slices.DeleteFunc(w.radios, func(r *testNode) bool { return r == n })

		return "disconnect"
	case 8:
		ops := w.operators()

		w.g.mu.Lock()
		w.g.applyLocked(ops, false)
		w.g.configChangedLocked()
		w.g.mu.Unlock()

		return "operators"
	default:
		return "none"
	}
}

func byRef(a, b *testNode) int {
	return strings.Compare(a.Setup.ref, b.Setup.ref)
}

func (w *viewWorld) check(step int, event string) {
	w.t.Helper()

	g := w.g

	g.mu.Lock()
	defer g.mu.Unlock()

	var set []*testNode

	for _, n := range g.radios {
		if n.Setup != nil {
			set = append(set, n)
		}
	}

	slices.SortFunc(set, byRef)

	setups := make([]testSetup, len(set))
	for i, n := range set {
		setups[i] = *n.Setup
	}

	allTAIs := make([]TAI, 0, len(viewPLMNs)*len(viewTACs))

	for _, p := range viewPLMNs {
		for _, tac := range viewTACs {
			allTAIs = append(allTAIs, TAI{PLMN: p, TAC: tac})
		}
	}

	for id := range int64(5) {
		got, gotOK := g.coreConfigLocked(id)

		var (
			want   testCore
			wantOK bool
			plmns  []db.PLMN
		)

		if o, ok := g.OperatorLocked(id); ok {
			plmns = o.PLMNs
			want, wantOK = testProtocol{}.CoreConfig(o.PLMNs, setups)
		}

		if gotOK != wantOK || !slices.Equal(got.served, want.served) {
			w.t.Fatalf("step %d (%s), operator %d: cached config %v (%v), full recompute %v (%v)", step, event, id,
				got.served, gotOK, want.served, wantOK)
		}

		lists := [][]TAI{allTAIs}
		for _, tai := range allTAIs {
			lists = append(lists, []TAI{tai})
		}

		for _, list := range lists {
			var brute []*testNode

			for _, n := range set {
				if n.Up && slices.ContainsFunc(n.Setup.TAIs(), func(t TAI) bool {
					return slices.Contains(list, t) && slices.Contains(plmns, t.PLMN)
				}) {
					brute = append(brute, n)
				}
			}

			paged := slices.SortedFunc(slices.Values(g.pagingTargetsLocked(id, list)), byRef)

			if !slices.Equal(paged, brute) {
				w.t.Fatalf("step %d (%s), operator %d, TAIs %v: paging targets %d, full scan %d", step, event, id, list,
					len(paged), len(brute))
			}
		}
	}
}

func TestViewMatchesAFullRecompute(t *testing.T) {
	t.Parallel()

	for seed := range uint64(25) {
		w := &viewWorld{t: t, g: newTestGateway(t), rng: rand.New(rand.NewPCG(seed, 1))}

		for step := range 300 {
			w.check(step, w.event())
		}
	}
}
