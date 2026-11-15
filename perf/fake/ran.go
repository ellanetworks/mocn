package fake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
)

const (
	ueStream     = 1
	setupTimeout = 30 * time.Second
)

var errIncomplete = errors.New("sessions not all established")

type RANConfig struct {
	Proto    string
	PLMN     db.PLMN
	MOCN     netip.Addr
	Radios   []netip.Addr
	Sessions int
	Window   int
	Logger   *slog.Logger
}

type RAN struct {
	mu       sync.Mutex
	sessions []Session
	conns    []*sctp.SCTPConn
	sockets  map[netip.Addr]*net.UDPConn
	pings    *pinger
}

func StartRAN(ctx context.Context, cfg RANConfig) (*RAN, error) {
	proto, err := newRadioProto(cfg.Proto, cfg.PLMN)
	if err != nil {
		return nil, err
	}

	r := &RAN{sockets: map[netip.Addr]*net.UDPConn{}, pings: newPinger()}

	for _, a := range cfg.Radios {
		c, err := listenGTPU(a)
		if err != nil {
			r.Close()
			return nil, err
		}

		r.sockets[a] = c

		go r.serveGTPU(c)
	}

	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup

	errs := make([]error, len(cfg.Radios))

	for i, a := range cfg.Radios {
		count := cfg.Sessions / len(cfg.Radios)
		if i < cfg.Sessions%len(cfg.Radios) {
			count++
		}

		wg.Go(func() {
			if errs[i] = r.radio(gctx, proto, cfg, i, a, count); errs[i] != nil {
				cancel()
			}
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		r.Close()
		return nil, err
	}

	if len(r.Sessions()) != cfg.Sessions {
		r.Close()
		return nil, fmt.Errorf("%w: %d of %d", errIncomplete, len(r.Sessions()), cfg.Sessions)
	}

	return r, nil
}

func (r *RAN) radio(ctx context.Context, proto radioProto, cfg RANConfig, idx int, local netip.Addr, count int) error {
	dctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()

	conn, err := sctp.Dial(dctx, "sctp", &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: local.AsSlice()}}},
		&sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: cfg.MOCN.AsSlice()}}, Port: proto.port()},
		sctp.InitMsg{NumOstreams: 2, MaxInstreams: 2})
	if err != nil {
		return fmt.Errorf("radio %s: %w", local, err)
	}

	r.mu.Lock()
	r.conns = append(r.conns, conn)
	r.mu.Unlock()

	info := &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(proto.ppid())}
	ueInfo := &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(proto.ppid()), Stream: ueStream}

	req, err := proto.setup(idx)
	if err != nil {
		return err
	}

	if _, err := conn.WriteMsg(req, info); err != nil {
		return err
	}

	buf := make([]byte, sctp.MaxMessageSize)

	for {
		n, _, err := conn.ReadMsg(buf)
		if err != nil {
			return fmt.Errorf("radio %s setup: %w", local, err)
		}

		up, err := proto.setupResult(buf[:n])
		if err != nil {
			return fmt.Errorf("radio %s setup: %w", local, err)
		}

		if up {
			break
		}
	}

	window := make(chan struct{}, cfg.Window)
	done := make(chan error, 1)

	go func() {
		done <- r.establish(conn, proto, local, idx, count, window, ueInfo, buf)
	}()

	for ue := range count {
		select {
		case window <- struct{}{}:
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}

		msg, err := proto.initialUE(idx, uint32(ue+1))
		if err != nil {
			return err
		}

		if _, err := conn.WriteMsg(msg, ueInfo); err != nil {
			return err
		}
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RAN) establish(conn *sctp.SCTPConn, proto radioProto, local netip.Addr, idx, count int,
	window chan struct{}, info *sctp.SndRcvInfo, buf []byte,
) error {
	for got := 0; got < count; {
		n, _, err := conn.ReadMsg(buf)
		if err != nil {
			return fmt.Errorf("radio %s: %w", local, err)
		}

		teid := uint32(idx+1)<<16 | uint32(got+1)

		reply, s, _, ok, err := proto.handle(buf[:n], local, teid)
		if err != nil {
			return fmt.Errorf("radio %s: %w", local, err)
		}

		if !ok {
			continue
		}

		if _, err := conn.WriteMsg(reply, info); err != nil {
			return err
		}

		r.mu.Lock()
		r.sessions = append(r.sessions, s)
		r.mu.Unlock()

		got++

		<-window
	}

	go drain(conn)

	return nil
}

func drain(conn *sctp.SCTPConn) {
	buf := make([]byte, sctp.MaxMessageSize)

	for {
		if _, _, err := conn.ReadMsg(buf); err != nil {
			return
		}
	}
}

func (r *RAN) Sessions() []Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Session(nil), r.sessions...)
}

func (r *RAN) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, c := range r.conns {
		_ = c.Close()
	}

	for _, c := range r.sockets {
		_ = c.Close()
	}
}

func (r *RAN) serveGTPU(c *net.UDPConn) {
	serve(c, func(m gtpuMessage) {
		if m.msg.GPDU() {
			r.pings.receive(m.msg.Body)
		}
	})
}
