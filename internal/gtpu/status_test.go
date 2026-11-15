package gtpu

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func wantStatus(t *testing.T, p *Plane, state State, reason Reason) Status {
	t.Helper()

	st, ok := p.Status(7)
	if !ok || st.State != state || st.Reason != reason {
		t.Fatalf("status = %+v, %v, want %s %s", st, ok, state, reason)
	}

	return st
}

func wantState(t *testing.T, p *Plane, state State, reason Reason) {
	t.Helper()

	for _, s := range states {
		wantSample(t, p, "ellamocn_user_plane_state", map[string]string{"operator": "op", "state": string(s)},
			flag(s == state))
	}

	for _, r := range unavailableReasons {
		wantSample(t, p, "ellamocn_user_plane_unavailable", map[string]string{"operator": "op", "reason": string(r)},
			flag(r == reason))
	}
}

func TestUserPlaneReadyOnceTheAssociationIsUp(t *testing.T) {
	h := newHarness(t, Operator{ID: 7, Name: "op", Aggregated: true})

	wantStatus(t, h.p, StateUnavailable, ReasonNotConnected)

	if _, err := h.p.Open(h.path(t, 7), Core, TLA(sgw), 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("open before the association is up = %v, want ErrUnavailable", err)
	}

	h.clock.t = h.clock.t.Add(time.Minute)
	h.anchor(t, 7, mme)

	st := wantStatus(t, h.p, StateReady, "")
	if st.Interface != "core0" || !slices.Equal(st.Addresses, []netip.Addr{coreGW}) || !st.Since.Equal(h.clock.t) {
		t.Fatalf("status = %+v", st)
	}

	wantState(t, h.p, StateReady, "")

	h.p.Unanchor(7, mme)
	wantStatus(t, h.p, StateUnavailable, ReasonNotConnected)
	wantState(t, h.p, StateUnavailable, ReasonNotConnected)

	if _, ok := h.dp.ifaces["core0"]; ok {
		t.Fatalf("leg kept on the interface after the association went down: %v", h.dp.ifaces)
	}
}

func TestTunnelRefusedWhenTheRouteLeavesByAnotherInterface(t *testing.T) {
	h := newHarness(t)

	elsewhere := netip.MustParseAddr("10.3.0.20")

	_, err := h.p.Open(h.path(t, 7), Core, TLA(elsewhere), 1)
	if !errors.Is(err, ErrRouteMismatch) {
		t.Fatalf("open toward an S-GW routed by core1 = %v, want ErrRouteMismatch", err)
	}

	if h.p.Tunnels() != 0 || len(h.dp.tunnels) != 0 {
		t.Fatal("refused tunnel programmed")
	}

	h.p.TunnelSetupFailed(7, "s1", "initial_context_setup", err)

	wantSample(t, h.p, "ellamocn_user_plane_tunnel_setup_failures_total", map[string]string{
		"operator": "op", "interface": "s1", "procedure": "initial_context_setup", "reason": "route_mismatch",
	}, 1)

	if _, err := h.p.Open(h.path(t, 7), Core, TLA(netip.MustParseAddr("192.0.2.1")), 1); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("open toward an S-GW with no route = %v, want ErrNoRoute", err)
	}

	if _, err := h.p.Open(h.path(t, 7), RAN, TLA(netip.MustParseAddr("10.2.0.30")), 1); !errors.Is(err, ErrRouteMismatch) {
		t.Fatalf("open toward a radio routed by the operator's interface = %v, want ErrRouteMismatch", err)
	}
}

func TestUserPlaneUnavailableWhileTheRouteToTheCoreMoves(t *testing.T) {
	h := newHarness(t)

	var logs bytes.Buffer

	h.p.log = slog.New(slog.NewTextHandler(&logs, nil))

	ul := h.uplink(t)
	moved := netip.MustParsePrefix(mme.String() + "/32")
	h.host.routes[moved] = Route{Src: coreB, Ifindex: 21}

	for range 3 {
		h.p.refresh()
	}

	wantStatus(t, h.p, StateUnavailable, ReasonRouteMismatch)

	if n := strings.Count(logs.String(), "level=ERROR"); n != 1 {
		t.Fatalf("%d ERROR logs, want one for the state change:\n%s", n, logs.String())
	}

	before := h.host.lookups.Load()

	for i := range 10 {
		if _, err := h.p.Open(h.path(t, 7), Core, TLA(sgw), uint32(i)); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("open while unavailable = %v, want ErrUnavailable", err)
		}
	}

	if got := h.host.lookups.Load(); got != before {
		t.Fatalf("%d route lookups for refused tunnels", got-before)
	}

	if _, ok := h.p.Get(ul.TEID); !ok {
		t.Fatal("live tunnel closed")
	}

	wantState(t, h.p, StateUnavailable, ReasonRouteMismatch)

	delete(h.host.routes, moved)
	h.p.refresh()

	wantStatus(t, h.p, StateReady, "")

	if _, err := h.p.Open(h.path(t, 7), Core, TLA(sgw), 99); err != nil {
		t.Fatalf("open once the route is back = %v", err)
	}
}

func TestUserPlaneUnavailableReasons(t *testing.T) {
	for _, c := range []struct {
		name   string
		apply  func(h *harness)
		reason Reason
	}{
		{"no route", func(h *harness) {
			for p := range h.host.routes {
				if p.Contains(mme) {
					delete(h.host.routes, p)
				}
			}
		}, ReasonNoRoute},
		{"source address changed", func(h *harness) {
			h.host.routes[netip.MustParsePrefix(mme.String()+"/32")] = Route{Src: netip.MustParseAddr("10.2.0.2"), Ifindex: 20}
		}, ReasonNoAddress},
		{"interface missing", func(h *harness) {
			h.dp.fail = map[string]error{"core0": errors.New("Link not found")}
			h.p.Unanchor(7, mme)
			h.anchor(t, 7, mme)
		}, ReasonInterfaceError},
		{"two associations by different interfaces", func(h *harness) {
			h.anchor(t, 7, mmeB)
		}, ReasonRouteMismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)

			c.apply(h)
			h.p.refresh()

			wantStatus(t, h.p, StateUnavailable, c.reason)
		})
	}
}

func TestUserPlaneOffKeepsLiveSessionsRelayed(t *testing.T) {
	h := newHarness(t)
	path := h.path(t, 7)

	if _, err := h.p.Open(path, Core, TLA(sgw), 1); err != nil {
		t.Fatal(err)
	}

	if err := h.p.SetOperators([]Operator{{ID: 7, Name: "op"}}); err != nil {
		t.Fatal(err)
	}

	wantStatus(t, h.p, StateOff, "")
	wantState(t, h.p, StateOff, "")

	if _, ok := h.p.Path(7, "eNB:1"); ok {
		t.Fatal("new sessions relayed with aggregation off")
	}

	if _, err := h.p.Open(path, Core, TLA(sgw), 2); err != nil {
		t.Fatalf("live session modified after aggregation was turned off = %v", err)
	}
}

func TestUserPlaneFollowsRouteChanges(t *testing.T) {
	h := newHarness(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go h.p.follow(ctx, h.dp.changed)

	h.p.mu.Lock()
	h.host.routes[netip.MustParsePrefix(mme.String()+"/32")] = Route{Src: coreB, Ifindex: 21}
	h.p.mu.Unlock()

	h.dp.changed <- struct{}{}

	for range 200 {
		if st, _ := h.p.Status(7); st.State == StateUnavailable {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("route change not followed")
}
