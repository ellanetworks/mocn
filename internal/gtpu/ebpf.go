package gtpu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const linkResubscribe = time.Second

type attachment struct {
	index int
	flags uint32
	link  link.Link
}

type ebpfDatapath struct {
	log  *slog.Logger
	objs relayObjects
	cpus int

	mu       sync.RWMutex
	closed   bool
	want     map[string]uint64
	attached map[string]*attachment
	done     chan struct{}
	changed  chan struct{}
	stopped  sync.WaitGroup
}

func loadDatapath(ctx context.Context, log *slog.Logger) (*ebpfDatapath, error) {
	spec, err := loadRelay()
	if err != nil {
		return nil, err
	}

	spec.Maps[relayMapTunnels].MaxEntries = maxTunnels
	spec.Maps[relayMapTunnelStats].MaxEntries = maxTunnels
	spec.Maps[relayMapPathStats].MaxEntries = maxPaths

	cpus, err := ebpf.PossibleCPU()
	if err != nil {
		return nil, err
	}

	d := &ebpfDatapath{
		log: log, cpus: cpus, want: map[string]uint64{}, attached: map[string]*attachment{}, done: make(chan struct{}),
		changed: make(chan struct{}, 1),
	}

	if err := spec.LoadAndAssign(&d.objs, nil); err != nil {
		var ve *ebpf.VerifierError
		if errors.As(err, &ve) {
			return nil, fmt.Errorf("%w: %+v", err, ve)
		}

		return nil, err
	}

	d.stopped.Go(func() { d.watchLinks(ctx) })

	return d, nil
}

func linkFlags(l netlink.Link) uint32 {
	var flags uint32
	if len(l.Attrs().HardwareAddr) == 0 {
		flags |= ifaceL3
	}

	if l.Type() == "xfrm" {
		flags |= ifaceXFRM
	}

	return flags
}

func (d *ebpfDatapath) setInterface(name string, legs uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return net.ErrClosed
	}

	if legs == 0 {
		delete(d.want, name)
		return d.detachLocked(name)
	}

	d.want[name] = legs

	if a := d.attached[name]; a != nil {
		return d.objs.Ifaces.Put(uint32(a.index), relayIface{Legs: legs, Flags: a.flags})
	}

	l, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("user-plane interface %s: %w", name, err)
	}

	return d.attachLocked(l)
}

func (d *ebpfDatapath) attachLocked(l netlink.Link) error {
	name, index := l.Attrs().Name, l.Attrs().Index
	flags := linkFlags(l)

	if len(d.attached) >= maxIfaces {
		return fmt.Errorf("user-plane interface %s: more than %d interfaces", name, maxIfaces)
	}

	if err := d.objs.Ifaces.Put(uint32(index), relayIface{Legs: d.want[name], Flags: flags}); err != nil {
		return fmt.Errorf("register user-plane interface %s: %w", name, err)
	}

	if err := datapathAlreadyAttached(d.objs.Relay, index); err != nil {
		_ = d.objs.Ifaces.Delete(uint32(index))
		return fmt.Errorf("user-plane interface %s: %w", name, err)
	}

	lk, err := link.AttachTCX(link.TCXOptions{
		Interface: index,
		Program:   d.objs.Relay,
		Attach:    ebpf.AttachTCXIngress,
		Anchor:    link.Tail(),
	})
	if err != nil {
		_ = d.objs.Ifaces.Delete(uint32(index))
		return fmt.Errorf("attach the user-plane program to %s: %w", name, err)
	}

	d.attached[name] = &attachment{index: index, flags: flags, link: lk}
	d.log.Info("user-plane program attached", slog.String("interface", name), slog.Int("index", index),
		slog.Bool("xfrm", flags&ifaceXFRM != 0))

	return nil
}

func (d *ebpfDatapath) detachLocked(name string) error {
	a := d.attached[name]
	if a == nil {
		return nil
	}

	delete(d.attached, name)

	err := d.objs.Ifaces.Delete(uint32(a.index))
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		err = nil
	}

	d.log.Info("user-plane program detached", slog.String("interface", name), slog.Int("index", a.index))

	return errors.Join(err, a.link.Close())
}

func datapathAlreadyAttached(prog *ebpf.Program, index int) error {
	info, err := prog.Info()
	if err != nil {
		return nil
	}

	attached, err := link.QueryPrograms(link.QueryOptions{Target: index, Attach: ebpf.AttachTCXIngress})
	if err != nil || attached == nil {
		return nil
	}

	for _, ap := range attached.Programs {
		other, err := ebpf.NewProgramFromID(ap.ID)
		if err != nil {
			continue
		}

		otherInfo, err := other.Info()
		_ = other.Close()

		if err == nil && otherInfo.Name == info.Name {
			return fmt.Errorf("%q is already attached at TCX ingress: another Ella MOCN runs on this host", info.Name)
		}
	}

	return nil
}

func (d *ebpfDatapath) watchLinks(ctx context.Context) {
	for {
		updates := make(chan netlink.LinkUpdate, 64)
		routes := make(chan netlink.RouteUpdate, 64)
		addrs := make(chan netlink.AddrUpdate, 64)
		failed := make(chan struct{})
		stop := make(chan struct{})

		var once sync.Once

		onError := func(error) { once.Do(func() { close(failed) }) }

		err := errors.Join(
			netlink.LinkSubscribeWithOptions(updates, stop, netlink.LinkSubscribeOptions{ErrorCallback: onError}),
			netlink.RouteSubscribeWithOptions(routes, stop, netlink.RouteSubscribeOptions{ErrorCallback: onError}),
			netlink.AddrSubscribeWithOptions(addrs, stop, netlink.AddrSubscribeOptions{ErrorCallback: onError}),
		)
		if err != nil {
			d.log.WarnContext(ctx, "failed to watch user-plane interfaces and routes", slog.Any("error", err))
		} else {
			d.reconcile()
			d.notify()
			d.follow(ctx, updates, routes, addrs, failed)
		}

		close(stop)

		select {
		case <-ctx.Done():
			return
		case <-d.done:
			return
		case <-time.After(linkResubscribe):
		}
	}
}

func (d *ebpfDatapath) follow(ctx context.Context, updates <-chan netlink.LinkUpdate,
	routes <-chan netlink.RouteUpdate, addrs <-chan netlink.AddrUpdate, failed <-chan struct{},
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.done:
			return
		case <-failed:
			return
		case u, ok := <-updates:
			if !ok {
				return
			}

			d.update(u)
		case _, ok := <-routes:
			if !ok {
				return
			}
		case _, ok := <-addrs:
			if !ok {
				return
			}
		}

		d.notify()
	}
}

func (d *ebpfDatapath) update(u netlink.LinkUpdate) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return
	}

	name, index := u.Attrs().Name, u.Attrs().Index

	for n, a := range d.attached {
		if a.index == index && (n != name || u.Header.Type == unix.RTM_DELLINK) {
			d.logErr(d.detachLocked(n), n)
		}
	}

	if u.Header.Type != unix.RTM_NEWLINK {
		return
	}

	if _, ok := d.want[name]; !ok {
		return
	}

	if a := d.attached[name]; a != nil && a.index == index {
		return
	}

	d.logErr(d.detachLocked(name), name)
	d.logErr(d.attachLocked(u.Link), name)
}

func (d *ebpfDatapath) reconcile() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return
	}

	for name := range d.want {
		l, err := netlink.LinkByName(name)
		if err != nil {
			d.logErr(d.detachLocked(name), name)
			continue
		}

		if a := d.attached[name]; a != nil && a.index == l.Attrs().Index {
			continue
		}

		d.logErr(d.detachLocked(name), name)
		d.logErr(d.attachLocked(l), name)
	}
}

func (d *ebpfDatapath) logErr(err error, name string) {
	if err != nil {
		d.log.Error("failed to update the user-plane interface", slog.String("interface", name), slog.Any("error", err))
	}
}

func (d *ebpfDatapath) interfaceLegs(index int) (uint64, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for name, a := range d.attached {
		if a.index == index {
			return d.want[name], true
		}
	}

	return 0, false
}

func (d *ebpfDatapath) changes() <-chan struct{} {
	return d.changed
}

func (d *ebpfDatapath) notify() {
	select {
	case d.changed <- struct{}{}:
	default:
	}
}

func (d *ebpfDatapath) open() error {
	if d.closed {
		return net.ErrClosed
	}

	return nil
}

func (d *ebpfDatapath) put(teid uint32, e relayTunnel) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return err
	}

	if err := d.objs.TunnelStats.Update(teid, relayTunnelActivity{}, ebpf.UpdateNoExist); err != nil {
		return err
	}

	if err := d.objs.Tunnels.Update(teid, e, ebpf.UpdateNoExist); err != nil {
		return errors.Join(err, d.objs.TunnelStats.Delete(teid))
	}

	return nil
}

func (d *ebpfDatapath) remove(teid uint32) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return err
	}

	return errors.Join(absent(d.objs.Tunnels.Delete(teid)), absent(d.objs.TunnelStats.Delete(teid)))
}

func absent(err error) error {
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}

	return err
}

func (d *ebpfDatapath) setPath(teid, idx uint32) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return err
	}

	var e relayTunnel
	if err := d.objs.Tunnels.Lookup(teid, &e); err != nil {
		return err
	}

	e.Path = idx

	return d.objs.Tunnels.Update(teid, e, ebpf.UpdateExist)
}

func (d *ebpfDatapath) setPolicyIPsec(on bool) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return err
	}

	var v uint32
	if on {
		v = configPolicyIPsec
	}

	return d.objs.Config.Put(uint32(0), v)
}

func (d *ebpfDatapath) tunnel(teid uint32) (Counters, netip.Addr, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return Counters{}, netip.Addr{}, err
	}

	var e relayTunnel
	if err := d.objs.Tunnels.Lookup(teid, &e); err != nil {
		return Counters{}, netip.Addr{}, err
	}

	var a relayTunnelActivity
	if err := d.objs.TunnelStats.Lookup(teid, &a); err != nil {
		return Counters{}, netip.Addr{}, err
	}

	return Counters{Packets: a.Packets, Bytes: a.Bytes}, addrOf16(a.Last, e.Family == familyIPv4), nil
}

func batchPerCPU[V any](m *ebpf.Map, n, cpus int) ([]uint32, []V, error) {
	keys := make([]uint32, n)
	values := make([]V, n*cpus)

	var cursor ebpf.MapBatchCursor

	got := 0

	for got < n {
		c, err := m.BatchLookup(&cursor, keys[got:], values[got*cpus:], nil)
		got += c

		if errors.Is(err, ebpf.ErrKeyNotExist) {
			break
		}

		if err != nil {
			return nil, nil, err
		}
	}

	return keys[:got], values[:got*cpus], nil
}

func (d *ebpfDatapath) paths() (map[uint32]Counters, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return nil, err
	}

	keys, values, err := batchPerCPU[relayCounter](d.objs.PathStats, maxPaths, d.cpus)
	if err != nil {
		return nil, err
	}

	out := make(map[uint32]Counters)

	for i, k := range keys {
		var c Counters

		for _, v := range values[i*d.cpus : (i+1)*d.cpus] {
			c.Packets += v.Packets
			c.Bytes += v.Bytes
		}

		if c != (Counters{}) {
			out[k] = c
		}
	}

	return out, nil
}

func (d *ebpfDatapath) resetPath(idx uint32) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return err
	}

	return d.objs.PathStats.Put(idx, make([]relayCounter, d.cpus))
}

func (d *ebpfDatapath) sums(m *ebpf.Map, n int) ([]uint64, error) {
	keys, values, err := batchPerCPU[uint64](m, n, d.cpus)
	if err != nil {
		return nil, err
	}

	out := make([]uint64, n)

	for i, k := range keys {
		for _, v := range values[i*d.cpus : (i+1)*d.cpus] {
			out[k] += v
		}
	}

	return out, nil
}

func (d *ebpfDatapath) drops() ([][]uint64, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return nil, err
	}

	flat, err := d.sums(d.objs.Drops, maxLegs*dropCount)
	if err != nil {
		return nil, err
	}

	out := make([][]uint64, maxLegs)
	for leg := range out {
		out[leg] = flat[leg*dropCount : (leg+1)*dropCount]
	}

	return out, nil
}

func (d *ebpfDatapath) resetLeg(leg uint8) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return err
	}

	zero := make([]uint64, d.cpus)

	for r := range dropCount {
		if err := d.objs.Drops.Put(uint32(int(leg)*dropCount+r), zero); err != nil {
			return err
		}
	}

	return nil
}

func (d *ebpfDatapath) passes() ([]uint64, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if err := d.open(); err != nil {
		return nil, err
	}

	return d.sums(d.objs.Passes, passCount)
}

func (d *ebpfDatapath) mapUsage() map[string]mapUsage {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.open() != nil {
		return nil
	}

	out := map[string]mapUsage{}

	for name, m := range map[string]*ebpf.Map{
		relayMapTunnels: d.objs.Tunnels, relayMapTunnelStats: d.objs.TunnelStats, relayMapIfaces: d.objs.Ifaces,
		relayMapConfig: d.objs.Config, relayMapPathStats: d.objs.PathStats, relayMapDrops: d.objs.Drops, relayMapPasses: d.objs.Passes,
	} {
		u := mapUsage{maxEntries: m.MaxEntries()}

		if info, err := m.Info(); err == nil {
			u.memlock, _ = info.Memlock()
		}

		out[name] = u
	}

	return out
}

func (d *ebpfDatapath) close() error {
	d.mu.Lock()

	if d.closed {
		d.mu.Unlock()
		return nil
	}

	d.closed = true
	close(d.done)

	var errs []error

	for name := range d.attached {
		errs = append(errs, d.detachLocked(name))
	}

	errs = append(errs, d.objs.Close())
	d.mu.Unlock()

	d.stopped.Wait()

	return errors.Join(errs...)
}
