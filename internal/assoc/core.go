package assoc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/tracing"
)

type Node[C any] struct {
	Key          string
	Addrs        []string
	SetupRequest func(want C) ([]byte, error)
}

type Peer struct {
	operatorID int64
	name       atomic.Pointer[string]
	addrs      []string
	attr       string
}

func (p *Peer) OperatorID() int64 {
	return p.operatorID
}

func (p *Peer) Operator() string {
	if name := p.name.Load(); name != nil {
		return *name
	}

	return ""
}

func (p *Peer) Addrs() []string {
	return p.addrs
}

func (p *Peer) TNLAddresses() []string {
	return slices.Clone(p.addrs)
}

type Core struct {
	*Peer
	conn *sctp.SCTPConn
	tx   *Transport
	lost atomic.Bool
}

func (a *Core) Send(ctx context.Context, msg []byte, stream uint16) error {
	return a.tx.Send(ctx, metrics.SideCore, a.conn, a.Operator(), msg, stream)
}

func (a *Core) Conn() *sctp.SCTPConn {
	return a.conn
}

func (a *Core) Logger(log *slog.Logger) *slog.Logger {
	return log.With(slog.Int64("operator_id", a.operatorID), slog.Any(a.attr, a.addrs))
}

func (a *Core) MarkLost() {
	a.lost.Store(true)
}

func (a *Core) Lost() bool {
	return a.lost.Load()
}

type Client[I, C any] struct {
	*Peer
	node   Node[C]
	log    *slog.Logger
	now    func() time.Time
	cancel context.CancelFunc
	kicks  chan struct{}
	done   chan struct{}

	plmns []db.PLMN

	State State
	Since time.Time
	Info  I
	Assoc *Core
}

func (c *Client[I, C]) Logger() *slog.Logger {
	return c.log
}

func (c *Client[I, C]) setStateLocked(state State) {
	if c.State != state {
		c.State = state
		c.Since = c.now()
	}
}

func (c *Client[I, C]) PrimaryAddressLocked() string {
	if c.Assoc != nil {
		if addr, err := c.Assoc.conn.PrimaryPeerAddr(); err == nil {
			return addr.String()
		}
	}

	return ""
}

func (c *Client[I, C]) kick() {
	select {
	case c.kicks <- struct{}{}:
	default:
	}
}

type procedure int

const (
	procNone procedure = iota
	procSetup
	procUpdate
)

type Session[S Setup, R RadioConfig[R], I any, C Equaler[C]] struct {
	g         *Gateway[S, R, I, C]
	c         *Client[I, C]
	assoc     *Core
	setupDone bool
	acked     C
	pending   procedure
	sent      C
	sentMsg   []byte
	deadline  time.Time
	retryAt   time.Time
}

func (s *Session[S, R, I, C]) Core() *Core {
	return s.assoc
}

func (s *Session[S, R, I, C]) Client() *Client[I, C] {
	return s.c
}

func (s *Session[S, R, I, C]) Update(ctx context.Context, apply func(c *Client[I, C])) {
	s.g.mu.Lock()
	defer s.g.mu.Unlock()

	apply(s.c)
	s.g.changedLocked(ctx)
}

func (s *Session[S, R, I, C]) SetupDone() bool {
	return s.setupDone
}

func (s *Session[S, R, I, C]) AwaitingSetup() bool {
	return s.pending == procSetup
}

func (s *Session[S, R, I, C]) EndSetup(result string) {
	s.pending = procNone

	s.g.countSetup(metrics.SideCore, result)
}

func (s *Session[S, R, I, C]) SetupInvalid(ctx context.Context, what string, err error) error {
	tracing.SpanFailed(ctx, ErrTypeInvalid)
	s.EndSetup(SetupFailure)

	return fmt.Errorf("%s: %w", what, err)
}

func (s *Session[S, R, I, C]) SetupAccepted(ctx context.Context, apply func(info *I)) {
	s.pending, s.setupDone, s.acked = procNone, true, s.sent

	s.g.mu.Lock()
	apply(&s.c.Info)
	s.c.setStateLocked(Up)
	s.g.changedLocked(ctx)
	s.g.mu.Unlock()

	s.g.countSetup(metrics.SideCore, SetupSuccess)
}

func (s *Session[S, R, I, C]) RetryAfter(wait *time.Duration) time.Duration {
	retry := s.g.cfg.RetryInterval
	if wait != nil {
		retry = *wait
	}

	s.retryAt = s.g.cfg.Now().Add(retry)

	return retry
}

func (s *Session[S, R, I, C]) SetupRejected(ctx context.Context, apply func(info *I)) {
	s.g.mu.Lock()
	apply(&s.c.Info)
	s.c.State, s.c.Since = SetupFailed, s.g.cfg.Now()
	s.g.changedLocked(ctx)
	s.g.mu.Unlock()
}

func (s *Session[S, R, I, C]) AwaitingUpdate() bool {
	return s.pending == procUpdate
}

func (s *Session[S, R, I, C]) UpdateAcknowledged() {
	if s.pending == procUpdate {
		s.pending, s.acked = procNone, s.sent
	}
}

func (s *Session[S, R, I, C]) UpdateRejected(wait *time.Duration) time.Duration {
	s.pending = procNone

	return s.RetryAfter(wait)
}

func (g *Gateway[S, R, I, C]) newClient(o db.Operator, node Node[C]) *Client[I, C] {
	c := &Client[I, C]{
		Peer:  &Peer{operatorID: o.ID, addrs: slices.Clone(node.Addrs), attr: g.names.CoreAttr},
		node:  node,
		plmns: slices.Clone(o.PLMNs),
		now:   g.cfg.Now,
		kicks: make(chan struct{}, 1),
		done:  make(chan struct{}),
		State: Disconnected,
		Since: g.cfg.Now(),
	}

	c.name.Store(&o.Name)
	c.log = g.log.With(slog.Int64("operator_id", o.ID), slog.Any(g.names.CoreAttr, c.addrs))

	return c
}

func (g *Gateway[S, R, I, C]) runClient(ctx context.Context, c *Client[I, C]) {
	log := c.log
	backoff := g.cfg.ReconnectMin

	for {
		conn, err := g.dial(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			log.WarnContext(ctx, g.names.Core+" association failed", slog.Any("error", err))

			if !sleep(ctx, backoff) {
				return
			}

			backoff = min(2*backoff, g.cfg.ReconnectMax)

			continue
		}

		assoc := &Core{Peer: c.Peer, conn: conn, tx: g.tx}

		g.mu.Lock()
		c.Assoc = assoc
		c.setStateLocked(Connected)
		g.mu.Unlock()

		log.InfoContext(ctx, g.names.Core+" association up")

		start := g.cfg.Now()
		err = g.session(ctx, c, assoc)

		if ctx.Err() != nil {
			_ = conn.Close()
		} else {
			_ = conn.Abort()
		}

		g.mu.Lock()

		wasUp := c.State == Up
		lost := g.proto.CoreLostLocked(c, assoc)

		var none I

		c.Assoc = nil
		c.State, c.Since, c.Info = Disconnected, g.cfg.Now(), none

		if wasUp {
			g.changedLocked(ctx)
		}

		g.mu.Unlock()

		lost(context.WithoutCancel(ctx))

		if ctx.Err() != nil {
			return
		}

		log.WarnContext(ctx, g.names.Core+" association lost", slog.Any("error", err))

		if g.cfg.Now().Sub(start) > g.cfg.ReconnectMax {
			backoff = g.cfg.ReconnectMin
		}

		if !sleep(ctx, backoff) {
			return
		}

		backoff = min(2*backoff, g.cfg.ReconnectMax)
	}
}

// TS 36.412 clause 7
// TS 38.412 clause 7
func (g *Gateway[S, R, I, C]) dial(ctx context.Context, c *Client[I, C]) (*sctp.SCTPConn, error) {
	raddr := &sctp.SCTPAddr{Port: g.cfg.CorePort}

	for _, a := range c.addrs {
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return nil, fmt.Errorf("invalid TNL address %q: %w", a, err)
		}

		raddr.IPAddrs = append(raddr.IPAddrs, net.IPAddr{IP: ip.AsSlice()})
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	d := sctp.Dialer{
		InitMsg: sctp.InitMsg{NumOstreams: g.names.CoreStreams, MaxInstreams: g.names.CoreStreams}, Logger: g.log,
	}

	return d.Dial(dialCtx, raddr)
}

func (g *Gateway[S, R, I, C]) session(ctx context.Context, c *Client[I, C], assoc *Core) error {
	msgs := make(chan []byte)
	readErr := make(chan error, 1)

	readCtx, stopReading := context.WithCancel(ctx)
	defer stopReading()

	go func() {
		buf := make([]byte, sctp.MaxMessageSize)

		for {
			n, info, err := assoc.conn.ReadMsg(buf)
			if err != nil {
				readErr <- err
				return
			}

			if info == nil || sctp.PPIDWireOrder(info.PPID) != g.names.PPID {
				continue
			}

			select {
			case msgs <- slices.Clone(buf[:n]):
			case <-readCtx.Done():
				return
			}
		}
	}()

	s := &Session[S, R, I, C]{g: g, c: c, assoc: assoc}
	timer := time.NewTimer(time.Hour)

	defer timer.Stop()
	defer func() {
		if s.pending == procSetup {
			s.EndSetup(SetupError)
		}
	}()

	for {
		if err := g.step(ctx, s); err != nil {
			return err
		}

		timer.Reset(g.nextWake(s))

		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return fmt.Errorf("SCTP: %w", err)
		case msg := <-msgs:
			if err := g.proto.HandleCore(ctx, s, msg); err != nil {
				return err
			}
		case <-c.kicks:
		case <-timer.C:
		}
	}
}

func (g *Gateway[S, R, I, C]) nextWake(s *Session[S, R, I, C]) time.Duration {
	now := g.cfg.Now()

	switch {
	case s.pending != procNone:
		return max(s.deadline.Sub(now), time.Millisecond)
	case now.Before(s.retryAt):
		return s.retryAt.Sub(now)
	default:
		return time.Hour
	}
}

func (g *Gateway[S, R, I, C]) step(ctx context.Context, s *Session[S, R, I, C]) error {
	now := g.cfg.Now()

	if s.pending != procNone {
		if now.Before(s.deadline) {
			return nil
		}

		if s.pending == procSetup {
			s.EndSetup(SetupTimeout)
			return errors.New("no response to " + g.names.Setup + " Request")
		}

		return g.resendUpdate(ctx, s)
	}

	if now.Before(s.retryAt) {
		return nil
	}

	g.mu.Lock()
	want, ok := g.coreConfigLocked(s.c.operatorID)
	g.mu.Unlock()

	if !ok || (s.setupDone && want.Equal(s.acked)) {
		return nil
	}

	if !s.setupDone {
		return g.sendSetup(ctx, s, want)
	}

	return g.sendUpdate(ctx, s, want)
}

func (g *Gateway[S, R, I, C]) coreConfigLocked(operatorID int64) (C, bool) {
	o, ok := g.OperatorLocked(operatorID)
	if !ok {
		var none C
		return none, false
	}

	setups := make([]S, 0, len(g.radios))

	for _, n := range g.radios {
		if n.Setup != nil {
			setups = append(setups, *n.Setup)
		}
	}

	return g.proto.CoreConfig(o.PLMNs, setups)
}

// TS 36.413 clause 8.7.3
// TS 38.413 clause 8.7.1
func (g *Gateway[S, R, I, C]) sendSetup(ctx context.Context, s *Session[S, R, I, C], want C) error {
	out, err := s.c.node.SetupRequest(want)
	if err != nil {
		g.countSetup(metrics.SideCore, SetupError)
		return err
	}

	if err := s.assoc.Send(ctx, out, NonUEStream); err != nil {
		g.countSetup(metrics.SideCore, SetupError)
		return fmt.Errorf("SCTP: %w", err)
	}

	s.c.log.LogAttrs(ctx, slog.LevelInfo, g.names.Setup+" Request sent to "+g.names.Core, g.proto.ConfigAttrs(want)...)

	s.pending, s.sent, s.deadline = procSetup, want, g.cfg.Now().Add(g.cfg.ResponseTimeout)

	return nil
}

// TS 36.413 clause 8.7.4
// TS 38.413 clause 8.7.2
func (g *Gateway[S, R, I, C]) sendUpdate(ctx context.Context, s *Session[S, R, I, C], want C) error {
	out, err := g.proto.ConfigurationUpdate(want, s.acked)
	if err != nil {
		return err
	}

	if err := s.assoc.Send(ctx, out, NonUEStream); err != nil {
		return fmt.Errorf("SCTP: %w", err)
	}

	s.c.log.LogAttrs(ctx, slog.LevelInfo, g.names.Update+" sent to "+g.names.Core, g.proto.ConfigAttrs(want)...)

	s.pending, s.sent, s.sentMsg, s.deadline = procUpdate, want, out, g.cfg.Now().Add(g.cfg.ResponseTimeout)

	return nil
}

// TS 36.413 clause 8.7.4.4
// TS 38.413 clause 8.7.2.4
func (g *Gateway[S, R, I, C]) resendUpdate(ctx context.Context, s *Session[S, R, I, C]) error {
	if err := s.assoc.Send(ctx, s.sentMsg, NonUEStream); err != nil {
		return fmt.Errorf("SCTP: %w", err)
	}

	s.c.log.WarnContext(ctx, "no response to "+g.names.Update+", sent it again")

	s.deadline = g.cfg.Now().Add(g.cfg.ResponseTimeout)

	return nil
}
