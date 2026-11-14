package sctptest

import (
	"context"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
)

const WaitFor = 5 * time.Second

type Codec[P any] struct {
	Protocol  string
	PPID      uint32
	Unmarshal func([]byte) (P, error)
	Name      func(P) string
}

type Frame[P any] struct {
	PDU    P
	Raw    []byte
	Stream uint16
}

type Peer[P any] struct {
	Conn  *sctp.SCTPConn
	Msgs  chan Frame[P]
	codec Codec[P]
	done  chan struct{}
}

func NewPeer[P any](t *testing.T, codec Codec[P], conn *sctp.SCTPConn) *Peer[P] {
	t.Helper()

	p := &Peer[P]{Conn: conn, Msgs: make(chan Frame[P], 16), codec: codec, done: make(chan struct{})}

	go func() {
		defer close(p.done)

		buf := make([]byte, sctp.MaxMessageSize)

		for {
			n, info, err := conn.ReadMsg(buf)
			if err != nil {
				return
			}

			if info == nil || sctp.PPIDWireOrder(info.PPID) != codec.PPID {
				continue
			}

			raw := append([]byte{}, buf[:n]...)

			pdu, err := codec.Unmarshal(raw)
			if err != nil {
				continue
			}

			p.Msgs <- Frame[P]{PDU: pdu, Raw: raw, Stream: info.Stream}
		}
	}()

	t.Cleanup(func() { _ = conn.Abort() })

	return p
}

func (p *Peer[P]) Send(t *testing.T, msg interface{ Marshal() ([]byte, error) }) {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p.SendRaw(t, b)
}

func (p *Peer[P]) SendRaw(t *testing.T, b []byte) {
	t.Helper()

	if _, err := p.Conn.WriteMsg(b, &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(p.codec.PPID)}); err != nil {
		t.Fatal(err)
	}
}

func (p *Peer[P]) Next(t *testing.T) P {
	t.Helper()

	return p.NextFrame(t).PDU
}

func (p *Peer[P]) NextFrame(t *testing.T) Frame[P] {
	t.Helper()

	select {
	case f := <-p.Msgs:
		return f
	case <-time.After(WaitFor):
		t.Fatal("no " + p.codec.Protocol + " message")
		return Frame[P]{}
	}
}

func (p *Peer[P]) Quiet(t *testing.T, d time.Duration) {
	t.Helper()

	select {
	case f := <-p.Msgs:
		t.Fatalf("unexpected %s message %s", p.codec.Protocol, p.codec.Name(f.PDU))
	case <-time.After(d):
	}
}

func (p *Peer[P]) Closed(t *testing.T) {
	t.Helper()

	select {
	case <-p.done:
	case <-time.After(WaitFor):
		t.Fatal("association still up")
	}
}

func SkipWithoutSCTP(t *testing.T) {
	t.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}})
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("SCTP unavailable: %v", err)
		}

		t.Skipf("SCTP unavailable (sudo modprobe sctp): %v", err)
	}

	_ = ln.Close()
}

type Core[P any] struct {
	ln    *sctp.Listener
	Conns chan *Peer[P]
}

func ListenCore[P any](t *testing.T, codec Codec[P], ip string, port int) *Core[P] {
	t.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.ParseIP(ip)}}, Port: port})
	if err != nil {
		t.Fatal(err)
	}

	m := &Core[P]{ln: ln, Conns: make(chan *Peer[P], 4)}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			if err := conn.PrepareAccepted(slog.New(slog.DiscardHandler)); err != nil {
				_ = conn.Abort()
				continue
			}

			m.Conns <- NewPeer(t, codec, conn)
		}
	}()

	t.Cleanup(func() { _ = ln.Close() })

	return m
}

func (m *Core[P]) Port() int {
	return m.ln.Addr().(*sctp.SCTPAddr).Port
}

func (m *Core[P]) Accept(t *testing.T) *Peer[P] {
	t.Helper()

	select {
	case p := <-m.Conns:
		return p
	case <-time.After(WaitFor):
		t.Fatal("no association from the gateway")
		return nil
	}
}

type Operators struct {
	mu  sync.Mutex
	ops []db.Operator
}

func (o *Operators) Set(ops ...db.Operator) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.ops = ops
}

func (o *Operators) List(context.Context) ([]db.Operator, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]db.Operator{}, o.ops...), nil
}

type Gateway struct {
	Addr *sctp.SCTPAddr
	Ops  *Operators
	Stop func()
}

func Listen(t *testing.T) *sctp.Listener {
	t.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}})
	if err != nil {
		t.Fatal(err)
	}

	return ln
}

func Start(t *testing.T, ln *sctp.Listener, ops *Operators, run func(ctx context.Context)) *Gateway {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		run(ctx)
	}()

	var once sync.Once

	stop := func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}

	t.Cleanup(stop)

	return &Gateway{Stop: stop, Ops: ops, Addr: &sctp.SCTPAddr{
		IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}},
		Port:    ln.Addr().(*sctp.SCTPAddr).Port,
	}}
}

func Dial[P any](t *testing.T, codec Codec[P], addr *sctp.SCTPAddr) *Peer[P] {
	t.Helper()

	return dial(t, codec, nil, addr)
}

func DialFrom[P any](t *testing.T, codec Codec[P], ip string, addr *sctp.SCTPAddr) *Peer[P] {
	t.Helper()

	return dial(t, codec, &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.ParseIP(ip)}}}, addr)
}

func dial[P any](t *testing.T, codec Codec[P], laddr, addr *sctp.SCTPAddr) *Peer[P] {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), WaitFor)
	defer cancel()

	conn, err := sctp.Dial(ctx, "sctp", laddr, addr, sctp.InitMsg{NumOstreams: 2, MaxInstreams: 2})
	if err != nil {
		t.Fatal(err)
	}

	return NewPeer(t, codec, conn)
}

func Eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(WaitFor)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func PLMNID[T ~[3]byte](t *testing.T, mcc, mnc string) T {
	t.Helper()

	d := []byte(mcc + mnc)
	for i := range d {
		d[i] -= '0'
	}

	id := T{d[1]<<4 | d[0], 0xf0 | d[2], d[4]<<4 | d[3]}
	if len(mnc) == 3 {
		id[1] = d[5]<<4 | d[2]
	}

	return id
}

func Logger() *slog.Logger {
	if os.Getenv("MOCN_TEST_LOG") == "" {
		return slog.New(slog.DiscardHandler)
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
