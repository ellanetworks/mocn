package gtpu

import (
	"errors"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	pathFast = "fast"
	pathSlow = "slow"

	dropNoTunnel             = "no_tunnel"
	dropQuarantined          = "quarantined"
	dropMalformed            = "malformed"
	dropWrongInterface       = "wrong_interface"
	dropUnknownMessage       = "unknown_message"
	dropExtensionNotif       = "supported_extension_headers_notification"
	dropUnsupportedExtension = "unsupported_extension_header"
	dropNoEgressInterface    = "no_egress_interface"
	dropWrongEgressInterface = "wrong_egress_interface" // #nosec G101
	dropSendFailed           = "send_failed"

	originReceived  = "received"
	originGenerated = "generated"

	resultTranslated      = "translated"
	resultNoTunnel        = "no_tunnel"
	resultNoReturnAddress = "no_return_address"
	resultMalformed       = "malformed"
	resultSent            = "sent"
	resultRateLimited     = "rate_limited"
)

var (
	directions = []string{RAN.direction(), Core.direction()}

	operatorSlowDrops = []string{
		dropWrongInterface, dropMalformed, dropUnsupportedExtension, dropNoEgressInterface, dropWrongEgressInterface,
		dropSendFailed,
	}

	unattributedSlowDrops = []string{
		dropNoTunnel, dropQuarantined, dropMalformed, dropWrongInterface, dropUnknownMessage, dropExtensionNotif,
	}

	errorIndicationResults = map[string][]string{
		originReceived:  {resultTranslated, resultNoTunnel, resultNoReturnAddress, resultMalformed},
		originGenerated: {resultSent, resultRateLimited},
	}

	states             = []State{StateReady, StateUnavailable, StateOff}
	unavailableReasons = []Reason{
		ReasonNotConnected, ReasonNoRoute, ReasonRouteMismatch, ReasonNoAddress, ReasonInterfaceError,
		ReasonTooManyOperators,
	}

	trackedMaps = []string{relayMapTunnels, relayMapTunnelStats, relayMapIfaces, relayMapPathStats}
)

type pathLabels struct {
	operator  int64
	radio     string
	direction string
}

type failureKey struct {
	iface, procedure, reason string
}

type operatorStats struct {
	tunnels  map[string]int
	setups   map[string]uint64
	relayed  map[string]uint64
	drops    map[string]uint64
	failures map[failureKey]uint64
}

func (o *operatorStats) initFailure(k failureKey) {
	if _, ok := o.failures[k]; !ok {
		o.failures[k] = 0
	}
}

type stats struct {
	mu         sync.Mutex
	paths      map[uint32]pathLabels
	slow       map[uint32]Counters
	retired    map[pathLabels]Counters
	operators  map[int64]*operatorStats
	drops      map[string]uint64
	errorInds  map[[2]string]uint64
	procedures map[string][]string

	loaded prometheus.Gauge
}

var (
	enabledDesc = prometheus.NewDesc("ellamocn_user_plane_enabled",
		"1 when user-plane aggregation is allowed (ran.user_plane.enabled in the configuration file), 0 otherwise.",
		nil, nil)
	stateDesc = prometheus.NewDesc("ellamocn_user_plane_state",
		"State of an operator's user plane, by operator and state (ready: Ella MOCN relays the operator's "+
			"subscriber traffic; unavailable: user-plane aggregation is on for the operator but Ella MOCN cannot relay, "+
			"new sessions are refused; off: user-plane aggregation is off for the operator or not allowed). "+
			"1 for the current state, 0 for the others.",
		[]string{"operator", "state"}, nil)
	unavailableDesc = prometheus.NewDesc("ellamocn_user_plane_unavailable",
		"Why an operator's user plane is unavailable, by operator and reason (not_connected: no MME or AMF "+
			"association is up; no_route: no route to an MME or AMF; route_mismatch: the MMEs and AMFs are reached by "+
			"different interfaces or source addresses, or the route moved to another interface; no_address: the "+
			"route's source address changed; interface_error: the datapath cannot use the interface; "+
			"too_many_operators: 63 operators already use user-plane aggregation). 1 for the current reason while the "+
			"state is unavailable, 0 otherwise.",
		[]string{"operator", "reason"}, nil)
	tunnelsDesc = prometheus.NewDesc("ellamocn_user_plane_tunnels",
		"GTP-U tunnels Ella MOCN relays, by operator and direction (uplink: received from a radio; downlink: "+
			"received from the operator).",
		[]string{"operator", "direction"}, nil)
	setupsDesc = prometheus.NewDesc("ellamocn_user_plane_tunnel_setups_total",
		"GTP-U tunnels Ella MOCN set up, by operator and direction (uplink: received from a radio; downlink: "+
			"received from the operator).",
		[]string{"operator", "direction"}, nil)
	failuresDesc = prometheus.NewDesc("ellamocn_user_plane_tunnel_setup_failures_total",
		"Procedures whose user-plane transport Ella MOCN could not relay, by operator, interface (s1, ng), "+
			"procedure and reason (user_plane_unavailable, no_route, route_mismatch, no_address, no_teid, "+
			"too_many_paths, invalid_address, unknown_endpoint, other). The UE is released, or the handover or path "+
			"switch is rejected.",
		[]string{"operator", "interface", "procedure", "reason"}, nil)
	packetsDesc = prometheus.NewDesc("ellamocn_user_plane_packets_total",
		"G-PDUs relayed, by operator, radio (global eNB or gNB ID) and direction (uplink: received from a radio; "+
			"downlink: received from the operator). End Marker and Tunnel Status messages are not counted.",
		[]string{"operator", "radio", "direction"}, nil)
	bytesDesc = prometheus.NewDesc("ellamocn_user_plane_bytes_total",
		"Octets of the T-PDUs of the G-PDUs relayed, the inner IP packet without the GTP-U header and extension "+
			"headers, by operator, radio (global eNB or gNB ID) and direction (uplink: received from a radio; "+
			"downlink: received from the operator).",
		[]string{"operator", "radio", "direction"}, nil)
	droppedDesc = prometheus.NewDesc("ellamocn_user_plane_dropped_packets_total",
		"GTP-U messages dropped, by operator, path (fast: eBPF datapath; slow: Ella MOCN process) and reason. "+
			"path=fast: wrong_interface (received on an interface of another operator), wrong_egress_interface "+
			"(routed out of another interface than the tunnel's), fib_blackhole, fib_unreachable, fib_prohibit "+
			"(route type of the peer), rewrite_failed (the packet could not be rewritten). "+
			"path=slow, for a tunnel of the operator: wrong_interface, malformed, unsupported_extension_header "+
			"(an extension header that must be comprehended), no_egress_interface, wrong_egress_interface, "+
			"send_failed. path=slow, with no operator: no_tunnel (unknown TEID), quarantined (TEID of a tunnel "+
			"closed less than 10 s ago), malformed, wrong_interface (Echo Request on an interface Ella MOCN does not "+
			"relay), unknown_message, supported_extension_headers_notification.",
		[]string{"operator", "path", "reason"}, nil)
	passesDesc = prometheus.NewDesc("ellamocn_user_plane_slow_path_packets_total",
		"Packets on user-plane interfaces that the fast path left to the kernel, by reason (ip_fragment: an IP "+
			"fragment, of any protocol, counted before UDP and GTP-U parsing; no_tunnel: unknown TEID; not_local: not "+
			"addressed to the tunnel's address; extension_header: an extension header the fast path does not relay; "+
			"zero_checksum: IPv6 with a zero UDP checksum; merged: a GRO aggregate; frag_needed: larger than the "+
			"egress MTU; forwarding_disabled; ipsec_policy: policy-based IPsec on the ingress or egress interface; "+
			"fib_other: another route lookup result). Except ip_fragment, they are G-PDUs, End Markers or Tunnel "+
			"Status messages.",
		[]string{"reason"}, nil)
	relayedDesc = prometheus.NewDesc("ellamocn_user_plane_slow_path_relayed_total",
		"GTP-U messages (G-PDU, End Marker, Tunnel Status) relayed by the slow path, by operator and direction "+
			"(uplink: received from a radio; downlink: received from the operator).",
		[]string{"operator", "direction"}, nil)
	errorIndsDesc = prometheus.NewDesc("ellamocn_user_plane_error_indications_total",
		"GTP-U Error Indications, by origin and result. origin=received: from a peer; result translated (sent "+
			"to the other side of the matching tunnels), no_tunnel (no matching tunnel), no_return_address (no "+
			"address known for the other side), malformed. origin=generated: by Ella MOCN, for a G-PDU to an unknown "+
			"TEID; result sent, rate_limited (over 100 per second).",
		[]string{"origin", "result"}, nil)
	peerUpDesc = prometheus.NewDesc("ellamocn_user_plane_peer_up",
		"Whether a GTP-U peer answers Echo Requests, by operator, side (radio, core) and peer address: 0 once 3 "+
			"consecutive Echo Requests, sent every 60 s, are unanswered, 1 otherwise. Present while a tunnel of the "+
			"operator relays to the peer.",
		[]string{"operator", "side", "peer"}, nil)
	mapEntriesDesc = prometheus.NewDesc("ellamocn_user_plane_bpf_map_entries",
		"Entries currently installed in a user-plane datapath BPF map. Divide by "+
			"ellamocn_user_plane_bpf_map_max_entries for a fill ratio.",
		[]string{"map"}, nil)
	mapMaxEntriesDesc = prometheus.NewDesc("ellamocn_user_plane_bpf_map_max_entries",
		"Capacity of a user-plane datapath BPF map, as declared by its max_entries attribute.",
		[]string{"map"}, nil)
	mapMemoryDesc = prometheus.NewDesc("ellamocn_user_plane_bpf_map_memory_bytes",
		"Kernel memory locked by a user-plane datapath BPF map, as reported by its memlock accounting.",
		[]string{"map"}, nil)
)

func newStats() *stats {
	s := &stats{
		paths:      map[uint32]pathLabels{},
		slow:       map[uint32]Counters{},
		retired:    map[pathLabels]Counters{},
		operators:  map[int64]*operatorStats{},
		drops:      map[string]uint64{},
		errorInds:  map[[2]string]uint64{},
		procedures: map[string][]string{},
		loaded: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ellamocn_user_plane_datapath_loaded",
			Help: "1 when the user-plane eBPF program is loaded, 0 when user-plane aggregation is not allowed or the " +
				"program was unloaded.",
		}),
	}

	for _, r := range unattributedSlowDrops {
		s.drops[r] = 0
	}

	for origin, results := range errorIndicationResults {
		for _, r := range results {
			s.errorInds[[2]string{origin, r}] = 0
		}
	}

	return s
}

func (s *stats) initFailures(o *operatorStats) {
	for iface, procs := range s.procedures {
		for _, proc := range procs {
			for _, r := range failureReasons {
				o.initFailure(failureKey{iface: iface, procedure: proc, reason: r.reason})
			}

			o.initFailure(failureKey{iface: iface, procedure: proc, reason: failureOther})
		}
	}
}

func (s *stats) addOperator(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.operators[id]; ok {
		return
	}

	o := &operatorStats{
		tunnels: map[string]int{}, setups: map[string]uint64{}, relayed: map[string]uint64{}, drops: map[string]uint64{},
		failures: map[failureKey]uint64{},
	}

	for _, d := range directions {
		o.tunnels[d], o.setups[d], o.relayed[d] = 0, 0, 0
	}

	for _, r := range operatorSlowDrops {
		o.drops[r] = 0
	}

	s.initFailures(o)
	s.operators[id] = o
}

func (s *stats) removeOperator(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.operators, id)

	for l := range s.retired {
		if l.operator == id {
			delete(s.retired, l)
		}
	}
}

func (s *stats) setProcedures(iface string, procs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.procedures[iface] = slices.Clone(procs)

	for _, o := range s.operators {
		s.initFailures(o)
	}
}

func (s *stats) tunnelOpened(operator int64, direction string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if o := s.operators[operator]; o != nil {
		o.tunnels[direction]++
		o.setups[direction]++
	}
}

func (s *stats) tunnelClosed(operator int64, direction string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if o := s.operators[operator]; o != nil && o.tunnels[direction] > 0 {
		o.tunnels[direction]--
	}
}

func (s *stats) setupFailed(operator int64, k failureKey) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if o := s.operators[operator]; o != nil {
		o.failures[k]++
	}
}

func (s *stats) slowDrop(operator int64, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if o := s.operators[operator]; o != nil {
		o.drops[reason]++
	}
}

func (s *stats) unattributedDrop(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.drops[reason]++
}

func (s *stats) errorIndication(origin, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.errorInds[[2]string{origin, result}]++
}

func (s *stats) addPath(idx uint32, l pathLabels) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.paths[idx] = l
}

func (s *stats) retirePath(idx uint32, fast Counters) {
	s.mu.Lock()
	defer s.mu.Unlock()

	l, ok := s.paths[idx]
	if !ok {
		return
	}

	c := s.retired[l]
	c.Packets += fast.Packets + s.slow[idx].Packets
	c.Bytes += fast.Bytes + s.slow[idx].Bytes
	s.retired[l] = c

	delete(s.slow, idx)
	delete(s.paths, idx)
}

func (s *stats) dropPath(idx uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.retired, s.paths[idx])
	delete(s.slow, idx)
	delete(s.paths, idx)
}

func (s *stats) forgetRadio(radio string, live map[pathLabels]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for l := range s.retired {
		if l.radio == radio && !live[l] {
			delete(s.retired, l)
		}
	}
}

func (s *stats) slowRelayed(idx uint32, operator int64, direction string, gpdu bool, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if o := s.operators[operator]; o != nil {
		o.relayed[direction]++
	}

	if !gpdu {
		return
	}

	c := s.slow[idx]
	c.Packets++
	c.Bytes += uint64(n)
	s.slow[idx] = c
}

func (s *stats) totalsLocked(fast map[uint32]Counters) map[pathLabels]Counters {
	out := make(map[pathLabels]Counters, len(s.paths)+len(s.retired))

	maps.Copy(out, s.retired)

	for idx, l := range s.paths {
		c := out[l]
		c.Packets += fast[idx].Packets + s.slow[idx].Packets
		c.Bytes += fast[idx].Bytes + s.slow[idx].Bytes
		out[l] = c
	}

	return out
}

type collector struct {
	p *Plane
}

func (p *Plane) Collectors() []prometheus.Collector {
	return []prometheus.Collector{collector{p}, p.stats.loaded}
}

func (c collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		enabledDesc, stateDesc, unavailableDesc, tunnelsDesc, setupsDesc, failuresDesc, packetsDesc, bytesDesc,
		droppedDesc, passesDesc, relayedDesc, errorIndsDesc, peerUpDesc, mapEntriesDesc, mapMaxEntriesDesc,
		mapMemoryDesc,
	} {
		ch <- d
	}
}

func gauge(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

func counter(ch chan<- prometheus.Metric, d *prometheus.Desc, v uint64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v), labels...)
}

func flag(b bool) float64 {
	if b {
		return 1
	}

	return 0
}

func (c collector) Collect(ch chan<- prometheus.Metric) {
	p := c.p

	p.mu.RLock()
	defer p.mu.RUnlock()

	gauge(ch, enabledDesc, flag(p.cfg.Enabled))

	fast, err := p.dp.paths()
	if err != nil {
		p.log.Debug("failed to read user-plane path counters", slog.Any("error", err))
	}

	drops, err := p.dp.drops()
	if err != nil {
		p.log.Debug("failed to read user-plane drop counters", slog.Any("error", err))
	}

	passes, err := p.dp.passes()
	if err != nil {
		p.log.Debug("failed to read user-plane pass counters", slog.Any("error", err))
	}

	for r := range passCount {
		var n uint64
		if r < len(passes) {
			n = passes[r]
		}

		counter(ch, passesDesc, n, passReasons[relayPassReason(r)])
	}

	p.collectOperatorsLocked(ch, drops)
	p.collectPeersLocked(ch)
	p.collectMapsLocked(ch)

	s := p.stats

	s.mu.Lock()
	defer s.mu.Unlock()

	for l, v := range s.totalsLocked(fast) {
		o, ok := p.operators[l.operator]
		if !ok {
			continue
		}

		counter(ch, packetsDesc, v.Packets, o.Name, l.radio, l.direction)
		counter(ch, bytesDesc, v.Bytes, o.Name, l.radio, l.direction)
	}

	for r, n := range s.drops {
		counter(ch, droppedDesc, n, "", pathSlow, r)
	}

	for k, n := range s.errorInds {
		counter(ch, errorIndsDesc, n, k[0], k[1])
	}
}

func (p *Plane) collectOperatorsLocked(ch chan<- prometheus.Metric, drops [][]uint64) {
	s := p.stats

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, op := range p.operators {
		name := op.Name

		if st, ok := p.status[id]; ok {
			for _, state := range states {
				gauge(ch, stateDesc, flag(st.State == state), name, string(state))
			}

			for _, r := range unavailableReasons {
				gauge(ch, unavailableDesc, flag(st.State == StateUnavailable && st.Reason == r), name, string(r))
			}
		}

		var fast []uint64
		if leg, ok := p.legs[id]; ok && int(leg) < len(drops) {
			fast = drops[leg]
		}

		for r := range dropCount {
			var n uint64
			if r < len(fast) {
				n = fast[r]
			}

			counter(ch, droppedDesc, n, name, pathFast, dropReasons[relayDropReason(r)])
		}

		o := s.operators[id]
		if o == nil {
			continue
		}

		for _, d := range directions {
			gauge(ch, tunnelsDesc, float64(o.tunnels[d]), name, d)
			counter(ch, setupsDesc, o.setups[d], name, d)
			counter(ch, relayedDesc, o.relayed[d], name, d)
		}

		for r, n := range o.drops {
			counter(ch, droppedDesc, n, name, pathSlow, r)
		}

		for k, n := range o.failures {
			counter(ch, failuresDesc, n, name, k.iface, k.procedure, k.reason)
		}
	}
}

func (p *Plane) collectPeersLocked(ch chan<- prometheus.Metric) {
	for k := range p.peers {
		o, ok := p.operators[k.operator]
		if !ok || p.own[k.addr] {
			continue
		}

		gauge(ch, peerUpDesc, flag(p.sp.up(k.addr)), o.Name, k.side.label(), k.addr.String())
	}
}

func (p *Plane) collectMapsLocked(ch chan<- prometheus.Metric) {
	entries := map[string]int{
		relayMapTunnels: len(p.tunnels), relayMapTunnelStats: len(p.tunnels), relayMapIfaces: len(p.ifaceLegs),
		relayMapPathStats: len(p.paths),
	}

	for name, u := range p.dp.mapUsage() {
		gauge(ch, mapMemoryDesc, float64(u.memlock), name)

		if slices.Contains(trackedMaps, name) {
			gauge(ch, mapEntriesDesc, float64(entries[name]), name)
			gauge(ch, mapMaxEntriesDesc, float64(u.maxEntries), name)
		}
	}
}

type peerKey struct {
	operator int64
	side     Side
	addr     netip.Addr
}

var failureReasons = []struct {
	err    error
	reason string
}{
	{ErrUnavailable, "user_plane_unavailable"},
	{ErrNoRoute, "no_route"},
	{ErrRouteMismatch, "route_mismatch"},
	{ErrNoAddress, "no_address"},
	{ErrNoTEID, "no_teid"},
	{ErrTooManyPaths, "too_many_paths"},
	{ErrInvalidAddress, "invalid_address"},
	{ErrUnknownEndpoint, "unknown_endpoint"},
}

const failureOther = "other"

func failureReason(err error) string {
	for _, r := range failureReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}

	return failureOther
}

func (p *Plane) TunnelProcedures(iface string, procedures ...string) {
	p.stats.setProcedures(iface, procedures)
}

func (p *Plane) TunnelSetupFailed(operator int64, iface, procedure string, err error) {
	p.stats.setupFailed(operator, failureKey{iface: iface, procedure: procedure, reason: failureReason(err)})
}
