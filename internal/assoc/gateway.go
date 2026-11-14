package assoc

import (
	"context"
	"iter"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"go.opentelemetry.io/otel/trace"
)

const NonUEStream = 0

type Protocol[S Setup, R RadioConfig[R], I any, C Equaler[C]] interface {
	Nodes(o db.Operator) []Node[C]
	CoreConfig(plmns []db.PLMN, setups []S) (C, bool)
	ConfigurationUpdate(want, acked C) ([]byte, error)
	ConfigAttrs(cfg C) []slog.Attr
	HandleCore(ctx context.Context, s *Session[S, R, I, C], msg []byte) error
	CoreLostLocked(c *Client[I, C], a *Core) func(ctx context.Context)
	RadioUpLocked(ctx context.Context, n *Radio[S, R])
	RadioConfigLocked() R
	RadioConfigUpdate(ctx context.Context, cfg R) []byte
	RadioNotifyLocked(ctx context.Context, n *Radio[S, R], acked, want R) R
	HandleRadio(ctx context.Context, n *Radio[S, R], msg []byte)
	RadioLost(ctx context.Context, n *Radio[S, R])
	Overloaded(info I) bool
	UEsByOperator() map[int64]int
}

type Gateway[S Setup, R RadioConfig[R], I any, C Equaler[C]] struct {
	cfg     Config
	names   Names
	proto   Protocol[S, R, I, C]
	log     *slog.Logger
	tx      *Transport
	clients sync.WaitGroup

	reconcileMu sync.Mutex

	mu        sync.Mutex
	loop      *loop
	operators []Operator
	cores     map[string]*Client[I, C]
	stopping  map[*Client[I, C]]struct{}
	radios    map[*sctp.SCTPConn]*Radio[S, R]
	restarts  map[*sctp.SCTPConn][]restart
	changed   chan struct{}
}

type loop struct {
	requests chan request
	done     chan struct{}
}

type request struct {
	ops  []db.Operator
	done chan struct{}
}

func New[S Setup, R RadioConfig[R], I any, C Equaler[C]](cfg Config, names Names, inspect func(msg []byte) SpanInfo,
	proto Protocol[S, R, I, C],
) *Gateway[S, R, I, C] {
	cfg = cfg.withDefaults(names)

	var tracer trace.Tracer
	if cfg.TracerProvider != nil {
		tracer = cfg.TracerProvider.Tracer(names.Tracer)
	}

	g := &Gateway[S, R, I, C]{
		cfg:      cfg,
		names:    names,
		proto:    proto,
		log:      cfg.Logger,
		tx:       newTransport(names.PPID, names.Protocol, tracer, inspect),
		cores:    map[string]*Client[I, C]{},
		stopping: map[*Client[I, C]]struct{}{},
		radios:   map[*sctp.SCTPConn]*Radio[S, R]{},
		restarts: map[*sctp.SCTPConn][]restart{},
		changed:  make(chan struct{}),
	}

	g.initMetrics()

	return g
}

func (g *Gateway[S, R, I, C]) Config() Config {
	return g.cfg
}

func (g *Gateway[S, R, I, C]) Transport() *Transport {
	return g.tx
}

func (g *Gateway[S, R, I, C]) Run(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	l := &loop{requests: make(chan request), done: make(chan struct{})}

	go g.reconcileLoop(loopCtx, l)

	g.mu.Lock()
	g.loop = l
	g.mu.Unlock()

	g.loadOperators(ctx)

	var server *sctp.Server

	if g.cfg.Listener != nil {
		radioCtx := context.WithoutCancel(ctx)

		server = sctp.NewServer(sctp.Config{PPID: g.names.PPID, Name: g.names.Server, Logger: g.log}, sctp.Callbacks{
			Dispatch:     g.dispatchRadio,
			OnConnect:    func(conn *sctp.SCTPConn) { g.radioConnected(radioCtx, conn) },
			OnDisconnect: func(conn *sctp.SCTPConn) { g.radioDisconnected(radioCtx, conn) },
		})
		server.Serve(loopCtx, g.cfg.Listener)
	}

	<-ctx.Done()

	if server != nil {
		serverCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), serverShutdownTimeout)
		server.Shutdown(serverCtx)
		stop()
	}

	g.mu.Lock()
	g.loop = nil
	g.mu.Unlock()

	cancel()
	<-l.done

	done := make(chan struct{})

	go func() {
		g.clients.Wait()
		close(done)
	}()

	timer := time.NewTimer(clientShutdownTimeout)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		g.log.WarnContext(ctx, "timed out closing "+g.names.Core+" associations")
	}
}

func (g *Gateway[S, R, I, C]) reconcileLoop(ctx context.Context, l *loop) {
	defer close(l.done)

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-l.requests:
			g.mu.Lock()

			for _, c := range g.applyLocked(req.ops, true) {
				g.startClientLocked(ctx, c)
			}

			g.kickLocked()
			g.mu.Unlock()

			close(req.done)
		}
	}
}

func (g *Gateway[S, R, I, C]) loadOperators(ctx context.Context) {
	backoff := g.cfg.ReconnectMin

	for {
		err := g.Reconcile(ctx)
		if err == nil {
			return
		}

		g.log.ErrorContext(ctx, "failed to load operators", slog.Any("error", err), slog.Duration("retry_in", backoff))

		if !sleep(ctx, backoff) {
			return
		}

		backoff = min(2*backoff, g.cfg.ReconnectMax)
	}
}

func (g *Gateway[S, R, I, C]) Reconcile(ctx context.Context) error {
	if g.cfg.Operators == nil {
		return nil
	}

	g.reconcileMu.Lock()
	defer g.reconcileMu.Unlock()

	ops, err := g.cfg.Operators(ctx)
	if err != nil {
		return err
	}

	g.mu.Lock()
	l := g.loop
	g.mu.Unlock()

	if !g.submit(ctx, l, ops) {
		g.mu.Lock()
		g.applyLocked(ops, false)
		g.kickLocked()
		g.mu.Unlock()
	}

	g.mu.Lock()
	g.changedLocked(ctx)
	g.mu.Unlock()

	return nil
}

func (g *Gateway[S, R, I, C]) submit(ctx context.Context, l *loop, ops []db.Operator) bool {
	if l == nil {
		return false
	}

	req := request{ops: ops, done: make(chan struct{})}

	select {
	case l.requests <- req:
	case <-l.done:
		return false
	case <-ctx.Done():
		return false
	}

	<-req.done

	return true
}

func (g *Gateway[S, R, I, C]) applyLocked(ops []db.Operator, start bool) []*Client[I, C] {
	g.operators = g.operators[:0]
	wanted := map[string]bool{}

	var added []*Client[I, C]

	for _, o := range ops {
		nodes := g.proto.Nodes(o)
		if len(nodes) == 0 {
			continue
		}

		g.operators = append(g.operators, Operator{
			ID: o.ID, Name: o.Name, PLMNs: slices.Clone(o.PLMNs), PWS: o.PWSOperator, GlobalENBID: o.GlobalENBID,
			GlobalGNBID: o.GlobalGNBID,
		})

		for _, node := range nodes {
			wanted[node.Key] = true

			if c, ok := g.cores[node.Key]; ok {
				c.name.Store(&o.Name)
				c.plmns = slices.Clone(o.PLMNs)

				continue
			}

			if !start {
				continue
			}

			c := g.newClient(o, node)
			g.cores[node.Key] = c
			added = append(added, c)
		}
	}

	for key, c := range g.cores {
		if !wanted[key] {
			delete(g.cores, key)
			g.stopping[c] = struct{}{}
			c.cancel()
		}
	}

	return added
}

// TS 36.412 clause 7
// TS 38.412 clause 7
func (g *Gateway[S, R, I, C]) startClientLocked(ctx context.Context, c *Client[I, C]) {
	var prev []*Client[I, C]

	for p := range g.stopping {
		if slices.ContainsFunc(p.addrs, func(a string) bool { return slices.Contains(c.addrs, a) }) {
			prev = append(prev, p)
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	g.clients.Add(1)

	go func() {
		defer g.clients.Done()
		defer close(c.done)
		defer cancel()
		defer func() {
			g.mu.Lock()
			delete(g.stopping, c)
			g.mu.Unlock()
		}()

		for _, p := range prev {
			select {
			case <-p.done:
			case <-ctx.Done():
				return
			}
		}

		g.runClient(ctx, c)
	}()
}

func (g *Gateway[S, R, I, C]) View(fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()

	fn()
}

func (g *Gateway[S, R, I, C]) UpdateCore(ctx context.Context, a *Core, apply func(c *Client[I, C]) bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	c, ok := g.clientOfLocked(a)
	if !ok {
		return false
	}

	if apply(c) {
		g.changedLocked(ctx)
	}

	return true
}

func (g *Gateway[S, R, I, C]) OperatorLocked(id int64) (Operator, bool) {
	for _, o := range g.operators {
		if o.ID == id {
			return o, true
		}
	}

	return Operator{}, false
}

func (g *Gateway[S, R, I, C]) OperatorsLocked() []Operator {
	return g.operators
}

func (g *Gateway[S, R, I, C]) OperatorName(id int64) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	o, _ := g.OperatorLocked(id)

	return o.Name
}

func (g *Gateway[S, R, I, C]) ClientsLocked() iter.Seq[*Client[I, C]] {
	return func(yield func(*Client[I, C]) bool) {
		for _, key := range slices.Sorted(maps.Keys(g.cores)) {
			if !yield(g.cores[key]) {
				return
			}
		}
	}
}

func (g *Gateway[S, R, I, C]) ClientLocked(operatorID int64, addrs []string) (*Client[I, C], bool) {
	for _, c := range g.cores {
		if c.operatorID == operatorID && slices.Equal(c.addrs, addrs) {
			return c, true
		}
	}

	return nil, false
}

func (g *Gateway[S, R, I, C]) UpLocked(operatorID int64) []*Client[I, C] {
	var out []*Client[I, C]

	for c := range g.ClientsLocked() {
		if c.operatorID == operatorID && c.State == Up && c.Assoc != nil {
			out = append(out, c)
		}
	}

	return out
}

func (g *Gateway[S, R, I, C]) kickLocked() {
	for _, c := range g.cores {
		c.kick()
	}
}

func (g *Gateway[S, R, I, C]) changedLocked(ctx context.Context) {
	close(g.changed)
	g.changed = make(chan struct{})

	for _, n := range g.radios {
		g.syncRadioLocked(ctx, n)
	}

	g.sendRestartsLocked(ctx)
}

func (g *Gateway[S, R, I, C]) clientOfLocked(a *Core) (*Client[I, C], bool) {
	for _, c := range g.cores {
		if c.Assoc == a {
			return c, true
		}
	}

	return nil, false
}

func (g *Gateway[S, R, I, C]) OperatorNames() map[int64]string {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make(map[int64]string, len(g.operators))
	for _, o := range g.operators {
		out[o.ID] = o.Name
	}

	return out
}
