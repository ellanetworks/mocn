package s1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

// TS 36.412 clause 7
const (
	PPID = 18
	Port = 36412
)

const (
	defaultSetupWait       = 4 * time.Second
	defaultResponseTimeout = 10 * time.Second
	defaultRetryInterval   = 10 * time.Second
	defaultReconnectMin    = time.Second
	defaultReconnectMax    = 30 * time.Second
	shutdownTimeout        = 5 * time.Second
	mmeStreams             = 8
)

type Config struct {
	Listener        *sctp.Listener
	Operators       func(ctx context.Context) ([]db.Operator, error)
	Relay           Relay
	TracerProvider  trace.TracerProvider
	Logger          *slog.Logger
	MMEPort         int
	SetupWait       time.Duration
	ResponseTimeout time.Duration
	RetryInterval   time.Duration
	ReconnectMin    time.Duration
	ReconnectMax    time.Duration
	Now             func() time.Time
	IntN            func(n int) int
}

type Gateway struct {
	cfg       Config
	log       *slog.Logger
	tracer    trace.Tracer
	setups    *prometheus.CounterVec
	overloads *prometheus.CounterVec
	relay     *relay
	runCtx    context.Context
	clients   sync.WaitGroup

	reconcileMu sync.Mutex

	mu        sync.Mutex
	operators []operatorView
	mmes      map[string]*mmeClient
	stopping  map[*mmeClient]struct{}
	ranNodes  map[*sctp.SCTPConn]*RANAssociation
	changed   chan struct{}
}

func New(cfg Config) *Gateway {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	for _, d := range []struct {
		v   *time.Duration
		def time.Duration
	}{
		{&cfg.SetupWait, defaultSetupWait},
		{&cfg.ResponseTimeout, defaultResponseTimeout},
		{&cfg.RetryInterval, defaultRetryInterval},
		{&cfg.ReconnectMin, defaultReconnectMin},
		{&cfg.ReconnectMax, defaultReconnectMax},
	} {
		if *d.v <= 0 {
			*d.v = d.def
		}
	}

	if cfg.MMEPort == 0 {
		cfg.MMEPort = Port
	}

	g := &Gateway{
		cfg:       cfg,
		log:       cfg.Logger,
		mmes:      map[string]*mmeClient{},
		stopping:  map[*mmeClient]struct{}{},
		ranNodes:  map[*sctp.SCTPConn]*RANAssociation{},
		changed:   make(chan struct{}),
		setups:    newSetupCounter(),
		overloads: newOverloadCounter(),
	}

	if cfg.TracerProvider != nil {
		g.tracer = cfg.TracerProvider.Tracer(tracerName)
	}

	g.relay = newRelay(g, cfg.IntN)
	if g.cfg.Relay == nil {
		g.cfg.Relay = g.relay
	}

	return g
}

func (g *Gateway) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	g.mu.Lock()
	g.runCtx = runCtx
	g.mu.Unlock()

	g.loadOperators(ctx)

	var server *sctp.Server

	if g.cfg.Listener != nil {
		server = sctp.NewServer(sctp.Config{PPID: PPID, Name: "S1-MME", Logger: g.log}, sctp.Callbacks{
			Dispatch:     g.dispatchRAN,
			OnConnect:    g.ranConnected,
			OnDisconnect: g.ranDisconnected,
		})
		server.Serve(runCtx, g.cfg.Listener)
	}

	<-ctx.Done()

	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer stop()

	if server != nil {
		server.Shutdown(shutdownCtx)
	}

	g.mu.Lock()
	g.runCtx = nil
	g.mu.Unlock()

	cancel()

	done := make(chan struct{})

	go func() {
		g.clients.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-shutdownCtx.Done():
		g.log.WarnContext(ctx, "timed out closing MME associations")
	}

	return nil
}

func (g *Gateway) loadOperators(ctx context.Context) {
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

func (g *Gateway) Reconcile(ctx context.Context) error {
	if g.cfg.Operators == nil {
		return nil
	}

	g.reconcileMu.Lock()
	defer g.reconcileMu.Unlock()

	ops, err := g.cfg.Operators(ctx)
	if err != nil {
		return err
	}

	g.apply(ctx, ops)

	return nil
}

func mmeKey(o db.Operator, addrs []string) string {
	e := o.GlobalENBID

	return fmt.Sprintf("%d|%s|%s|%d|%s", o.ID, e.PLMN, e.Type, e.ID, strings.Join(addrs, ","))
}

func (g *Gateway) apply(ctx context.Context, ops []db.Operator) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.operators = g.operators[:0]
	wanted := map[string]bool{}

	var added []*mmeClient

	for _, o := range ops {
		if o.GlobalENBID == nil || len(o.MMEs) == 0 {
			continue
		}

		g.operators = append(g.operators, operatorView{id: o.ID, name: o.Name, plmns: slices.Clone(o.PLMNs)})

		for _, m := range o.MMEs {
			key := mmeKey(o, m.TNLAddresses)
			wanted[key] = true

			if c, ok := g.mmes[key]; ok {
				c.setOperatorName(o.Name)
				continue
			}

			if g.runCtx == nil {
				continue
			}

			c := newMMEClient(g, o.ID, o.Name, *o.GlobalENBID, m.TNLAddresses)
			g.mmes[key] = c
			added = append(added, c)
		}
	}

	for key, c := range g.mmes {
		if !wanted[key] {
			delete(g.mmes, key)
			g.stopping[c] = struct{}{}
			c.cancel()
		}
	}

	for _, c := range added {
		g.startClientLocked(c)
	}

	g.kickMMEsLocked()
	g.mmeChangedLocked(ctx)
}

// TS 36.412 clause 7
func (g *Gateway) startClientLocked(c *mmeClient) {
	var prev []*mmeClient

	for p := range g.stopping {
		if slices.ContainsFunc(p.addrs, func(a string) bool { return slices.Contains(c.addrs, a) }) {
			prev = append(prev, p)
		}
	}

	ctx, cancel := context.WithCancel(g.runCtx)
	c.cancel = cancel

	g.clients.Add(1)

	go func() {
		defer g.clients.Done()
		defer close(c.done)
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

		c.run(ctx)
	}()
}

func (g *Gateway) operatorLocked(id int64) (operatorView, bool) {
	for _, o := range g.operators {
		if o.id == id {
			return o, true
		}
	}

	return operatorView{}, false
}

func (g *Gateway) ranSetupsLocked() []ranSetup {
	setups := make([]ranSetup, 0, len(g.ranNodes))

	for _, n := range g.ranNodes {
		if n.setup != nil {
			setups = append(setups, *n.setup)
		}
	}

	return setups
}

func (g *Gateway) enbConfigLocked(operatorID int64) (enbConfig, bool) {
	o, ok := g.operatorLocked(operatorID)
	if !ok {
		return enbConfig{}, false
	}

	return enbConfigFor(o.plmns, g.ranSetupsLocked())
}

func (g *Gateway) servedGUMMEIsLocked() []ServedGUMMEIs {
	pools := make([]mmePool, 0, len(g.mmes))

	for _, key := range sortedKeys(g.mmes) {
		c := g.mmes[key]
		if c.status.State != MMEUp {
			continue
		}

		if o, ok := g.operatorLocked(c.operatorID); ok {
			pools = append(pools, mmePool{plmns: o.plmns, served: c.status.ServedGUMMEIs})
		}
	}

	return mergeServedGUMMEIs(pools)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

func (g *Gateway) kickMMEsLocked() {
	for _, c := range g.mmes {
		c.kick()
	}
}

func (g *Gateway) mmeChangedLocked(ctx context.Context) {
	close(g.changed)
	g.changed = make(chan struct{})

	for _, n := range g.ranNodes {
		g.syncRANLocked(ctx, n)
	}
}

func (g *Gateway) MMEStatus(operatorID int64, tnlAddresses []string) (MMEStatus, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, c := range g.mmes {
		if c.operatorID == operatorID && slices.Equal(c.addrs, tnlAddresses) {
			return c.snapshotLocked(), true
		}
	}

	return MMEStatus{}, false
}

func (g *Gateway) UEs() []UE {
	ues := g.relay.snapshot()

	g.mu.Lock()
	defer g.mu.Unlock()

	for i := range ues {
		if o, ok := g.operatorLocked(ues[i].OperatorID); ok {
			ues[i].OperatorName = o.name
		}
	}

	slices.SortStableFunc(ues, func(a, b UE) int { return a.Since.Compare(b.Since) })

	return ues
}

func (g *Gateway) RANNodes() []RANNode {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make([]RANNode, 0, len(g.ranNodes))
	for _, n := range g.ranNodes {
		out = append(out, n.snapshotLocked())
	}

	slices.SortFunc(out, func(a, b RANNode) int {
		if c := a.ConnectedSince.Compare(b.ConnectedSince); c != 0 {
			return c
		}

		return strings.Compare(strings.Join(a.TNLAddresses, ","), strings.Join(b.TNLAddresses, ","))
	})

	return out
}

var errClosed = errors.New("association closed")

func send(conn *sctp.SCTPConn, msg []byte, stream uint16) error {
	if conn == nil {
		return errClosed
	}

	_, err := conn.WriteMsg(msg, &sctp.SndRcvInfo{Stream: stream, PPID: sctp.PPIDWireOrder(PPID)})

	return err
}

func responseDiagnostics(proc s1ap.ProcedureCode, d s1ap.Diagnostics) *s1ap.CriticalityDiagnostics {
	if !d.ReportRequired() {
		return nil
	}

	return &s1ap.CriticalityDiagnostics{
		ProcedureCriticality:      s1ap.Ptr(s1ap.ProcedureCriticality(proc)),
		IEsCriticalityDiagnostics: d.Report(),
	}
}
