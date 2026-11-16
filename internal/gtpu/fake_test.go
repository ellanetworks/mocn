package gtpu

import (
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	mme     = netip.MustParseAddr("10.2.0.10")
	mmeB    = netip.MustParseAddr("10.3.0.10")
	coreB   = netip.MustParseAddr("10.3.0.1")
	core6GW = netip.MustParseAddr("2001:db8:2::1")
)

type fakeHost struct {
	routes  map[netip.Prefix]Route
	names   map[int]string
	lookups atomic.Int64
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		routes: map[netip.Prefix]Route{
			netip.MustParsePrefix("10.1.0.0/16"):     {Src: ranGW, Ifindex: 10},
			netip.MustParsePrefix("10.2.0.0/16"):     {Src: coreGW, Ifindex: 20},
			netip.MustParsePrefix("10.3.0.0/16"):     {Src: coreB, Ifindex: 21},
			netip.MustParsePrefix("2001:db8:1::/48"): {Src: ran6GW, Ifindex: 10},
			netip.MustParsePrefix("2001:db8::/32"):   {Src: core6GW, Ifindex: 20},
		},
		names: map[int]string{10: "ran0", 20: "core0", 21: "core1"},
	}
}

func (f *fakeHost) Route(dst netip.Addr) (Route, error) {
	f.lookups.Add(1)

	var (
		best  netip.Prefix
		found bool
	)

	for p := range f.routes {
		if p.Contains(dst) && (!found || p.Bits() > best.Bits()) {
			best, found = p, true
		}
	}

	if !found {
		return Route{}, fmt.Errorf("%w %s", ErrNoRoute, dst)
	}

	return f.routes[best], nil
}

func (f *fakeHost) InterfaceName(index int) (string, error) {
	if n, ok := f.names[index]; ok {
		return n, nil
	}

	return "", fmt.Errorf("no interface %d", index)
}

func (f *fakeHost) InterfaceIndex(name string) (int, error) {
	for i, n := range f.names {
		if n == name {
			return i, nil
		}
	}

	return 0, fmt.Errorf("no interface %s", name)
}

func anchorOf(t *testing.T, h Host, peer netip.Addr) Anchor {
	t.Helper()

	r, name, err := Resolve(h, peer)
	if err != nil {
		t.Fatal(err)
	}

	return Anchor{Peer: peer, Interface: name, Addresses: []netip.Addr{r.Src}}
}

type fakeDatapath struct {
	tunnels   map[uint32]relayTunnel
	ifaces    map[string]uint64
	index     map[string]int
	changed   chan struct{}
	fail      map[string]error
	stats     map[uint32]Counters
	last      map[uint32]netip.Addr
	resets    []uint8
	pathStats map[uint32]Counters
	puts      int
	removes   int
}

func newFakeDatapath() *fakeDatapath {
	return &fakeDatapath{
		tunnels: map[uint32]relayTunnel{}, ifaces: map[string]uint64{}, stats: map[uint32]Counters{},
		last: map[uint32]netip.Addr{}, pathStats: map[uint32]Counters{},
		index: map[string]int{"ran0": 10, "core0": 20, "core1": 21}, changed: make(chan struct{}, 1),
	}
}

func (f *fakeDatapath) setInterface(name string, legs uint64) error {
	if err := f.fail[name]; err != nil && legs != 0 {
		return err
	}

	if legs == 0 {
		delete(f.ifaces, name)
		return nil
	}

	f.ifaces[name] = legs

	return nil
}

func (f *fakeDatapath) interfaceLegs(index int) (uint64, bool) {
	for name, i := range f.index {
		if i == index {
			legs, ok := f.ifaces[name]
			return legs, ok
		}
	}

	return 0, false
}

func (f *fakeDatapath) changes() <-chan struct{} { return f.changed }

func (f *fakeDatapath) mapUsage() map[string]mapUsage {
	return map[string]mapUsage{
		relayMapTunnels: {maxEntries: maxTunnels, memlock: 4096}, relayMapTunnelStats: {maxEntries: maxTunnels, memlock: 2048},
		relayMapIfaces:    {maxEntries: uint32(maxIfaces), memlock: 512},
		relayMapPathStats: {maxEntries: maxPaths, memlock: 1024},
		relayMapDrops:     {maxEntries: uint32(maxLegs * dropCount), memlock: 256},
	}
}

func (f *fakeDatapath) put(teid uint32, e relayTunnel) error {
	f.puts++
	f.tunnels[teid] = e

	return nil
}

func (f *fakeDatapath) remove(teid uint32) error {
	if _, ok := f.tunnels[teid]; ok {
		f.removes++
	}

	delete(f.tunnels, teid)

	return nil
}

func (f *fakeDatapath) setPath(teid, idx uint32) error {
	e, ok := f.tunnels[teid]
	if !ok {
		return fmt.Errorf("no tunnel %#x", teid)
	}

	e.Path = idx
	f.tunnels[teid] = e

	return nil
}

func (f *fakeDatapath) setPolicyIPsec(bool) error { return nil }

func (f *fakeDatapath) tunnel(teid uint32) (Counters, netip.Addr, error) {
	return f.stats[teid], f.last[teid], nil
}

func (f *fakeDatapath) paths() (map[uint32]Counters, error) { return f.pathCounters(), nil }

func (f *fakeDatapath) pathCounters() map[uint32]Counters {
	out := map[uint32]Counters{}
	for k, v := range f.pathStats {
		out[k] = v
	}

	return out
}

func (f *fakeDatapath) resetPath(idx uint32) error {
	delete(f.pathStats, idx)
	return nil
}

func (f *fakeDatapath) drops() ([][]uint64, error) { return nil, nil }

func (f *fakeDatapath) resetLeg(leg uint8) error {
	f.resets = append(f.resets, leg)
	return nil
}

func (f *fakeDatapath) passes() ([]uint64, error) { return nil, nil }
func (f *fakeDatapath) close() error              { return nil }

type sent struct {
	local, src, dst netip.Addr
	via             int
	to              netip.AddrPort
	tos             uint8
	ifindex         int
	b               []byte
}

type fakeSender struct {
	udps    []sent
	raws    []sent
	locals  []netip.Addr
	closed  bool
	failRaw error
}

func (s *fakeSender) udp(local netip.Addr, ifindex int, to netip.AddrPort, msg []byte) error {
	s.udps = append(s.udps, sent{local: local, via: ifindex, to: to, b: append([]byte(nil), msg...)})
	return nil
}

func (s *fakeSender) raw(src, dst netip.Addr, tos uint8, ifindex int, udp []byte) error {
	if s.failRaw != nil {
		return s.failRaw
	}

	s.raws = append(s.raws, sent{src: src, dst: dst, tos: tos, ifindex: ifindex, b: append([]byte(nil), udp...)})

	return nil
}

func (s *fakeSender) listen(local netip.Addr, _ func(inbound)) error {
	s.locals = append(s.locals, local)
	return nil
}

func (s *fakeSender) close() { s.closed = true }

func (s *fakeSender) reset() {
	s.udps, s.raws = nil, nil
}

type harness struct {
	p     *Plane
	dp    *fakeDatapath
	host  *fakeHost
	out   *fakeSender
	clock *clock
}

func newHarness(t *testing.T, ops ...Operator) *harness {
	t.Helper()

	c := &clock{t: time.Unix(1000, 0)}
	n := uint32(0)
	host := newFakeHost()
	p := New(Config{
		RANAddress: ranGW, RANInterface: "ran0", Enabled: true, Now: c.now, Host: host,
		Rand: func() uint32 { n++; return 0x1000 + n },
	})

	h := &harness{p: p, dp: newFakeDatapath(), host: host, out: &fakeSender{}, clock: c}
	p.dp = h.dp
	p.sp = newSlowPath(p, h.out)

	if err := p.setLegLocked("ran0", ranLeg, true); err != nil {
		t.Fatal(err)
	}

	anchored := len(ops) == 0
	if anchored {
		ops = []Operator{{ID: 7, Name: "op", Aggregated: true}}
	}

	if err := p.SetOperators(ops); err != nil {
		t.Fatal(err)
	}

	if anchored {
		h.anchor(t, 7, mme)
	}

	return h
}

func (h *harness) anchor(t *testing.T, op int64, peer netip.Addr) {
	t.Helper()

	h.p.Anchor(op, peer, anchorOf(t, h.host, peer))
}

func (h *harness) path(t *testing.T, op int64) Path {
	t.Helper()

	path, ok := h.p.Path(op, "eNB:1")
	if !ok {
		t.Fatal("no path")
	}

	return path
}

func (h *harness) symmetric(t *testing.T) {
	t.Helper()

	if len(h.dp.tunnels) != h.p.Tunnels() {
		t.Fatalf("datapath has %d tunnels, plane has %d", len(h.dp.tunnels), h.p.Tunnels())
	}

	for teid := range h.dp.tunnels {
		if _, ok := h.p.Get(teid); !ok {
			t.Fatalf("datapath tunnel %#x unknown to the plane", teid)
		}
	}

	if h.dp.puts-h.dp.removes != len(h.dp.tunnels) {
		t.Fatalf("%d puts, %d removes, %d tunnels", h.dp.puts, h.dp.removes, len(h.dp.tunnels))
	}

	h.p.mu.RLock()
	defer h.p.mu.RUnlock()

	if len(h.p.pathAt) != len(h.p.paths) {
		t.Fatalf("%d paths by index, %d paths", len(h.p.pathAt), len(h.p.paths))
	}

	for k, e := range h.p.paths {
		if e.key != k || h.p.pathAt[e.idx] != e {
			t.Fatalf("path %+v at index %d not indexed", k, e.idx)
		}
	}

	peered := 0

	for peer, teids := range h.p.byPeer {
		if len(teids) == 0 {
			t.Fatalf("peer %v indexed without tunnels", peer)
		}

		for teid := range teids {
			if tun := h.p.tunnels[teid]; tun == nil || tun.Peer != peer {
				t.Fatalf("tunnel %#x indexed under peer %v", teid, peer)
			}

			peered++
		}
	}

	if peered != len(h.p.tunnels) {
		t.Fatalf("%d tunnels by peer, %d tunnels", peered, len(h.p.tunnels))
	}
}

type series struct {
	labels map[string]string
	value  float64
}

func gather(t *testing.T, p *Plane) map[string][]series {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(p.Collectors()...)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	out := map[string][]series{}

	for _, f := range families {
		for _, m := range f.GetMetric() {
			s := series{labels: map[string]string{}, value: m.GetCounter().GetValue() + m.GetGauge().GetValue()}
			for _, l := range m.GetLabel() {
				s.labels[l.GetName()] = l.GetValue()
			}

			out[f.GetName()] = append(out[f.GetName()], s)
		}
	}

	return out
}

func matching(all []series, labels map[string]string) []series {
	var out []series

	for _, s := range all {
		if subset(labels, s.labels) {
			out = append(out, s)
		}
	}

	return out
}

func subset(want, have map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}

	return true
}

func wantSample(t *testing.T, p *Plane, name string, labels map[string]string, want float64) {
	t.Helper()

	got := matching(gather(t, p)[name], labels)
	if len(got) != 1 || got[0].value != want {
		t.Fatalf("%s%v = %+v, want one series at %v", name, labels, got, want)
	}
}

func wantAbsent(t *testing.T, p *Plane, name string, labels map[string]string) {
	t.Helper()

	if got := matching(gather(t, p)[name], labels); len(got) != 0 {
		t.Fatalf("%s%v = %+v, want no series", name, labels, got)
	}
}
