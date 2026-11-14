package assoc

import (
	"reflect"
	"testing"
	"time"

	"github.com/ellanetworks/core/sctp"
)

type answered struct {
	got     []string
	missing int
}

func TestAreasMergeOneKind(t *testing.T) {
	t.Parallel()

	var a Areas[string, string, int]

	a.AddTAI("tai1", []int{1})
	a.AddTAI("tai2", []int{2})
	a.AddTAI("tai1", []int{3})

	want := []Area[string, int]{{ID: "tai1", Cells: []int{1, 3}}, {ID: "tai2", Cells: []int{2}}}
	if got := a.OneKind(); !reflect.DeepEqual(got.TAIs, want) || got.Cells != nil || got.EAIs != nil {
		t.Fatalf("one kind = %+v, want TAIs %+v", got, want)
	}

	a.AddEAI("eai1", []int{4})
	a.AddCells(5)

	if got := a.OneKind(); !reflect.DeepEqual(got, Areas[string, string, int]{Cells: []int{5, 1, 3, 2, 4}}) {
		t.Fatalf("mixed kinds = %+v, want the cells", got)
	}

	if !(Areas[string, string, int]{}).Empty() || a.Empty() {
		t.Fatal("Empty")
	}
}

func TestAreasCapped(t *testing.T) {
	t.Parallel()

	var a Areas[int, int, int]

	cells := make([]int, maxnoofWarningItems-1)
	a.AddCells(cells...)
	a.AddCells(1, 2, 3)

	if len(a.Cells) != maxnoofWarningItems {
		t.Fatalf("cells = %d, want %d", len(a.Cells), maxnoofWarningItems)
	}
}

func TestWarningsAnswered(t *testing.T) {
	t.Parallel()

	w := NewWarnings[int, string](time.Minute)
	r1, r2, r3 := new(sctp.SCTPConn), new(sctp.SCTPConn), new(sctp.SCTPConn)
	done := make(chan answered, 4)
	answer := func(got []string, missing int) { done <- answered{got, missing} }

	first := w.start(1, []*sctp.SCTPConn{r1, r2, r3}, answer)
	w.start(1, []*sctp.SCTPConn{r1}, answer)

	if !w.Answer(r1, 1, "a") || !w.Answer(r1, 1, "b") || w.Answer(r1, 1, "c") || w.Answer(r2, 2, "x") {
		t.Fatal("answers not matched to the oldest request with the same key")
	}

	if got := <-done; !reflect.DeepEqual(got, answered{got: []string{"b"}}) {
		t.Fatalf("second request answered with %+v", got)
	}

	w.drop(r3, first)
	w.RadioLost(r2)

	if got := <-done; !reflect.DeepEqual(got, answered{got: []string{"a"}}) {
		t.Fatalf("first request answered with %+v", got)
	}

	if len(w.pending) != 0 {
		t.Fatalf("pending = %+v", w.pending)
	}
}

func TestWarningsExpire(t *testing.T) {
	t.Parallel()

	w := NewWarnings[int, string](50 * time.Millisecond)
	r1, r2 := new(sctp.SCTPConn), new(sctp.SCTPConn)
	done := make(chan answered, 1)

	w.start(1, []*sctp.SCTPConn{r1, r2}, func(got []string, missing int) { done <- answered{got, missing} })
	w.Answer(r1, 1, "a")

	if got := <-done; !reflect.DeepEqual(got, answered{got: []string{"a"}, missing: 1}) {
		t.Fatalf("expired request answered with %+v", got)
	}

	if w.Answer(r2, 1, "late") {
		t.Fatal("late answer matched an expired request")
	}

	w.RadioLost(r2)

	select {
	case got := <-done:
		t.Fatalf("answered twice: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}
