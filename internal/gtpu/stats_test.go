package gtpu

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func wantNoOperator(t *testing.T, p *Plane, operator string) {
	t.Helper()

	for name, all := range gather(t, p) {
		if got := matching(all, map[string]string{"operator": operator}); len(got) != 0 {
			t.Fatalf("%s keeps series of operator %q: %+v", name, operator, got)
		}
	}
}

func TestMetricsArePreinitialised(t *testing.T) {
	h := newHarness(t)
	h.p.TunnelProcedures("s1", "initial_context_setup", "path_switch_request")

	if problems, err := testutil.CollectAndLint(collector{h.p}); err != nil || len(problems) > 0 {
		t.Fatalf("lint = %v, %v", problems, err)
	}

	op := map[string]string{"operator": "op"}

	wantSample(t, h.p, "ellamocn_user_plane_enabled", nil, 1)

	for _, d := range directions {
		for _, name := range []string{
			"ellamocn_user_plane_tunnels", "ellamocn_user_plane_tunnel_setups_total",
			"ellamocn_user_plane_slow_path_relayed_total",
		} {
			wantSample(t, h.p, name, map[string]string{"operator": "op", "direction": d}, 0)
		}
	}

	for _, r := range dropReasons {
		wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
			map[string]string{"operator": "op", "path": "fast", "reason": r}, 0)
	}

	for _, r := range operatorSlowDrops {
		wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
			map[string]string{"operator": "op", "path": "slow", "reason": r}, 0)
	}

	for _, r := range unattributedSlowDrops {
		wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
			map[string]string{"operator": "", "path": "slow", "reason": r}, 0)
	}

	for origin, results := range errorIndicationResults {
		for _, r := range results {
			wantSample(t, h.p, "ellamocn_user_plane_error_indications_total",
				map[string]string{"origin": origin, "result": r}, 0)
		}
	}

	for _, r := range passReasons {
		wantSample(t, h.p, "ellamocn_user_plane_slow_path_packets_total", map[string]string{"reason": r}, 0)
	}

	for _, proc := range []string{"initial_context_setup", "path_switch_request"} {
		for _, r := range append([]string{failureOther}, "user_plane_unavailable", "too_many_paths") {
			wantSample(t, h.p, "ellamocn_user_plane_tunnel_setup_failures_total",
				map[string]string{"operator": "op", "interface": "s1", "procedure": proc, "reason": r}, 0)
		}
	}

	if got := matching(gather(t, h.p)["ellamocn_user_plane_tunnel_setup_failures_total"], op); len(got) != 2*(len(failureReasons)+1) {
		t.Fatalf("%d tunnel setup failure series, want 2 procedures by %d reasons", len(got), len(failureReasons)+1)
	}

	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_entries", map[string]string{"map": relayMapTunnels}, 0)
	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_entries", map[string]string{"map": relayMapIfaces}, 2)
	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_max_entries", map[string]string{"map": relayMapTunnels}, maxTunnels)
	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_max_entries", map[string]string{"map": relayMapTunnelStats}, maxTunnels)
	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_memory_bytes", map[string]string{"map": relayMapDrops}, 256)
	wantAbsent(t, h.p, "ellamocn_user_plane_bpf_map_entries", map[string]string{"map": relayMapDrops})

	h.uplink(t)

	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_entries", map[string]string{"map": relayMapTunnels}, 1)
	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_entries", map[string]string{"map": relayMapTunnelStats}, 1)
	wantSample(t, h.p, "ellamocn_user_plane_bpf_map_entries", map[string]string{"map": relayMapPathStats}, 1)
}

func TestMetricsForUplinkAndDownlinkTunnels(t *testing.T) {
	h := newHarness(t)
	path := h.path(t, 7)

	ul := h.uplink(t)

	dl, err := h.p.Open(path, RAN, TLA(radio), 1)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range directions {
		wantSample(t, h.p, "ellamocn_user_plane_tunnels", map[string]string{"operator": "op", "direction": d}, 1)
		wantSample(t, h.p, "ellamocn_user_plane_tunnel_setups_total", map[string]string{"operator": "op", "direction": d}, 1)
	}

	wantSample(t, h.p, "ellamocn_user_plane_peer_up", map[string]string{"operator": "op", "side": "core", "peer": sgw.String()}, 1)
	wantSample(t, h.p, "ellamocn_user_plane_peer_up", map[string]string{"operator": "op", "side": "radio", "peer": radio.String()}, 1)

	h.p.Close(ul.TEID, dl.TEID)

	for _, d := range directions {
		wantSample(t, h.p, "ellamocn_user_plane_tunnels", map[string]string{"operator": "op", "direction": d}, 0)
		wantSample(t, h.p, "ellamocn_user_plane_tunnel_setups_total", map[string]string{"operator": "op", "direction": d}, 1)
	}

	wantAbsent(t, h.p, "ellamocn_user_plane_peer_up", nil)
}

func TestMetricsFollowAnOperatorRename(t *testing.T) {
	h := newHarness(t)
	h.p.TunnelProcedures("s1", "initial_context_setup")

	tun := h.uplink(t)
	h.dp.pathStats[tun.path] = Counters{Packets: 3, Bytes: 300}
	h.p.TunnelSetupFailed(7, "s1", "initial_context_setup", ErrNoTEID)

	if err := h.p.SetOperators([]Operator{{ID: 7, Name: "renamed", Aggregated: true}}); err != nil {
		t.Fatal(err)
	}

	wantNoOperator(t, h.p, "op")

	labels := map[string]string{"operator": "renamed", "radio": "eNB:1", "direction": "uplink"}
	wantSample(t, h.p, "ellamocn_user_plane_packets_total", labels, 3)
	wantSample(t, h.p, "ellamocn_user_plane_bytes_total", labels, 300)
	wantSample(t, h.p, "ellamocn_user_plane_tunnel_setup_failures_total", map[string]string{
		"operator": "renamed", "interface": "s1", "procedure": "initial_context_setup", "reason": "no_teid",
	}, 1)

	h.p.Close(tun.TEID)

	wantSample(t, h.p, "ellamocn_user_plane_tunnels", map[string]string{"operator": "renamed", "direction": "uplink"}, 0)
	wantSample(t, h.p, "ellamocn_user_plane_state", map[string]string{"operator": "renamed", "state": "ready"}, 1)
}

func TestMetricsForgetARemovedOperator(t *testing.T) {
	h := newHarness(t)
	h.p.TunnelProcedures("s1", "initial_context_setup")

	tun := h.uplink(t)
	h.dp.pathStats[tun.path] = Counters{Packets: 5, Bytes: 500}
	h.p.Close(tun.TEID)

	h.p.nextPath = maxPaths
	h.clock.t = h.clock.t.Add(defaultQuarantine)

	other, _ := h.p.Path(7, "eNB:2")
	if _, err := h.p.Open(other, Core, TLA(sgw), 1); err != nil {
		t.Fatal(err)
	}

	h.p.TunnelSetupFailed(7, "s1", "initial_context_setup", ErrNoTEID)
	wantSample(t, h.p, "ellamocn_user_plane_packets_total",
		map[string]string{"operator": "op", "radio": "eNB:1", "direction": "uplink"}, 5)

	if err := h.p.SetOperators(nil); err != nil {
		t.Fatal(err)
	}

	wantNoOperator(t, h.p, "op")

	if len(h.p.stats.retired) != 0 || len(h.p.stats.paths) != 0 || len(h.p.stats.operators) != 0 ||
		len(h.p.paths) != 0 || len(h.p.peers) != 0 {
		t.Fatalf("state kept for a removed operator: retired %v, paths %v, operators %v, plane paths %v, peers %v",
			h.p.stats.retired, h.p.stats.paths, h.p.stats.operators, h.p.paths, h.p.peers)
	}

	h.p.TunnelSetupFailed(7, "s1", "initial_context_setup", ErrNoTEID)
	wantAbsent(t, h.p, "ellamocn_user_plane_tunnel_setup_failures_total", nil)

	if err := h.p.SetOperators([]Operator{{ID: 7, Name: "op", Aggregated: true}}); err != nil {
		t.Fatal(err)
	}

	wantSample(t, h.p, "ellamocn_user_plane_tunnel_setup_failures_total", map[string]string{
		"operator": "op", "interface": "s1", "procedure": "initial_context_setup", "reason": "no_teid",
	}, 0)
	wantAbsent(t, h.p, "ellamocn_user_plane_packets_total", nil)
}

func TestMetricsForgetARadioThatLeft(t *testing.T) {
	h := newHarness(t)

	first := h.uplink(t)
	h.dp.pathStats[first.path] = Counters{Packets: 2, Bytes: 20}

	other, _ := h.p.Path(7, "eNB:2")

	second, err := h.p.Open(other, Core, TLA(sgw), 2)
	if err != nil {
		t.Fatal(err)
	}

	h.dp.pathStats[second.path] = Counters{Packets: 4, Bytes: 40}

	gone := map[string]string{"operator": "op", "radio": "eNB:1", "direction": "uplink"}
	stays := map[string]string{"operator": "op", "radio": "eNB:2", "direction": "uplink"}

	h.p.ForgetRadio("eNB:1")
	wantSample(t, h.p, "ellamocn_user_plane_packets_total", gone, 2)

	h.p.Close(first.TEID)
	wantAbsent(t, h.p, "ellamocn_user_plane_packets_total", gone)
	wantSample(t, h.p, "ellamocn_user_plane_packets_total", stays, 4)

	if len(h.p.paths) != 1 || len(h.p.freePaths) != 1 || h.p.freePaths[0] != first.path {
		t.Fatalf("paths %v, free %v, want the radio's path freed", h.p.paths, h.p.freePaths)
	}

	h.dp.pathStats[first.path] = Counters{Packets: 9, Bytes: 90}

	back, err := h.p.Open(h.path(t, 7), Core, TLA(sgw), 3)
	if err != nil || back.path != first.path {
		t.Fatalf("open = %+v, %v, want the freed path %d", back, err, first.path)
	}

	wantSample(t, h.p, "ellamocn_user_plane_packets_total", gone, 0)

	h.p.Close(second.TEID)
	h.p.ForgetRadio("eNB:2")
	wantAbsent(t, h.p, "ellamocn_user_plane_packets_total", stays)
}

func TestAdoptedTunnelsCountUnderTheNewRadio(t *testing.T) {
	h := newHarness(t)

	var source, target Session

	if _, err := h.p.Bind(&source, Binding{Path: h.path(t, 7), Bearer: 5, Slot: Slot{Kind: KindUL}}, Core, TLA(sgw), 1); err != nil {
		t.Fatal(err)
	}

	ul := source.Refs()[0].TEID
	old, _ := h.p.Get(ul)
	h.dp.pathStats[old.path] = Counters{Packets: 2, Bytes: 20}

	to, _ := h.p.Path(7, "eNB:2")
	h.p.Adopt(&source, &target, to, func(Ref) bool { return true })

	moved, _ := h.p.Get(ul)
	if moved.Radio != "eNB:2" || moved.path == old.path || h.dp.tunnels[ul].Path != moved.path {
		t.Fatalf("adopted tunnel = %+v, datapath path %d, want it on a path of eNB:2", moved, h.dp.tunnels[ul].Path)
	}

	h.dp.pathStats[moved.path] = Counters{Packets: 3, Bytes: 30}

	before := map[string]string{"operator": "op", "radio": "eNB:1", "direction": "uplink"}
	after := map[string]string{"operator": "op", "radio": "eNB:2", "direction": "uplink"}

	wantSample(t, h.p, "ellamocn_user_plane_packets_total", before, 2)
	wantSample(t, h.p, "ellamocn_user_plane_packets_total", after, 3)
	wantSample(t, h.p, "ellamocn_user_plane_tunnels", map[string]string{"operator": "op", "direction": "uplink"}, 1)

	h.p.ForgetRadio("eNB:1")

	wantAbsent(t, h.p, "ellamocn_user_plane_packets_total", before)
	wantSample(t, h.p, "ellamocn_user_plane_packets_total", after, 3)

	if len(h.p.paths) != 1 || len(h.p.freePaths) != 1 || h.p.freePaths[0] != old.path {
		t.Fatalf("paths %v, free %v, want the source radio's path freed", h.p.paths, h.p.freePaths)
	}
}
