package gtpu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ellanetworks/mocn/internal/metrics"
)

type Side uint8

const (
	RAN Side = iota + 1
	Core
)

func (s Side) other() Side {
	if s == RAN {
		return Core
	}

	return RAN
}

func (s Side) label() string {
	if s == RAN {
		return metrics.SideRadio
	}

	return metrics.SideCore
}

const (
	teidMask          = 1<<28 - 1
	ranLeg            = 0
	defaultQuarantine = 10 * time.Second
	maxTunnels        = 1 << 20
	maxPaths          = 1 << 14
)

var forwardingSysctls = []string{"/proc/sys/net/ipv4/ip_forward", "/proc/sys/net/ipv6/conf/all/forwarding"}

var (
	ErrNoAddress       = errors.New("no Ella MOCN user-plane address of the peer's IP version")
	ErrNoRoute         = errors.New("no route to")
	ErrRouteMismatch   = errors.New("the route to the peer leaves by another interface than the operator's")
	ErrUnavailable     = errors.New("the operator's user plane is unavailable")
	ErrUnknownEndpoint = errors.New("not a tunnel endpoint of Ella MOCN")
	ErrInvalidAddress  = errors.New("invalid transport layer address")
	ErrNoTEID          = errors.New("no free TEID")
	ErrTooManyPaths    = errors.New("too many user-plane paths")
	ErrTooManyRelayed  = errors.New("too many operators relay the user plane")
)

type Endpoint struct {
	Addr netip.Addr
	TEID uint32
}

type Operator struct {
	ID         int64
	Name       string
	Aggregated bool
}

type Config struct {
	RANAddress   netip.Addr
	RANInterface string
	Enabled      bool
	Quarantine   time.Duration
	Logger       *slog.Logger
	Now          func() time.Time
	Rand         func() uint32
	Host         Host
}

type Path struct {
	Operator int64
	Radio    string
	derived  map[bool]netip.Addr
}

type Tunnel struct {
	TEID       uint32
	Local      netip.Addr
	Ingress    Side
	Egress     Side
	Source     netip.Addr
	Peer       Endpoint
	Operator   int64
	Radio      string
	forwarding bool
	inIndex    int
	outIndex   int
	path       uint32
	slowPkts   uint64
	slowLen    uint64
	lastSrc    netip.Addr
}

func (t Tunnel) Endpoint() Endpoint {
	return Endpoint{Addr: t.Local, TEID: t.TEID}
}

type pathKey struct {
	operator int64
	radio    string
	ingress  Side
}

type pathEntry struct {
	idx  uint32
	refs int
	idle time.Time
	gone bool
}

type Plane struct {
	cfg Config
	log *slog.Logger
	dp  datapath
	sp  *slowPath

	host Host

	mu         sync.RWMutex
	operators  map[int64]Operator
	legs       map[int64]uint8
	legIface   map[int64]string
	anchors    map[int64]map[any]Anchor
	resolved   map[int64]resolution
	status     map[int64]Status
	ranIndex   int
	ifaceLegs  map[string]uint64
	own        map[netip.Addr]bool
	tunnels    map[uint32]*Tunnel
	byPeer     map[Endpoint][]uint32
	quarantine map[uint32]time.Time
	paths      map[pathKey]*pathEntry
	freePaths  []uint32
	nextPath   uint32
	peers      map[peerKey]int
	stats      *stats
}

func New(cfg Config) *Plane {
	if cfg.Quarantine == 0 {
		cfg.Quarantine = defaultQuarantine
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.Rand == nil {
		cfg.Rand = rand.Uint32
	}

	if cfg.Host == nil {
		cfg.Host = NetlinkHost{}
	}

	p := &Plane{
		cfg:        cfg,
		log:        cfg.Logger.With(slog.String("component", "user_plane")),
		dp:         noDatapath{},
		host:       cfg.Host,
		operators:  map[int64]Operator{},
		legs:       map[int64]uint8{},
		legIface:   map[int64]string{},
		anchors:    map[int64]map[any]Anchor{},
		resolved:   map[int64]resolution{},
		status:     map[int64]Status{},
		ifaceLegs:  map[string]uint64{},
		own:        map[netip.Addr]bool{},
		tunnels:    map[uint32]*Tunnel{},
		byPeer:     map[Endpoint][]uint32{},
		quarantine: map[uint32]time.Time{},
		paths:      map[pathKey]*pathEntry{},
		peers:      map[peerKey]int{},
		stats:      newStats(),
	}
	p.sp = newSlowPath(p, nil)

	if cfg.RANAddress.IsValid() {
		p.own[cfg.RANAddress.Unmap()] = true
	}

	return p
}

func (p *Plane) Enabled() bool {
	return p.cfg.Enabled
}

func (p *Plane) Start(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}

	if err := enableForwarding(); err != nil {
		return err
	}

	dp, err := loadDatapath(ctx, p.log)
	if err != nil {
		return fmt.Errorf("load the user-plane datapath: %w", err)
	}

	if err := p.startDatapath(ctx, dp); err != nil {
		return errors.Join(err, dp.close())
	}

	go p.expire(ctx)
	go p.sp.echoLoop(ctx)
	go p.follow(ctx, dp.changes())

	go func() {
		<-ctx.Done()
		p.close()
	}()

	return nil
}

func enableForwarding() error {
	for _, f := range forwardingSysctls {
		if cur, err := os.ReadFile(f); err == nil && strings.TrimSpace(string(cur)) == "1" { // #nosec G304
			continue
		}

		if err := os.WriteFile(f, []byte("1"), 0o600); err != nil {
			return fmt.Errorf("enable IP forwarding: %w", err)
		}
	}

	return nil
}

func (p *Plane) startDatapath(ctx context.Context, dp datapath) error {
	on, err := policyBasedIPsec()
	if err != nil {
		p.log.WarnContext(ctx, "failed to read IPsec policies, assuming policy-based IPsec", slog.Any("error", err))
	}

	if err := dp.setPolicyIPsec(on || err != nil); err != nil {
		return fmt.Errorf("configure the user-plane datapath: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.dp = dp

	if p.cfg.RANInterface != "" {
		if err := p.setLegLocked(p.cfg.RANInterface, ranLeg, true); err != nil {
			return err
		}
	}

	p.refreshLocked()
	p.stats.loaded.Set(1)

	go p.watchIPsec(ctx, dp)

	return nil
}

func (p *Plane) close() {
	p.sp.close()

	p.mu.Lock()
	defer p.mu.Unlock()

	p.stats.loaded.Set(0)

	if err := p.dp.close(); err != nil {
		p.log.Warn("failed to close the user-plane datapath", slog.Any("error", err))
	}
}

func (p *Plane) follow(ctx context.Context, changes <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			p.refresh()
		}
	}
}

func (p *Plane) expire(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		now := p.cfg.Now()

		p.mu.Lock()

		for teid, until := range p.quarantine {
			if !now.Before(until) {
				delete(p.quarantine, teid)
			}
		}

		p.mu.Unlock()
	}
}

func (p *Plane) SetOperators(ops []Operator) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	next := map[int64]Operator{}
	for _, o := range ops {
		next[o.ID] = o
	}

	var errs []error

	for id := range p.operators {
		if _, ok := next[id]; !ok {
			errs = append(errs, p.retireLocked(id))
		}
	}

	for _, o := range ops {
		p.stats.addOperator(o.ID)
	}

	for id := range p.status {
		if _, ok := next[id]; !ok {
			delete(p.status, id)
			delete(p.resolved, id)
		}
	}

	p.operators = next

	used := map[uint8]bool{}
	for _, leg := range p.legs {
		used[leg] = true
	}

	for _, o := range ops {
		if _, ok := p.legs[o.ID]; ok || !o.Aggregated {
			continue
		}

		leg := uint8(1)
		for ; int(leg) < maxLegs && used[leg]; leg++ {
		}

		if int(leg) == maxLegs {
			errs = append(errs, ErrTooManyRelayed)
			continue
		}

		used[leg] = true
		p.legs[o.ID] = leg
	}

	p.refreshLocked()

	return errors.Join(errs...)
}

func (p *Plane) retireLocked(id int64) error {
	var teids []uint32

	for teid, t := range p.tunnels {
		if t.Operator == id {
			teids = append(teids, teid)
		}
	}

	p.closeLocked(teids)

	for k, e := range p.paths {
		if k.operator == id {
			p.prunePathLocked(k, e)
		}
	}

	p.stats.removeOperator(id)

	leg, ok := p.legs[id]
	if !ok {
		return nil
	}

	delete(p.legs, id)
	delete(p.legIface, id)

	return errors.Join(p.clearLegLocked(leg), p.dp.resetLeg(leg))
}

func (p *Plane) clearLegLocked(leg uint8) error {
	var errs []error

	for name, legs := range p.ifaceLegs {
		if legs&(1<<leg) != 0 {
			errs = append(errs, p.setLegLocked(name, leg, false))
		}
	}

	return errors.Join(errs...)
}

func (p *Plane) setLegLocked(name string, leg uint8, on bool) error {
	legs := p.ifaceLegs[name] &^ (1 << leg)
	if on {
		legs |= 1 << leg
	}

	if legs == p.ifaceLegs[name] {
		_, known := p.ifaceLegs[name]
		if known || legs == 0 {
			return nil
		}
	}

	if err := p.dp.setInterface(name, legs); err != nil {
		return err
	}

	if legs == 0 {
		delete(p.ifaceLegs, name)
	} else {
		p.ifaceLegs[name] = legs
	}

	return nil
}

func (p *Plane) Path(operator int64, radio string) (Path, bool) {
	p.mu.RLock()
	o := p.operators[operator]
	p.mu.RUnlock()

	if !o.Aggregated {
		return Path{}, false
	}

	return Path{Operator: operator, Radio: radio, derived: map[bool]netip.Addr{}}, true
}

// TS 36.414 clause 5.1
// TS 38.414 clause 5.1
func Addresses(tla []byte) (v4, v6 netip.Addr) {
	switch len(tla) {
	case 4:
		v4 = netip.AddrFrom4([4]byte(tla))
	case 16:
		v6 = netip.AddrFrom16([16]byte(tla))
	case 20:
		v4 = netip.AddrFrom4([4]byte(tla[:4]))
		v6 = netip.AddrFrom16([16]byte(tla[4:]))
	}

	return v4, v6
}

func (p *Plane) Open(path Path, to Side, tla []byte, teid uint32) (Tunnel, error) {
	return p.open(path, to, tla, teid, false)
}

// TS 36.300 clause 4.6.2
func (p *Plane) open(path Path, to Side, tla []byte, teid uint32, forwarding bool) (Tunnel, error) {
	from := to.other()
	v4, v6 := Addresses(tla)

	if !v4.IsValid() && !v6.IsValid() {
		return Tunnel{}, ErrInvalidAddress
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	res := p.resolved[path.Operator]
	if !res.ok {
		return Tunnel{}, ErrUnavailable
	}

	var errs []error

	for _, local := range p.addrsLocked(path, res, from) {
		peer := v6
		if local.Is4() {
			peer = v4
		}

		if !peer.IsValid() {
			continue
		}

		if t, own := p.ownLocked(peer, teid); own {
			switch {
			case t == nil || to != Core || t.Ingress != Core:
				return Tunnel{}, ErrUnknownEndpoint
			case t.Operator == path.Operator:
				return p.relayToRadioLocked(path, res, local, t.Peer, forwarding)
			}

			src, ok := sameFamily(p.addrsLocked(path, res, to), local)
			if !ok {
				continue
			}

			return p.openLocked(path, res, from, to, local, src, Endpoint{Addr: peer, TEID: teid}, 0, forwarding)
		}

		r, err := p.host.Route(peer)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		src, ok := sameFamily(p.addrsLocked(path, res, to), local)
		if !ok && to == Core && r.Src.Is4() == local.Is4() && r.Src.IsGlobalUnicast() {
			src, ok = r.Src, true
		}

		if !ok {
			continue
		}

		if r.Ifindex != p.indexLocked(res, to) {
			errs = append(errs, fmt.Errorf("%w: %s", ErrRouteMismatch, peer))
			continue
		}

		if to == Core && path.derived != nil {
			path.derived[src.Is4()] = src
		}

		return p.openLocked(path, res, from, to, local, src, Endpoint{Addr: peer, TEID: teid}, r.Ifindex, forwarding)
	}

	if len(errs) > 0 {
		return Tunnel{}, errors.Join(errs...)
	}

	return Tunnel{}, ErrNoAddress
}

func (p *Plane) relayToRadioLocked(path Path, res resolution, local netip.Addr, peer Endpoint, forwarding bool,
) (Tunnel, error) {
	r, err := p.host.Route(peer.Addr)
	if err != nil {
		return Tunnel{}, err
	}

	if r.Ifindex != p.indexLocked(res, RAN) {
		return Tunnel{}, fmt.Errorf("%w: %s", ErrRouteMismatch, peer.Addr)
	}

	return p.openLocked(path, res, RAN, RAN, local, local, peer, r.Ifindex, forwarding)
}

func (p *Plane) addrsLocked(path Path, res resolution, s Side) []netip.Addr {
	if s == RAN {
		if p.cfg.RANAddress.IsValid() {
			return []netip.Addr{p.cfg.RANAddress.Unmap()}
		}

		return nil
	}

	out := slices.Clone(res.addrs)

	for _, v4 := range []bool{true, false} {
		if _, ok := res.addr(v4); !ok {
			if a, ok := path.derived[v4]; ok {
				out = append(out, a)
			}
		}
	}

	return out
}

func (p *Plane) indexLocked(res resolution, s Side) int {
	if s == RAN {
		return p.ranIndex
	}

	return res.ifindex
}

func sameFamily(addrs []netip.Addr, like netip.Addr) (netip.Addr, bool) {
	for _, a := range addrs {
		if a.Is4() == like.Is4() {
			return a, true
		}
	}

	return netip.Addr{}, false
}

func (p *Plane) ownLocked(a netip.Addr, teid uint32) (*Tunnel, bool) {
	if t := p.tunnels[teid]; t != nil && t.Local == a {
		return t, true
	}

	return nil, p.own[a]
}

func (p *Plane) openLocked(path Path, res resolution, ingress, egress Side, local, src netip.Addr, peer Endpoint,
	outIndex int, forwarding bool,
) (Tunnel, error) {
	leg, ok := p.legOf(ingress, path.Operator)
	if !ok {
		return Tunnel{}, ErrUnavailable
	}

	if err := p.sp.listen(local); err != nil {
		return Tunnel{}, err
	}

	if err := p.sp.listen(src); err != nil {
		return Tunnel{}, err
	}

	p.own[local], p.own[src] = true, true

	teid, err := p.allocLocked()
	if err != nil {
		return Tunnel{}, err
	}

	idx, err := p.pathLocked(pathKey{operator: path.Operator, radio: path.Radio, ingress: ingress})
	if err != nil {
		return Tunnel{}, err
	}

	t := &Tunnel{
		TEID: teid, Local: local, Ingress: ingress, Egress: egress, Source: src, Peer: peer, Operator: path.Operator,
		Radio: path.Radio, forwarding: forwarding, inIndex: p.indexLocked(res, ingress), outIndex: outIndex, path: idx,
	}

	if err := p.dp.put(teid, entryOf(t, leg, p.legs[path.Operator])); err != nil {
		p.unrefPathLocked(idx)
		return Tunnel{}, fmt.Errorf("program the user-plane datapath: %w", err)
	}

	p.tunnels[teid] = t
	p.byPeer[peer] = append(p.byPeer[peer], teid)
	p.stats.tunnelOpened(path.Operator, ingress.direction())

	if !forwarding {
		p.peers[peerKey{operator: path.Operator, side: egress, addr: peer.Addr}]++
	}

	return *t, nil
}

func (s Side) direction() string {
	if s == RAN {
		return "uplink"
	}

	return "downlink"
}

func (p *Plane) legOf(s Side, operator int64) (uint8, bool) {
	if s == RAN {
		return ranLeg, true
	}

	leg, ok := p.legs[operator]

	return leg, ok
}

func (p *Plane) allocLocked() (uint32, error) {
	for range 64 {
		teid := p.cfg.Rand() & teidMask
		if teid == 0 {
			continue
		}

		if _, used := p.tunnels[teid]; used {
			continue
		}

		if until, ok := p.quarantine[teid]; ok && p.cfg.Now().Before(until) {
			continue
		}

		delete(p.quarantine, teid)

		return teid, nil
	}

	return 0, ErrNoTEID
}

func (p *Plane) pathLocked(k pathKey) (uint32, error) {
	if e := p.paths[k]; e != nil {
		e.refs++
		e.gone = false

		return e.idx, nil
	}

	idx, err := p.freePathLocked()
	if err != nil {
		return 0, err
	}

	p.paths[k] = &pathEntry{idx: idx, refs: 1}
	p.stats.addPath(idx, pathLabels{operator: k.operator, radio: k.radio, direction: k.ingress.direction()})

	return idx, nil
}

func (p *Plane) freePathLocked() (uint32, error) {
	if n := len(p.freePaths); n > 0 {
		idx := p.freePaths[n-1]
		if err := p.dp.resetPath(idx); err != nil {
			return 0, err
		}

		p.freePaths = p.freePaths[:n-1]

		return idx, nil
	}

	if p.nextPath < maxPaths {
		p.nextPath++
		return p.nextPath - 1, nil
	}

	now := p.cfg.Now()

	var (
		oldest pathKey
		found  *pathEntry
	)

	for k, e := range p.paths {
		if e.refs == 0 && !now.Before(e.idle.Add(p.cfg.Quarantine)) && (found == nil || e.idle.Before(found.idle)) {
			oldest, found = k, e
		}
	}

	if found == nil {
		return 0, ErrTooManyPaths
	}

	fast, err := p.dp.paths()
	if err != nil {
		return 0, err
	}

	if err := p.dp.resetPath(found.idx); err != nil {
		return 0, err
	}

	p.stats.retirePath(found.idx, fast[found.idx])
	delete(p.paths, oldest)

	return found.idx, nil
}

func (p *Plane) unrefPathLocked(idx uint32) {
	for k, e := range p.paths {
		if e.idx != idx {
			continue
		}

		e.refs--

		switch {
		case e.refs > 0:
		case e.gone:
			p.prunePathLocked(k, e)
		default:
			e.idle = p.cfg.Now()
		}

		return
	}
}

func (p *Plane) prunePathLocked(k pathKey, e *pathEntry) {
	delete(p.paths, k)
	p.freePaths = append(p.freePaths, e.idx)
	p.stats.dropPath(e.idx)
}

func (p *Plane) ForgetRadio(radio string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	live := map[pathLabels]bool{}

	for k, e := range p.paths {
		if k.radio != radio {
			continue
		}

		if e.refs > 0 {
			e.gone = true
			live[pathLabels{operator: k.operator, radio: k.radio, direction: k.ingress.direction()}] = true

			continue
		}

		p.prunePathLocked(k, e)
	}

	p.stats.forgetRadio(radio, live)
}

func (p *Plane) Reverse(from Side, tla []byte, teid uint32) (Endpoint, error) {
	v4, v6 := Addresses(tla)

	p.mu.RLock()
	defer p.mu.RUnlock()

	t := p.tunnels[teid]
	if t == nil || t.Ingress != from || (t.Local != v4 && t.Local != v6) {
		return Endpoint{}, ErrUnknownEndpoint
	}

	return t.Peer, nil
}

func (p *Plane) Get(teid uint32) (Tunnel, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	t := p.tunnels[teid]
	if t == nil {
		return Tunnel{}, false
	}

	return *t, true
}

func (p *Plane) Close(teids ...uint32) {
	if len(teids) == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.closeLocked(teids)
}

func (p *Plane) closeLocked(teids []uint32) {
	until := p.cfg.Now().Add(p.cfg.Quarantine)

	for _, teid := range teids {
		t := p.tunnels[teid]
		if t == nil {
			continue
		}

		if err := p.dp.remove(teid); err != nil {
			p.log.Warn("failed to remove a tunnel from the user-plane datapath", slog.Uint64("teid", uint64(teid)),
				slog.Any("error", err))
		}

		delete(p.tunnels, teid)

		peers := slices.DeleteFunc(p.byPeer[t.Peer], func(x uint32) bool { return x == teid })
		if len(peers) == 0 {
			delete(p.byPeer, t.Peer)
		} else {
			p.byPeer[t.Peer] = peers
		}

		p.quarantine[teid] = until
		p.unrefPathLocked(t.path)
		p.stats.tunnelClosed(t.Operator, t.Ingress.direction())

		if !t.forwarding {
			k := peerKey{operator: t.Operator, side: t.Egress, addr: t.Peer.Addr}
			if p.peers[k]--; p.peers[k] <= 0 {
				delete(p.peers, k)
			}
		}
	}
}

func (p *Plane) move(teids []uint32, path Path) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, teid := range teids {
		t := p.tunnels[teid]
		if t == nil || t.Radio == path.Radio {
			continue
		}

		idx, err := p.pathLocked(pathKey{operator: t.Operator, radio: path.Radio, ingress: t.Ingress})
		if err == nil {
			err = p.dp.setPath(teid, idx)
			if err != nil {
				p.unrefPathLocked(idx)
			}
		}

		if err != nil {
			p.log.Warn("failed to move a tunnel to the radio that serves the UE", slog.Uint64("teid", uint64(teid)),
				slog.String("radio", path.Radio), slog.Any("error", err))

			continue
		}

		p.unrefPathLocked(t.path)
		t.path, t.Radio = idx, path.Radio
	}
}

func (p *Plane) quarantined(teid uint32) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	until, ok := p.quarantine[teid]

	return ok && p.cfg.Now().Before(until)
}

func (p *Plane) Tunnels() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.tunnels)
}

type Counters struct {
	Packets uint64
	Bytes   uint64
}

func (p *Plane) Counters(teid uint32) Counters {
	p.mu.RLock()
	defer p.mu.RUnlock()

	t := p.tunnels[teid]
	if t == nil {
		return Counters{}
	}

	c, _, err := p.dp.tunnel(teid)
	if err != nil {
		c = Counters{}
	}

	c.Packets += t.slowPkts
	c.Bytes += t.slowLen

	return c
}

func (p *Plane) byPeerEndpoint(e Endpoint) []Tunnel {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var out []Tunnel

	for _, teid := range p.byPeer[e] {
		if t := p.tunnels[teid]; t != nil {
			out = append(out, *t)
		}
	}

	return out
}

func (p *Plane) returnAddress(teid uint32) (netip.Addr, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	t := p.tunnels[teid]
	if t == nil {
		return netip.Addr{}, false
	}

	if _, last, err := p.dp.tunnel(teid); err == nil && last.IsValid() {
		return last, true
	}

	return t.lastSrc, t.lastSrc.IsValid()
}

func (p *Plane) slowRelayed(teid uint32, from netip.Addr, gpdu bool, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	t := p.tunnels[teid]
	if t == nil {
		return
	}

	if gpdu {
		t.slowPkts++
		t.slowLen += uint64(n)
	}

	t.lastSrc = from
	p.stats.slowRelayed(t.path, t.Operator, t.Ingress.direction(), gpdu, n)
}

func (p *Plane) egressIndex(t Tunnel) (int, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.own[t.Peer.Addr] {
		return 0, true
	}

	return t.outIndex, t.outIndex != 0
}

func (p *Plane) arrivedOn(t Tunnel, s Side, index int, from netip.Addr) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if index == loopbackIf && p.own[from] {
		return true
	}

	leg, ok := p.legOf(s, t.Operator)
	if !ok {
		return false
	}

	legs, ok := p.dp.interfaceLegs(index)

	return ok && legs&(1<<leg) != 0
}

type echoPath struct {
	source netip.Addr
	index  int
}

func (p *Plane) peersInUse() map[netip.Addr]echoPath {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := map[netip.Addr]echoPath{}

	for _, t := range p.tunnels {
		if !t.forwarding && !p.own[t.Peer.Addr] {
			out[t.Peer.Addr] = echoPath{source: t.Source, index: t.outIndex}
		}
	}

	return out
}

func (p *Plane) knownInterface(index int) bool {
	if index == loopbackIf {
		return true
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	_, ok := p.dp.interfaceLegs(index)

	return ok
}
