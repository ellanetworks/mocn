package assoc

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 36.413 clause 9.2.1.54
// TS 36.413 clause 9.2.1.70
// TS 38.413 clause 9.3.1.43
// TS 38.413 clause 9.3.1.44
const maxnoofWarningItems = 65535

type Area[ID comparable, C any] struct {
	ID    ID
	Cells []C
}

// TS 36.413 clause 9.2.1.54
// TS 36.413 clause 9.2.1.70
// TS 38.413 clause 9.3.1.43
// TS 38.413 clause 9.3.1.44
type Areas[T, E comparable, C any] struct {
	Cells []C
	TAIs  []Area[T, C]
	EAIs  []Area[E, C]
}

func (a *Areas[T, E, C]) AddCells(cells ...C) {
	a.Cells = addCells(a.Cells, cells)
}

func (a *Areas[T, E, C]) AddTAI(tai T, cells []C) {
	a.TAIs = addArea(a.TAIs, tai, cells)
}

func (a *Areas[T, E, C]) AddEAI(eai E, cells []C) {
	a.EAIs = addArea(a.EAIs, eai, cells)
}

func (a Areas[T, E, C]) Empty() bool {
	return len(a.Cells) == 0 && len(a.TAIs) == 0 && len(a.EAIs) == 0
}

func (a Areas[T, E, C]) OneKind() Areas[T, E, C] {
	kinds := 0

	for _, n := range []int{len(a.Cells), len(a.TAIs), len(a.EAIs)} {
		if n > 0 {
			kinds++
		}
	}

	if kinds <= 1 {
		return a
	}

	cells := a.Cells

	for _, t := range a.TAIs {
		cells = addCells(cells, t.Cells)
	}

	for _, e := range a.EAIs {
		cells = addCells(cells, e.Cells)
	}

	return Areas[T, E, C]{Cells: cells}
}

func addCells[C any](dst, cells []C) []C {
	return append(dst, cells[:min(len(cells), maxnoofWarningItems-len(dst))]...)
}

func addArea[ID comparable, C any](dst []Area[ID, C], id ID, cells []C) []Area[ID, C] {
	i := slices.IndexFunc(dst, func(a Area[ID, C]) bool { return a.ID == id })
	if i < 0 {
		if len(dst) == maxnoofWarningItems {
			return dst
		}

		dst = append(dst, Area[ID, C]{ID: id})
		i = len(dst) - 1
	}

	dst[i].Cells = addCells(dst[i].Cells, cells)

	return dst
}

func Convert[A, B any](in []A, f func(A) B) []B {
	if in == nil {
		return nil
	}

	out := make([]B, 0, len(in))
	for _, a := range in {
		out = append(out, f(a))
	}

	return out
}

type Warnings[K comparable, T any] struct {
	timeout time.Duration
	mu      sync.Mutex
	pending map[*sctp.SCTPConn][]*warning[K, T]
}

type warning[K comparable, T any] struct {
	key     K
	waiting int
	got     []T
	timer   *time.Timer
	ended   bool
	done    func(got []T, missing int)
}

func NewWarnings[K comparable, T any](timeout time.Duration) *Warnings[K, T] {
	return &Warnings[K, T]{timeout: timeout, pending: map[*sctp.SCTPConn][]*warning[K, T]{}}
}

func (w *Warnings[K, T]) start(key K, conns []*sctp.SCTPConn, done func(got []T, missing int)) *warning[K, T] {
	x := &warning[K, T]{key: key, waiting: len(conns), done: done}

	w.mu.Lock()
	defer w.mu.Unlock()

	for _, c := range conns {
		w.pending[c] = append(w.pending[c], x)
	}

	x.timer = time.AfterFunc(w.timeout, func() { w.expire(x) })

	return x
}

func (w *Warnings[K, T]) Answer(conn *sctp.SCTPConn, key K, got T) bool {
	w.mu.Lock()

	x := w.takeLocked(conn, func(x *warning[K, T]) bool { return x.key == key })
	if x == nil {
		w.mu.Unlock()
		return false
	}

	x.got = append(x.got, got)
	end := x.answeredLocked()
	w.mu.Unlock()

	end()

	return true
}

func (w *Warnings[K, T]) drop(conn *sctp.SCTPConn, x *warning[K, T]) {
	w.mu.Lock()

	if w.takeLocked(conn, func(y *warning[K, T]) bool { return y == x }) == nil {
		w.mu.Unlock()
		return
	}

	end := x.answeredLocked()
	w.mu.Unlock()

	end()
}

func (w *Warnings[K, T]) RadioLost(conn *sctp.SCTPConn) {
	w.mu.Lock()

	xs := w.pending[conn]
	delete(w.pending, conn)

	ends := make([]func(), 0, len(xs))
	for _, x := range xs {
		ends = append(ends, x.answeredLocked())
	}

	w.mu.Unlock()

	for _, end := range ends {
		end()
	}
}

func (w *Warnings[K, T]) takeLocked(conn *sctp.SCTPConn, match func(*warning[K, T]) bool) *warning[K, T] {
	xs := w.pending[conn]

	i := slices.IndexFunc(xs, match)
	if i < 0 {
		return nil
	}

	x := xs[i]

	if xs = slices.Delete(xs, i, i+1); len(xs) == 0 {
		delete(w.pending, conn)
	} else {
		w.pending[conn] = xs
	}

	return x
}

func (x *warning[K, T]) answeredLocked() func() {
	x.waiting--

	if x.waiting > 0 || x.ended {
		return func() {}
	}

	x.ended = true
	x.timer.Stop()
	got := x.got

	return func() { x.done(got, 0) }
}

func (w *Warnings[K, T]) expire(x *warning[K, T]) {
	w.mu.Lock()

	if x.ended {
		w.mu.Unlock()
		return
	}

	x.ended = true

	for conn := range w.pending {
		w.takeLocked(conn, func(y *warning[K, T]) bool { return y == x })
	}

	got, missing := x.got, x.waiting
	w.mu.Unlock()

	x.done(got, missing)
}

type Warning[S any, K comparable, T any] struct {
	Procedure string
	What      string
	Key       K
	Covers    func(S) bool
	Msg       []byte
	Answer    func(ctx context.Context, got []T)
}

// TS 36.300 clause 4.6.2
// TS 38.300 clause 4.10.2.3
// TS 23.251 clause 4.5
// TS 23.041 clause 3.4
func (g *Gateway[S, R, I, C]) warningRadiosLocked(a *Core, covers func(S) bool) ([]*Radio[S, R], string) {
	if o, ok := g.OperatorLocked(a.OperatorID()); !ok || !o.PWS {
		return nil, PWSNotPWSOperator
	}

	var out []*Radio[S, R]

	for _, n := range g.RadiosLocked() {
		if n.Up && n.Setup != nil && covers(*n.Setup) {
			out = append(out, n)
		}
	}

	if len(out) == 0 {
		return nil, PWSNoRadio
	}

	return out, PWSRelayed
}

// TS 36.413 clause 8.12.1
// TS 36.413 clause 8.12.2
// TS 38.413 clause 8.9.1
// TS 38.413 clause 8.9.2
// TS 36.300 clause 4.6.2
// TS 38.300 clause 4.10.2.3
func Warn[S Setup, R RadioConfig[R], I any, C Equaler[C], K comparable, T any](ctx context.Context,
	g *Gateway[S, R, I, C], w *Warnings[K, T], a *Core, log *slog.Logger, req Warning[S, K, T],
) {
	answerCtx := context.WithoutCancel(ctx)

	g.mu.Lock()

	radios, result := g.warningRadiosLocked(a, req.Covers)

	var x *warning[K, T]

	if result == PWSRelayed {
		conns := make([]*sctp.SCTPConn, 0, len(radios))
		for _, n := range radios {
			conns = append(conns, n.conn)
		}

		x = w.start(req.Key, conns, func(got []T, missing int) {
			if missing > 0 {
				g.cfg.Metrics.PWS.WithLabelValues(g.names.Interface, req.Procedure, PWSTimeout).Add(float64(missing))
				log.WarnContext(answerCtx, "radios did not answer the "+req.What+" in time", slog.Int("radios", missing))
			}

			req.Answer(answerCtx, got)
		})
	}

	g.mu.Unlock()

	g.CountPWS(req.Procedure, result)

	switch result {
	case PWSNotPWSOperator:
		log.InfoContext(ctx, req.What+" from "+g.names.Core+" of an operator that is not the PWS operator, "+
			"answered with no broadcast area")
		req.Answer(ctx, nil)

		return
	case PWSNoRadio:
		tracing.SpanFailed(ctx, PWSNoRadio)
		log.InfoContext(ctx, req.What+" from "+g.names.Core+" not relayed: no radio that is up covers the warning area, "+
			"answered with no broadcast area")
		req.Answer(ctx, nil)

		return
	}

	log.InfoContext(ctx, req.What+" from "+g.names.Core+" relayed to radios", slog.Int("radios", len(radios)))

	for _, n := range radios {
		if err := n.For(a.Operator()).Send(ctx, req.Msg, NonUEStream); err != nil {
			n.Logger(log).WarnContext(ctx, "failed to send "+req.What+" to radio", slog.Any("error", err))
			w.drop(n.conn, x)
		}
	}
}

// TS 36.413 clause 9.1.13.5
const maxRestartsPerRadio = 256

const maxRestartRadios = 1024

type restart struct {
	cells []string
	build func(o Operator) ([]byte, error)
}

func (g *Gateway[S, R, I, C]) pwsCoresLocked() (Operator, []*Core, bool) {
	for _, o := range g.operators {
		if !o.PWS {
			continue
		}

		var cores []*Core
		for _, c := range g.UpLocked(o.ID) {
			cores = append(cores, c.Assoc)
		}

		return o, cores, true
	}

	return Operator{}, nil, false
}

func sendPWS(ctx context.Context, log *slog.Logger, what string, o Operator, cores []*Core,
	build func(o Operator) ([]byte, error),
) {
	out, err := build(o)
	if err != nil {
		log.ErrorContext(ctx, "failed to build "+what, slog.Any("error", err))
		return
	}

	for _, a := range cores {
		if err := a.Send(ctx, out, NonUEStream); err != nil {
			a.Logger(log).WarnContext(ctx, "failed to send "+what, slog.Any("error", err))
		}
	}
}

// TS 23.007 clause 15A.1.2
func (g *Gateway[S, R, I, C]) supersedeRestartsLocked(ref string, cells []string) {
	kept := slices.DeleteFunc(g.restarts[ref], func(r restart) bool {
		return !slices.ContainsFunc(r.cells, func(c string) bool { return !slices.Contains(cells, c) })
	})

	if len(kept) == 0 {
		delete(g.restarts, ref)
		g.restartRadios = slices.DeleteFunc(g.restartRadios, func(r string) bool { return r == ref })

		return
	}

	g.restarts[ref] = kept
}

// TS 23.007 clause 15A.1.2
// TS 23.007 clause 15A.2
func (g *Gateway[S, R, I, C]) keepRestartLocked(ref string, r restart) {
	if _, ok := g.restarts[ref]; !ok {
		if len(g.restartRadios) == maxRestartRadios {
			delete(g.restarts, g.restartRadios[0])
			g.restartRadios = g.restartRadios[1:]
		}

		g.restartRadios = append(g.restartRadios, ref)
	}

	kept := g.restarts[ref]
	if len(kept) == maxRestartsPerRadio {
		kept = kept[1:]
	}

	g.restarts[ref] = append(kept, r)
}

// TS 36.300 clause 4.6.2
// TS 38.300 clause 4.10.2.3
// TS 23.007 clause 15A.1.2
// TS 23.251 clause 4.5
func (g *Gateway[S, R, I, C]) PWSIndication(ctx context.Context, n *Radio[S, R], log *slog.Logger, proc, what string,
	cells []string, build func(o Operator) ([]byte, error),
) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if n.Setup == nil {
		return
	}

	ref := (*n.Setup).Ref()
	restarted := proc == PWSRestartIndication

	o, cores, ok := g.pwsCoresLocked()

	switch {
	case !ok:
		g.CountPWS(proc, PWSNoPWSOperator)
		tracing.SpanFailed(ctx, PWSNoPWSOperator)
		log.WarnContext(ctx, what+" from radio dropped: no operator with "+g.names.Core+"s is the PWS operator")
	case len(cores) == 0 && restarted:
		g.CountPWS(proc, g.names.NoCore)
		tracing.SpanFailed(ctx, g.names.NoCore)
		g.supersedeRestartsLocked(ref, cells)
		g.keepRestartLocked(ref, restart{cells: cells, build: build})
		log.WarnContext(ctx, what+" from radio kept until an "+g.names.Core+" of the PWS operator is up")
	case len(cores) == 0:
		g.CountPWS(proc, g.names.NoCore)
		tracing.SpanFailed(ctx, g.names.NoCore)
		log.WarnContext(ctx, what+" from radio dropped: no "+g.names.Core+" of the PWS operator is up")
	default:
		if restarted {
			g.supersedeRestartsLocked(ref, cells)
		}

		g.CountPWS(proc, PWSRelayed)
		sendPWS(ctx, log, what, o, cores, build)
		log.InfoContext(ctx, what+" from radio relayed to the PWS operator", slog.Int(g.names.CoreAttr+"s", len(cores)))
	}
}

// TS 23.007 clause 15A.1.2
// TS 23.007 clause 15A.2
func (g *Gateway[S, R, I, C]) sendRestartsLocked(ctx context.Context) {
	if len(g.restarts) == 0 {
		return
	}

	o, cores, ok := g.pwsCoresLocked()

	switch {
	case !ok:
		g.log.WarnContext(ctx, "kept PWS Restart Indications dropped: no operator with "+g.names.Core+
			"s is the PWS operator", slog.Int("radios", len(g.restarts)))
	case len(cores) == 0:
		return
	default:
		for _, ref := range g.restartRadios {
			log := g.log.With(slog.String("radio", ref))

			for _, r := range g.restarts[ref] {
				sendPWS(ctx, log, "PWS Restart Indication", o, cores, r.build)
			}

			log.InfoContext(ctx, "kept PWS Restart Indications from radio relayed to the PWS operator",
				slog.Int("indications", len(g.restarts[ref])))
		}
	}

	clear(g.restarts)
	g.restartRadios = nil
}
