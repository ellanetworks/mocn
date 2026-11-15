package fake

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/ellanetworks/mocn/internal/db"
)

var (
	errSetupRejected = errors.New("setup rejected")
	errNoBearer      = errors.New("no bearer or PDU session")
	errUnknownUE     = errors.New("unknown UE")
)

type Session struct {
	Local     netip.Addr `json:"local"`
	LocalTEID uint32     `json:"local_teid"`
	Peer      netip.Addr `json:"peer"`
	PeerTEID  uint32     `json:"peer_teid"`
}

type Endpoint struct {
	Addr netip.Addr
	TEID uint32
}

type radioProto interface {
	port() int
	ppid() uint32
	setup(radio int) ([]byte, error)
	setupResult(raw []byte) (bool, error)
	initialUE(radio int, ue uint32) ([]byte, error)
	handle(raw []byte, local netip.Addr, teid uint32) ([]byte, Session, uint32, bool, error)
}

type coreProto interface {
	port() int
	ppid() uint32
	handle(raw []byte) ([][]byte, error)
}

func newRadioProto(proto string, p db.PLMN) (radioProto, error) {
	switch proto {
	case "s1":
		return newS1Radio(p)
	case "ng":
		return newNGRadio(p)
	}

	return nil, fmt.Errorf("unknown interface %q", proto)
}

func address(b []byte) (netip.Addr, error) {
	switch len(b) {
	case 4, 16:
		a, _ := netip.AddrFromSlice(b)
		return a, nil
	case 20:
		a, _ := netip.AddrFromSlice(b[:4])
		return a, nil
	}

	return netip.Addr{}, fmt.Errorf("transport layer address of %d octets", len(b))
}

type ueTable struct {
	mu       sync.Mutex
	gateways []netip.Addr
	next     uint64
	pending  map[uint64]Endpoint
	sessions []Session
	byTEID   map[uint32]Session
}

func newUETable(gateways []netip.Addr) *ueTable {
	return &ueTable{gateways: gateways, pending: map[uint64]Endpoint{}, byTEID: map[uint32]Session{}}
}

func (t *ueTable) open() (uint64, Endpoint) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.next++
	gw := Endpoint{Addr: t.gateways[int(t.next-1)%len(t.gateways)], TEID: uint32(t.next)}
	t.pending[t.next] = gw

	return t.next, gw
}

func (t *ueTable) established(id uint64, peer netip.Addr, teid uint32) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	gw, ok := t.pending[id]
	if !ok {
		return fmt.Errorf("%w %d", errUnknownUE, id)
	}

	delete(t.pending, id)

	s := Session{Local: gw.Addr, LocalTEID: gw.TEID, Peer: peer, PeerTEID: teid}
	t.sessions = append(t.sessions, s)
	t.byTEID[gw.TEID] = s

	return nil
}

func (t *ueTable) list() []Session {
	t.mu.Lock()
	defer t.mu.Unlock()

	return append([]Session(nil), t.sessions...)
}

func (t *ueTable) lookup(teid uint32) (Session, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.byTEID[teid]

	return s, ok
}
