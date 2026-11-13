package s1_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
)

const waitFor = 5 * time.Second

type peer struct {
	conn *sctp.SCTPConn
	msgs chan s1ap.PDU
	done chan struct{}
}

func newPeer(t *testing.T, conn *sctp.SCTPConn) *peer {
	t.Helper()

	p := &peer{conn: conn, msgs: make(chan s1ap.PDU, 16), done: make(chan struct{})}

	go func() {
		defer close(p.done)

		buf := make([]byte, sctp.MaxMessageSize)

		for {
			n, info, err := conn.ReadMsg(buf)
			if err != nil {
				return
			}

			if info == nil || sctp.PPIDWireOrder(info.PPID) != s1.PPID {
				continue
			}

			pdu, err := s1ap.Unmarshal(append([]byte{}, buf[:n]...))
			if err != nil {
				continue
			}

			p.msgs <- pdu
		}
	}()

	t.Cleanup(func() { _ = conn.Abort() })

	return p
}

func (p *peer) send(t *testing.T, msg interface{ Marshal() ([]byte, error) }) {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.conn.WriteMsg(b, &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(s1.PPID)}); err != nil {
		t.Fatal(err)
	}
}

func (p *peer) next(t *testing.T) s1ap.PDU {
	t.Helper()

	select {
	case pdu := <-p.msgs:
		return pdu
	case <-time.After(waitFor):
		t.Fatal("no S1AP message")
		return nil
	}
}

func (p *peer) quiet(t *testing.T, d time.Duration) {
	t.Helper()

	select {
	case pdu := <-p.msgs:
		t.Fatalf("unexpected S1AP message %T", pdu)
	case <-time.After(d):
	}
}

func (p *peer) closed(t *testing.T) {
	t.Helper()

	select {
	case <-p.done:
	case <-time.After(waitFor):
		t.Fatal("association still up")
	}
}

func expect[T any](t *testing.T, p *peer, kind string, proc s1ap.ProcedureCode,
	parse func([]byte) (*T, error),
) *T {
	t.Helper()

	pdu := p.next(t)

	var (
		value []byte
		code  s1ap.ProcedureCode
		got   string
	)

	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		value, code, got = m.Value, m.ProcedureCode, "initiating"
	case *s1ap.SuccessfulOutcome:
		value, code, got = m.Value, m.ProcedureCode, "successful"
	case *s1ap.UnsuccessfulOutcome:
		value, code, got = m.Value, m.ProcedureCode, "unsuccessful"
	}

	if got != kind || code != proc {
		t.Fatalf("got %s %s, want %s %s", got, code, kind, proc)
	}

	msg, err := parse(value)
	if err != nil {
		t.Fatalf("parse %s: %v", proc, err)
	}

	return msg
}

func skipWithoutSCTP(t *testing.T) {
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

type fakeMME struct {
	ln    *sctp.Listener
	conns chan *peer
}

func listenMME(t *testing.T, ip string, port int) *fakeMME {
	t.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.ParseIP(ip)}}, Port: port})
	if err != nil {
		t.Fatal(err)
	}

	m := &fakeMME{ln: ln, conns: make(chan *peer, 4)}

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

			m.conns <- newPeer(t, conn)
		}
	}()

	t.Cleanup(func() { _ = ln.Close() })

	return m
}

func (m *fakeMME) port() int {
	return m.ln.Addr().(*sctp.SCTPAddr).Port
}

func (m *fakeMME) accept(t *testing.T) *peer {
	t.Helper()

	select {
	case p := <-m.conns:
		return p
	case <-time.After(waitFor):
		t.Fatal("no association from the gateway")
		return nil
	}
}

type operators struct {
	mu  sync.Mutex
	ops []db.Operator
}

func (o *operators) set(ops ...db.Operator) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.ops = ops
}

func (o *operators) list(context.Context) ([]db.Operator, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]db.Operator{}, o.ops...), nil
}

type harness struct {
	gw   *s1.Gateway
	addr *sctp.SCTPAddr
	ops  *operators
}

func startGateway(t *testing.T, mmePort int, ops ...db.Operator) *harness {
	t.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}})
	if err != nil {
		t.Fatal(err)
	}

	src := &operators{}
	src.set(ops...)

	gw := s1.New(s1.Config{
		Listener: ln, Operators: src.list, MMEPort: mmePort, SetupWait: time.Second,
		ResponseTimeout: 2 * time.Second, RetryInterval: 500 * time.Millisecond,
		ReconnectMin: 100 * time.Millisecond, ReconnectMax: 200 * time.Millisecond,
		Logger: testLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- gw.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})

	return &harness{gw: gw, addr: &sctp.SCTPAddr{
		IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}},
		Port:    ln.Addr().(*sctp.SCTPAddr).Port,
	}, ops: src}
}

func (h *harness) connectENB(t *testing.T) *peer {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitFor)
	defer cancel()

	conn, err := sctp.Dial(ctx, "sctp", nil, h.addr, sctp.InitMsg{NumOstreams: 2, MaxInstreams: 2})
	if err != nil {
		t.Fatal(err)
	}

	return newPeer(t, conn)
}

func (h *harness) eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(waitFor)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func plmnID(t *testing.T, mcc, mnc string) s1ap.PLMNIdentity {
	t.Helper()

	d := []byte(mcc + mnc)
	for i := range d {
		d[i] -= '0'
	}

	id := s1ap.PLMNIdentity{d[1]<<4 | d[0], 0xf0 | d[2], d[4]<<4 | d[3]}
	if len(mnc) == 3 {
		id[1] = d[5]<<4 | d[2]
	}

	return id
}

func testLogger() *slog.Logger {
	if os.Getenv("MOCN_TEST_LOG") == "" {
		return slog.New(slog.DiscardHandler)
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
