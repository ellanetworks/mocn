package assoc

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/tracing"
)

var (
	ErrNoOperator = errors.New("no operator serves a broadcast PLMN")
	ErrNodeInUse  = errors.New("the global node ID is used by another radio")
	ErrNoSetup    = errors.New("no setup")
)

type Radio[S, R any] struct {
	conn        *sctp.SCTPConn
	addrs       []string
	connectedAt time.Time
	tx          *Transport
	ref         atomic.Pointer[string]

	Setup *S
	Up    bool
	push  push[R]
}

type push[R any] struct {
	acked   R
	pending *R
	timer   *time.Timer
	retryAt time.Time
}

func (n *Radio[S, R]) Send(ctx context.Context, msg []byte, stream uint16) error {
	return n.tx.sendRadio(ctx, n.conn, "", msg, stream)
}

func (n *Radio[S, R]) For(operator string) Sender {
	return operatorRadio[S, R]{n: n, operator: operator}
}

func (n *Radio[S, R]) Conn() *sctp.SCTPConn {
	return n.conn
}

func (n *Radio[S, R]) Addrs() []string {
	return n.addrs
}

func (n *Radio[S, R]) TNLAddresses() []string {
	return slices.Clone(n.addrs)
}

func (n *Radio[S, R]) ConnectedAt() time.Time {
	return n.connectedAt
}

func (n *Radio[S, R]) Logger(log *slog.Logger) *slog.Logger {
	if ref := n.ref.Load(); ref != nil {
		return log.With(slog.String("radio", *ref), slog.Any("tnl_addresses", n.addrs))
	}

	return log.With(slog.Any("tnl_addresses", n.addrs))
}

func (n *Radio[S, R]) stopPush() {
	if n.push.timer != nil {
		n.push.timer.Stop()
	}
}

type operatorRadio[S, R any] struct {
	n        *Radio[S, R]
	operator string
}

func (r operatorRadio[S, R]) Send(ctx context.Context, msg []byte, stream uint16) error {
	return r.n.tx.sendRadio(ctx, r.n.conn, r.operator, msg, stream)
}

func (g *Gateway[S, R, I, C]) radioConnected(ctx context.Context, conn *sctp.SCTPConn) {
	n := &Radio[S, R]{conn: conn, addrs: addresses(conn.RemoteAddr()), connectedAt: g.cfg.Now(), tx: g.tx}

	g.mu.Lock()
	g.radios[conn] = n
	g.mu.Unlock()

	n.Logger(g.log).InfoContext(ctx, "radio connected")
}

func (g *Gateway[S, R, I, C]) radioDisconnected(ctx context.Context, conn *sctp.SCTPConn) {
	g.mu.Lock()

	n, ok := g.radios[conn]
	if ok {
		g.removeLocked(n)
	}

	g.mu.Unlock()

	if !ok {
		return
	}

	n.Logger(g.log).InfoContext(ctx, "radio disconnected")
	g.proto.RadioLost(tracing.DetachedRoot(ctx), n)
}

func (g *Gateway[S, R, I, C]) removeLocked(n *Radio[S, R]) {
	delete(g.radios, n.conn)
	n.stopPush()

	if n.Setup != nil {
		g.kickLocked()
	}
}

func (g *Gateway[S, R, I, C]) dispatchRadio(ctx context.Context, conn *sctp.SCTPConn, msg []byte) {
	g.mu.Lock()
	n, ok := g.radios[conn]
	g.mu.Unlock()

	if ok {
		g.proto.HandleRadio(ctx, n, msg)
	}
}

func (g *Gateway[S, R, I, C]) RadiosLocked() []*Radio[S, R] {
	out := slices.Collect(maps.Values(g.radios))

	slices.SortFunc(out, func(a, b *Radio[S, R]) int {
		if c := a.connectedAt.Compare(b.connectedAt); c != 0 {
			return c
		}

		return strings.Compare(strings.Join(a.addrs, ","), strings.Join(b.addrs, ","))
	})

	return out
}

func Serving[S Setup](setup S, ops []Operator) []int64 {
	var ids []int64

	for _, o := range ops {
		if setup.Broadcasts(o.PLMNs) {
			ids = append(ids, o.ID)
		}
	}

	return ids
}

func (g *Gateway[S, R, I, C]) servingLocked(setup S) []int64 {
	return Serving(setup, g.operators)
}

func (g *Gateway[S, R, I, C]) OperatorRadiosLocked(operatorID int64) []*Radio[S, R] {
	o, ok := g.OperatorLocked(operatorID)
	if !ok {
		return nil
	}

	return g.broadcastingLocked(o.PLMNs)
}

func (g *Gateway[S, R, I, C]) ClientRadiosLocked(c *Client[I, C]) []*Radio[S, R] {
	return g.broadcastingLocked(c.plmns)
}

func (g *Gateway[S, R, I, C]) broadcastingLocked(plmns []db.PLMN) []*Radio[S, R] {
	var out []*Radio[S, R]

	for _, n := range g.radios {
		if n.Up && n.Setup != nil && (*n.Setup).Broadcasts(plmns) {
			out = append(out, n)
		}
	}

	return out
}

func (g *Gateway[S, R, I, C]) TakeDown(n *Radio[S, R]) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	wasUp := n.Up
	n.Up = false

	return wasUp
}

func (g *Gateway[S, R, I, C]) IsUp(n *Radio[S, R]) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	return n.Up
}

// TS 36.413 clause 10.4
// TS 38.413 clause 10.4
func (g *Gateway[S, R, I, C]) holderLocked(n *Radio[S, R], ref string) *Radio[S, R] {
	for _, other := range g.radios {
		if other != n && other.Setup != nil && (*other.Setup).Ref() == ref {
			return other
		}
	}

	return nil
}

func sameAddresses(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// TS 36.300 clause 4.6.2
// TS 38.300 clause 4.10.2.3
func (g *Gateway[S, R, I, C]) BeginSetup(ctx context.Context, n *Radio[S, R], setup S) ([]int64, error) {
	ref := setup.Ref()

	g.mu.Lock()

	stale := g.holderLocked(n, ref)
	if stale != nil && !sameAddresses(stale.addrs, n.addrs) {
		g.clearSetupLocked(n)
		g.mu.Unlock()

		return nil, ErrNodeInUse
	}

	if stale != nil {
		g.removeLocked(stale)
	}

	serving := g.servingLocked(setup)
	if len(serving) == 0 {
		g.clearSetupLocked(n)
	} else {
		n.stopPush()
		n.Setup, n.Up, n.push = &setup, false, push[R]{}
		n.ref.Store(&ref)

		g.kickLocked()
	}

	g.mu.Unlock()

	if stale != nil {
		_ = stale.conn.Abort()

		stale.Logger(g.log).WarnContext(ctx, "radio association replaced by a new association with the same global node ID")
		g.proto.RadioLost(ctx, stale)
	}

	if len(serving) == 0 {
		return nil, ErrNoOperator
	}

	return serving, nil
}

// TS 36.413 clause 8.7.4
// TS 38.413 clause 8.7.2
func (g *Gateway[S, R, I, C]) Reconfigure(ctx context.Context, n *Radio[S, R], update func(S) (S, bool),
	commit func(added []int64),
) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if n.Setup == nil || !n.Up {
		return ErrNoSetup
	}

	next, retargeted := update(*n.Setup)
	serving := g.servingLocked(next)

	if retargeted && len(serving) == 0 {
		return ErrNoOperator
	}

	before := g.servingLocked(*n.Setup)
	n.Setup = &next

	var added []int64

	for _, id := range serving {
		if !slices.Contains(before, id) {
			added = append(added, id)
		}
	}

	commit(added)
	g.kickLocked()

	return nil
}

type Await int

const (
	AwaitDone Await = iota
	AwaitCoreFailed
	AwaitNoCore
)

func (g *Gateway[S, R, I, C]) AwaitCore(ctx context.Context, n *Radio[S, R], serving []int64, accept func(cfg R) error,
	failed func(info I) bool,
) (Await, []I) {
	start := g.cfg.Now()
	timer := time.NewTimer(g.cfg.SetupWait)

	defer timer.Stop()

	for {
		g.mu.Lock()

		if g.radios[n.conn] != n {
			g.mu.Unlock()
			g.countSetup(metrics.SideRadio, SetupError)
			tracing.SpanFailed(ctx, SetupError)

			return AwaitDone, nil
		}

		if cfg := g.proto.RadioConfigLocked(); !cfg.Empty() && g.anyUpLocked(serving) {
			if err := accept(cfg); err != nil {
				g.clearSetupLocked(n)
				g.mu.Unlock()
				g.countSetup(metrics.SideRadio, SetupError)
				tracing.SpanFailed(ctx, SetupError)
				n.Logger(g.log).WarnContext(ctx, "failed to accept the radio "+g.names.Setup, slog.Any("error", err))

				return AwaitDone, nil
			}

			g.setUpLocked(ctx, n, cfg)
			g.mu.Unlock()

			return AwaitDone, nil
		}

		failures := g.setupFailuresLocked(serving, start, failed)
		changed := g.changed
		g.mu.Unlock()

		if len(failures) > 0 {
			return AwaitCoreFailed, failures
		}

		select {
		case <-changed:
		case <-timer.C:
			g.mu.Lock()
			failures := g.setupFailuresLocked(serving, time.Time{}, failed)
			g.mu.Unlock()

			if len(failures) > 0 {
				return AwaitCoreFailed, failures
			}

			return AwaitNoCore, nil
		case <-ctx.Done():
			g.countSetup(metrics.SideRadio, SetupError)
			tracing.SpanFailed(ctx, SetupError)

			return AwaitDone, nil
		}
	}
}

func (g *Gateway[S, R, I, C]) anyUpLocked(operators []int64) bool {
	for _, c := range g.cores {
		if c.State == Up && slices.Contains(operators, c.operatorID) {
			return true
		}
	}

	return false
}

func (g *Gateway[S, R, I, C]) setupFailuresLocked(operators []int64, since time.Time, failed func(I) bool) []I {
	var out []I

	for c := range g.ClientsLocked() {
		if !slices.Contains(operators, c.operatorID) {
			continue
		}

		if c.State != SetupFailed || !failed(c.Info) || c.Since.Before(since) {
			return nil
		}

		out = append(out, c.Info)
	}

	return out
}

func (g *Gateway[S, R, I, C]) setUpLocked(ctx context.Context, n *Radio[S, R], cfg R) {
	n.Up = true
	n.push.acked = cfg.Pushed()

	g.countSetup(metrics.SideRadio, SetupSuccess)
	g.syncRadioLocked(ctx, n)
	g.proto.RadioUpLocked(ctx, n)
}

func (g *Gateway[S, R, I, C]) clearSetupLocked(n *Radio[S, R]) {
	if n.Setup != nil {
		g.kickLocked()
	}

	n.stopPush()
	n.Setup, n.Up, n.push = nil, false, push[R]{}
	n.ref.Store(nil)
}

func (g *Gateway[S, R, I, C]) SetupRejected(ctx context.Context, n *Radio[S, R], result string) {
	g.mu.Lock()
	g.clearSetupLocked(n)
	g.mu.Unlock()

	g.countSetup(metrics.SideRadio, result)
	tracing.SpanFailed(ctx, result)
}

// TS 36.413 clause 8.7.5
// TS 38.413 clause 8.7.3
func (g *Gateway[S, R, I, C]) syncRadioLocked(ctx context.Context, n *Radio[S, R]) {
	p := &n.push
	if !n.Up || p.pending != nil || g.cfg.Now().Before(p.retryAt) {
		return
	}

	cfg := g.proto.RadioConfigLocked()
	if cfg.Empty() {
		return
	}

	p.acked = g.proto.RadioNotifyLocked(ctx, n, p.acked, cfg)
	if cfg.Equal(p.acked) {
		return
	}

	g.pushLocked(ctx, n, cfg)
}

func (g *Gateway[S, R, I, C]) pushLocked(ctx context.Context, n *Radio[S, R], cfg R) {
	out := g.proto.RadioConfigUpdate(ctx, cfg)
	if out == nil {
		return
	}

	if err := n.Send(ctx, out, NonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send "+g.names.RadioUpdate, slog.Any("error", err))
		return
	}

	pushed := cfg.Pushed()
	n.push.pending = &pushed
	retryCtx := tracing.DetachedRoot(ctx)
	n.push.timer = time.AfterFunc(g.cfg.ResponseTimeout, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		if g.radios[n.conn] == n && n.Up && n.push.pending != nil && (*n.push.pending).Equal(pushed) {
			g.pushLocked(retryCtx, n, cfg)
		}
	})
}

func (g *Gateway[S, R, I, C]) RadioUpdateAcknowledged(ctx context.Context, n *Radio[S, R]) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := &n.push
	if p.pending == nil {
		return
	}

	n.stopPush()

	p.acked, p.pending = *p.pending, nil

	g.syncRadioLocked(ctx, n)
}

func (g *Gateway[S, R, I, C]) RadioUpdateRejected(ctx context.Context, n *Radio[S, R], wait *time.Duration) {
	retry := g.cfg.RetryInterval
	if wait != nil {
		retry = *wait
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	p := &n.push
	if p.pending == nil {
		return
	}

	n.stopPush()

	p.pending = nil
	p.retryAt = g.cfg.Now().Add(retry)
	retryCtx := tracing.DetachedRoot(ctx)
	p.timer = time.AfterFunc(retry, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		if g.radios[n.conn] == n {
			g.syncRadioLocked(retryCtx, n)
		}
	})
}

func (g *Gateway[S, R, I, C]) pagingTargetsLocked(operatorID int64, list []TAI) []*Radio[S, R] {
	o, ok := g.OperatorLocked(operatorID)
	if !ok {
		return nil
	}

	var tais []TAI

	for _, t := range list {
		if slices.Contains(o.PLMNs, t.PLMN) {
			tais = append(tais, t)
		}
	}

	var out []*Radio[S, R]

	for _, n := range g.radios {
		if n.Up && n.Setup != nil && slices.ContainsFunc(tais, (*n.Setup).Serves) {
			out = append(out, n)
		}
	}

	return out
}

// TS 36.413 clause 8.5
// TS 38.413 clause 8.5.1
func (g *Gateway[S, R, I, C]) Page(ctx context.Context, a *Core, log *slog.Logger, tais []TAI, listed int, msg []byte) {
	g.mu.Lock()
	radios := g.pagingTargetsLocked(a.OperatorID(), tais)
	g.mu.Unlock()

	if len(radios) == 0 {
		g.CountPaging(PagingNoRadio)
		tracing.SpanFailed(ctx, PagingNoRadio)
		log.DebugContext(ctx, "Paging from "+g.names.Core+" not relayed: no radio serves a TAI of the list",
			slog.Int("tais", listed))

		return
	}

	g.CountPaging(PagingRelayed)
	log.DebugContext(ctx, "Paging from "+g.names.Core+" relayed to radios", slog.Int("radios", len(radios)))
	SendRadios(ctx, radios, a.Operator(), log, "Paging", msg)
}

func SendRadios[S, R any](ctx context.Context, radios []*Radio[S, R], operator string, log *slog.Logger, what string,
	out []byte,
) {
	for _, n := range radios {
		if err := n.For(operator).Send(ctx, out, NonUEStream); err != nil {
			n.Logger(log).WarnContext(ctx, "failed to send "+what+" to radio", slog.Any("error", err))
		}
	}
}
