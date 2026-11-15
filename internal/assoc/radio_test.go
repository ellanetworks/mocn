package assoc

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
)

var (
	plmnA = db.PLMN{MCC: "001", MNC: "01"}
	t0    = time.Date(2026, 11, 13, 16, 31, 55, 0, time.UTC)
)

type testGateway = Gateway[testSetup, testRadio, testInfo, testCore]

type testNode = Radio[testSetup, testRadio]

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()

	g := New(Config{Now: func() time.Time { return t0 }}, Names{Core: "MME", Setup: "S1 Setup"}, nil,
		Protocol[testSetup, testRadio, testInfo, testCore](testProtocol{}))
	g.operators = []Operator{{ID: 1, Name: "A", PLMNs: []db.PLMN{plmnA}}}

	return g
}

func addRadio(g *testGateway, addr string) *testNode {
	n := &testNode{conn: &sctp.SCTPConn{}, addrs: []string{addr}, connectedAt: t0, tx: g.tx}

	g.mu.Lock()
	g.radios[n.conn] = n
	g.mu.Unlock()

	return n
}

func setUp(t *testing.T, g *testGateway, n *testNode, setup testSetup) {
	t.Helper()

	if _, err := g.BeginSetup(t.Context(), n, setup); err != nil {
		t.Fatal(err)
	}

	g.mu.Lock()
	g.setUpLocked(t.Context(), n, testRadio{})
	g.mu.Unlock()
}

func radioState(g *testGateway, n *testNode) RadioState {
	g.mu.Lock()
	defer g.mu.Unlock()

	return n.StateLocked()
}

func TestRejectedSetupIsKeptForDisplayOnly(t *testing.T) {
	t.Parallel()

	g := newTestGateway(t)
	setup := testSetup{ref: "eNB:001-01:MacroeNB-00001", plmns: []db.PLMN{plmnA}, tacs: []uint32{1}}

	up := addRadio(g, "10.0.0.1")
	setUp(t, g, up, setup)

	refused := addRadio(g, "10.0.0.2")

	_, err := g.BeginSetup(t.Context(), refused, setup)
	if !errors.Is(err, ErrNodeInUse) {
		t.Fatalf("BeginSetup = %v, want %v", err, ErrNodeInUse)
	}

	g.SetupRejected(t.Context(), refused, SetupInvalid, RadioSetupFailure[testSetup]{
		Request: &setup, Cause: "Semantic error", Reason: "in use",
	})

	if st := radioState(g, refused); st != RadioSetupFailed {
		t.Fatalf("state = %s, want %s", st, RadioSetupFailed)
	}

	g.mu.Lock()
	failure, live, ref := refused.Failure, refused.Setup, refused.ref.Load()
	operatorRadios := g.OperatorRadiosLocked(1)
	paged := g.pagingTargetsLocked(1, []TAI{{PLMN: plmnA, TAC: 1}})
	g.mu.Unlock()

	if failure == nil || failure.Request == nil || failure.Request.ref != setup.ref || failure.Cause != "Semantic error" ||
		failure.Reason != "in use" || !failure.At.Equal(t0) {
		t.Fatalf("failure = %+v", failure)
	}

	if live != nil || ref != nil {
		t.Fatalf("setup = %v, ref = %v, want none", live, ref)
	}

	if !slices.Equal(operatorRadios, []*testNode{up}) || !slices.Equal(paged, []*testNode{up}) {
		t.Fatalf("operator radios = %v, paging targets = %v, want the up radio only", operatorRadios, paged)
	}

	st := g.MetricsState()
	if st.Radios[string(RadioUp)] != 1 || st.Radios[string(RadioSetupFailed)] != 1 ||
		st.Radios[string(RadioConnected)] != 0 {
		t.Fatalf("radios by state = %v", st.Radios)
	}

	g.mu.Lock()
	g.removeLocked(up)
	_, merged := g.coreConfigLocked(1)
	g.mu.Unlock()

	if merged {
		t.Fatal("the rejected setup took part in the operator's setup")
	}

	other := addRadio(g, "10.0.0.3")
	if _, err := g.BeginSetup(t.Context(), other, setup); err != nil {
		t.Fatalf("BeginSetup = %v, want the rejected radio to hold no Global ID", err)
	}
}

func TestLaterSetupReplacesTheFailure(t *testing.T) {
	t.Parallel()

	g := newTestGateway(t)
	setup := testSetup{ref: "gNB:001-01:000001@22", plmns: []db.PLMN{{MCC: "001", MNC: "03"}}}
	n := addRadio(g, "10.0.0.1")

	if _, err := g.BeginSetup(t.Context(), n, setup); !errors.Is(err, ErrNoOperator) {
		t.Fatalf("BeginSetup = %v, want %v", err, ErrNoOperator)
	}

	g.SetupRejected(t.Context(), n, SetupUnknownPLMN, RadioSetupFailure[testSetup]{
		Request: &setup, Cause: "Unknown PLMN or SNPN", Reason: ErrNoOperator.Error(),
	})

	g.SetupRejected(t.Context(), n, SetupInvalid, RadioSetupFailure[testSetup]{Cause: "Semantic error", Reason: "bad"})

	g.mu.Lock()
	failure := n.Failure
	g.mu.Unlock()

	if radioState(g, n) != RadioSetupFailed || failure == nil || failure.Request != nil || failure.Cause != "Semantic error" {
		t.Fatalf("failure = %+v, want the latest rejection", failure)
	}

	setup.plmns = []db.PLMN{plmnA}

	if _, err := g.BeginSetup(t.Context(), n, setup); err != nil {
		t.Fatal(err)
	}

	if st := radioState(g, n); st != RadioConnected {
		t.Fatalf("state = %s, want %s while the setup is pending", st, RadioConnected)
	}

	g.mu.Lock()
	g.setUpLocked(t.Context(), n, testRadio{})
	failure = n.Failure
	g.mu.Unlock()

	if radioState(g, n) != RadioUp || failure != nil {
		t.Fatalf("state = %s, failure = %+v, want up without a failure", radioState(g, n), failure)
	}
}

func TestCauseLabel(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"unknown-PLMN":                   "Unknown PLMN",
		"unknown-PLMN-or-SNPN":           "Unknown PLMN or SNPN",
		"transport-resource-unavailable": "Transport resource unavailable",
		"":                               "",
	} {
		if got := CauseLabel(name); got != want {
			t.Errorf("CauseLabel(%q) = %q, want %q", name, got, want)
		}
	}
}
