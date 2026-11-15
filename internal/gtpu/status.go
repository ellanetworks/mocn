package gtpu

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"time"
)

type State string

const (
	StateReady       State = "ready"
	StateUnavailable State = "unavailable"
	StateOff         State = "off"
)

type Reason string

const (
	ReasonNotConnected     Reason = "not_connected"
	ReasonNoRoute          Reason = "no_route"
	ReasonRouteMismatch    Reason = "route_mismatch"
	ReasonNoAddress        Reason = "no_address"
	ReasonInterfaceError   Reason = "interface_error"
	ReasonTooManyOperators Reason = "too_many_operators"
)

type Status struct {
	State     State
	Reason    Reason
	Interface string
	Addresses []netip.Addr
	Since     time.Time
}

func (s Status) same(o Status) bool {
	return s.State == o.State && s.Reason == o.Reason && s.Interface == o.Interface && slices.Equal(s.Addresses, o.Addresses)
}

type resolution struct {
	ok      bool
	reason  Reason
	detail  error
	iface   string
	ifindex int
	addrs   []netip.Addr
}

func (r resolution) addr(v4 bool) (netip.Addr, bool) {
	for _, a := range r.addrs {
		if a.Is4() == v4 {
			return a, true
		}
	}

	return netip.Addr{}, false
}

func unresolved(reason Reason, detail error) resolution {
	return resolution{reason: reason, detail: detail}
}

func (p *Plane) Anchor(operator int64, key any, a Anchor) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.anchors[operator] == nil {
		p.anchors[operator] = map[any]Anchor{}
	}

	a.Addresses = slices.Clone(a.Addresses)
	p.anchors[operator][key] = a
	p.evaluateLocked(operator)
}

func (p *Plane) Unanchor(operator int64, key any) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.anchors[operator], key)

	if len(p.anchors[operator]) == 0 {
		delete(p.anchors, operator)
	}

	p.evaluateLocked(operator)
}

func (p *Plane) Status(operator int64) (Status, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	s, ok := p.status[operator]
	s.Addresses = slices.Clone(s.Addresses)

	return s, ok
}

func (p *Plane) refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.refreshLocked()
}

func (p *Plane) refreshLocked() {
	p.ranIndex = 0

	if p.cfg.RANInterface != "" {
		if i, err := p.host.InterfaceIndex(p.cfg.RANInterface); err == nil {
			p.ranIndex = i
		}
	}

	for _, id := range slices.Sorted(maps.Keys(p.operators)) {
		p.evaluateLocked(id)
	}
}

func (p *Plane) evaluateLocked(id int64) {
	o, ok := p.operators[id]
	if !ok {
		return
	}

	res := p.resolveLocked(id)
	p.resolved[id] = res

	next := Status{State: StateOff}

	switch {
	case !o.Aggregated:
	case res.ok:
		next = Status{State: StateReady, Interface: res.iface, Addresses: res.addrs}
	default:
		next = Status{State: StateUnavailable, Reason: res.reason, Interface: res.iface, Addresses: res.addrs}
	}

	prev, existed := p.status[id]
	if existed && prev.same(next) {
		return
	}

	next.Since = p.cfg.Now()
	p.status[id] = next

	if !existed {
		return
	}

	log := p.log.With(slog.Int64("operator_id", id), slog.String("operator", o.Name))

	switch next.State {
	case StateReady:
		log.Info("operator user plane ready", slog.String("interface", next.Interface),
			slog.Any("addresses", next.Addresses))
	case StateUnavailable:
		attrs := []any{slog.String("reason", string(next.Reason))}
		if res.detail != nil {
			attrs = append(attrs, slog.Any("error", res.detail))
		}

		log.Error("operator user plane unavailable: its sessions are refused", attrs...)
	case StateOff:
		log.Info("operator user plane off")
	}
}

func (p *Plane) resolveLocked(id int64) resolution {
	leg, ok := p.legs[id]
	if !ok {
		if p.operators[id].Aggregated {
			return unresolved(ReasonTooManyOperators, ErrTooManyRelayed)
		}

		return unresolved(ReasonNotConnected, nil)
	}

	anchors := p.anchors[id]
	if len(anchors) == 0 {
		return unresolved(ReasonNotConnected, errors.Join(p.moveLegLocked(id, leg, "")))
	}

	res := resolution{}
	keys := slices.SortedFunc(maps.Keys(anchors), func(a, b any) int {
		return anchors[a].Peer.Compare(anchors[b].Peer)
	})

	for _, k := range keys {
		a := anchors[k]

		if res.iface != "" && res.iface != a.Interface {
			return p.mismatchLocked(id, leg, fmt.Errorf("its MMEs and AMFs are reached by %s and %s", res.iface,
				a.Interface))
		}

		res.iface = a.Interface

		for _, addr := range a.Addresses {
			have, ok := res.addr(addr.Is4())

			switch {
			case !ok:
				res.addrs = append(res.addrs, addr)
			case have != addr:
				return p.mismatchLocked(id, leg, fmt.Errorf("its MMEs and AMFs are reached from %s and %s", have, addr))
			}
		}
	}

	slices.SortFunc(res.addrs, func(a, b netip.Addr) int { return a.Compare(b) })

	if err := p.moveLegLocked(id, leg, res.iface); err != nil {
		res.reason, res.detail = ReasonInterfaceError, err
		return res
	}

	for _, k := range keys {
		a := anchors[k]

		r, err := p.host.Route(a.Peer)
		if err != nil {
			res.reason, res.detail = ReasonNoRoute, err
			return res
		}

		if name, err := p.host.InterfaceName(r.Ifindex); err != nil || name != a.Interface {
			res.reason, res.detail = ReasonRouteMismatch, fmt.Errorf("the route to %s no longer leaves by %s", a.Peer,
				a.Interface)

			return res
		}

		if want, _ := res.addr(a.Peer.Is4()); r.Src != want {
			res.reason, res.detail = ReasonNoAddress, fmt.Errorf("the route to %s no longer uses the source address %s",
				a.Peer, want)

			return res
		}

		res.ifindex = r.Ifindex
	}

	for _, a := range res.addrs {
		p.own[a] = true
	}

	res.ok = true

	return res
}

func (p *Plane) mismatchLocked(id int64, leg uint8, detail error) resolution {
	return unresolved(ReasonRouteMismatch, errors.Join(detail, p.moveLegLocked(id, leg, "")))
}

func (p *Plane) moveLegLocked(id int64, leg uint8, iface string) error {
	old := p.legIface[id]

	var errs []error

	if old != "" && old != iface {
		errs = append(errs, p.setLegLocked(old, leg, false))
		delete(p.legIface, id)
	}

	if iface != "" {
		if err := p.setLegLocked(iface, leg, true); err != nil {
			return errors.Join(append(errs, err)...)
		}

		p.legIface[id] = iface
	}

	return errors.Join(errs...)
}
