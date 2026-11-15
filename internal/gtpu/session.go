package gtpu

import (
	"slices"
)

type Kind uint8

const (
	KindUL Kind = iota + 1
	KindDL
	KindULAdditional
	KindDLAdditional
	KindULRedundant
	KindDLRedundant
	KindULAdditionalRedundant
	KindDLAdditionalRedundant
	KindForwardDL
	KindForwardUL
	KindForwardDLAdditional
	KindForwardULAdditional
	KindForwardDLDRB
	KindForwardULDRB
	KindForwardDLERAB
	KindULQosFlow
	KindDLQosFlow
	KindDLAdditionalPair
	KindULAdditionalPair
	KindDLAdditionalRedundantPair
	KindULAdditionalRedundantPair
)

func (k Kind) Forwarding() bool {
	return k >= KindForwardDL && k <= KindForwardDLERAB
}

func (k Kind) Uplink() bool {
	switch k {
	case KindUL, KindULAdditional, KindULRedundant, KindULAdditionalRedundant, KindULQosFlow, KindULAdditionalPair,
		KindULAdditionalRedundantPair:
		return true
	}

	return false
}

type Slot struct {
	Kind  Kind
	Index int
}

type Ref struct {
	TEID       uint32
	Bearer     int
	Slot       Slot
	Peer       Endpoint
	Ingress    Side
	Proc       int
	Pending    bool
	Superseded bool
}

type Session struct {
	refs []Ref
}

func (s *Session) Refs() []Ref {
	return slices.Clone(s.refs)
}

func (s *Session) Len() int {
	return len(s.refs)
}

type Binding struct {
	Path    Path
	Proc    int
	Bearer  int
	Slot    Slot
	Pending bool
}

func matches(r Ref, b Binding, to Side, tla []byte, teid uint32) bool {
	if r.Bearer != b.Bearer || r.Slot != b.Slot || r.Ingress != to.other() || r.Superseded || r.Peer.TEID != teid {
		return false
	}

	v4, v6 := Addresses(tla)

	return r.Peer.Addr == v4 || r.Peer.Addr == v6
}

func (p *Plane) Bind(s *Session, b Binding, to Side, tla []byte, teid uint32) (Endpoint, error) {
	for _, r := range s.refs {
		if matches(r, b, to, tla, teid) {
			if t, ok := p.Get(r.TEID); ok {
				return t.Endpoint(), nil
			}
		}
	}

	t, err := p.open(b.Path, to, tla, teid, b.Slot.Kind.Forwarding())
	if err != nil {
		return Endpoint{}, err
	}

	s.refs = append(s.refs, Ref{
		TEID: t.TEID, Bearer: b.Bearer, Slot: b.Slot, Peer: t.Peer, Ingress: t.Ingress, Proc: b.Proc, Pending: b.Pending,
	})

	if !b.Pending {
		p.activate(s, len(s.refs)-1)
	}

	return t.Endpoint(), nil
}

func (p *Plane) activate(s *Session, i int) {
	cur := s.refs[i]

	var free []uint32

	for j := range s.refs {
		r := &s.refs[j]
		if j == i || r.Bearer != cur.Bearer || r.Slot != cur.Slot || r.Pending {
			continue
		}

		if r.Superseded {
			free = append(free, r.TEID)
		} else {
			r.Superseded = true
		}
	}

	p.drop(s, free)
}

func (p *Plane) drop(s *Session, teids []uint32) {
	if len(teids) == 0 {
		return
	}

	s.refs = slices.DeleteFunc(s.refs, func(r Ref) bool { return slices.Contains(teids, r.TEID) })

	p.Close(teids...)
}

func (p *Plane) Settle(s *Session, proc int, accepted func(bearer int) bool) {
	var free, activated []uint32

	for i := range s.refs {
		r := &s.refs[i]
		if !r.Pending || r.Proc != proc {
			continue
		}

		if !accepted(r.Bearer) {
			free = append(free, r.TEID)
			continue
		}

		r.Pending = false
		activated = append(activated, r.TEID)
	}

	p.drop(s, free)

	for _, teid := range activated {
		if i := slices.IndexFunc(s.refs, func(r Ref) bool { return r.TEID == teid }); i >= 0 {
			p.activate(s, i)
		}
	}
}

func (p *Plane) Release(s *Session, keep func(Ref) bool) {
	var free []uint32

	for _, r := range s.refs {
		if !keep(r) {
			free = append(free, r.TEID)
		}
	}

	p.drop(s, free)
}

func (p *Plane) ReleaseBearers(s *Session, bearers ...int) {
	p.Release(s, func(r Ref) bool { return !slices.Contains(bearers, r.Bearer) })
}

func (p *Plane) ReleaseAll(s *Session) {
	p.Release(s, func(Ref) bool { return false })
}

func (p *Plane) Adopt(from, to *Session, path Path, take func(Ref) bool) {
	var moved []Ref

	from.refs = slices.DeleteFunc(from.refs, func(r Ref) bool {
		if take(r) {
			moved = append(moved, r)
			return true
		}

		return false
	})

	to.refs = append(to.refs, moved...)

	teids := make([]uint32, 0, len(moved))
	for _, r := range moved {
		teids = append(teids, r.TEID)
	}

	p.move(teids, path)
}

func (p *Plane) Reversed(s *Session, from Side, tla []byte, teid uint32) (Endpoint, error) {
	for _, r := range s.refs {
		if r.TEID == teid && r.Ingress == from {
			return p.Reverse(from, tla, teid)
		}
	}

	return Endpoint{}, ErrUnknownEndpoint
}

type TunnelInfo struct {
	Bearer     int
	Ingress    Side
	Forwarding bool
	Local      Endpoint
	Peer       Endpoint
	Counters   Counters
}

func (p *Plane) Describe(s *Session) []TunnelInfo {
	out := make([]TunnelInfo, 0, len(s.refs))

	for _, r := range s.refs {
		t, ok := p.Get(r.TEID)
		if !ok {
			continue
		}

		out = append(out, TunnelInfo{
			Bearer: r.Bearer, Ingress: r.Ingress, Forwarding: r.Slot.Kind.Forwarding(), Local: t.Endpoint(), Peer: t.Peer,
			Counters: p.Counters(r.TEID),
		})
	}

	return out
}

func (s Side) Direction() string {
	return s.direction()
}
