package fake

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/perf/packet"
)

type CoreConfig struct {
	PLMN     db.PLMN
	Listen   netip.Addr
	Gateways []netip.Addr
	Logger   *slog.Logger
}

type Core struct {
	ues       *ueTable
	log       *slog.Logger
	mu        sync.Mutex
	listeners []*sctp.Listener
	conns     []*sctp.SCTPConn
	gtpu      *net.UDPConn
}

func StartCore(ctx context.Context, cfg CoreConfig) (*Core, error) {
	c := &Core{ues: newUETable(cfg.Gateways), log: cfg.Logger}

	s1c, err := newS1Core(cfg.PLMN, c.ues)
	if err != nil {
		return nil, err
	}

	ngc, err := newNGCore(cfg.PLMN, c.ues)
	if err != nil {
		return nil, err
	}

	c.gtpu, err = net.ListenUDP("udp", &net.UDPAddr{Port: packet.Port})
	if err != nil {
		return nil, err
	}

	go Reflect(c.gtpu, c.ues.lookup)

	for _, p := range []coreProto{s1c, ngc} {
		var lc sctp.ListenConfig

		ln, err := lc.Listen(ctx, &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: cfg.Listen.AsSlice()}}, Port: p.port()})
		if err != nil {
			c.Close()
			return nil, err
		}

		c.listeners = append(c.listeners, ln)

		go c.accept(ctx, ln, p)
	}

	return c, nil
}

func (c *Core) accept(ctx context.Context, ln *sctp.Listener, p coreProto) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		if err := conn.PrepareAccepted(c.log); err != nil {
			_ = conn.Abort()
			continue
		}

		c.mu.Lock()
		c.conns = append(c.conns, conn)
		c.mu.Unlock()

		go c.serve(ctx, conn, p)
	}
}

func (c *Core) serve(ctx context.Context, conn *sctp.SCTPConn, p coreProto) {
	buf := make([]byte, sctp.MaxMessageSize)

	for {
		n, info, err := conn.ReadMsg(buf)
		if err != nil {
			c.log.InfoContext(ctx, "association closed", slog.String("error", err.Error()))
			return
		}

		replies, err := p.handle(buf[:n])
		if err != nil {
			c.log.WarnContext(ctx, "message", slog.String("error", err.Error()))
			continue
		}

		stream := uint16(0)
		if info != nil {
			stream = info.Stream
		}

		for _, r := range replies {
			out := &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(p.ppid()), Stream: stream}

			if _, err := conn.WriteMsgWait(ctx, r, out); err != nil {
				c.log.WarnContext(ctx, "send", slog.String("error", err.Error()))
				return
			}
		}
	}
}

func (c *Core) Sessions() []Session {
	return c.ues.list()
}

func (c *Core) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error

	for _, ln := range c.listeners {
		errs = append(errs, ln.Close())
	}

	for _, conn := range c.conns {
		errs = append(errs, conn.Close())
	}

	if c.gtpu != nil {
		errs = append(errs, c.gtpu.Close())
	}

	if err := errors.Join(errs...); err != nil {
		c.log.Warn("close", slog.String("error", err.Error()))
	}
}
