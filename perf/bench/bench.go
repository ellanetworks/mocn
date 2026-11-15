package bench

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/ellanetworks/mocn/perf/agent"
	"github.com/ellanetworks/mocn/perf/fake"
	"github.com/ellanetworks/mocn/perf/packet"
	"github.com/ellanetworks/mocn/perf/rx"
)

const (
	Aggregated = "aggregated"
	Direct     = "direct"
	Uplink     = "uplink"
	Downlink   = "downlink"
	directTEID = 0x0ffffff0
	settle     = 500 * time.Millisecond
	pause      = 2 * time.Second
	margin     = time.Second
	maxRetries = 10
	achieved   = 0.995
)

var (
	metricNames = []string{
		"ellamocn_user_plane_packets_total",
		"ellamocn_user_plane_dropped_packets_total",
		"ellamocn_user_plane_slow_path_packets_total",
		"ellamocn_user_plane_slow_path_relayed_total",
	}
	errNoSessions = errors.New("no sessions")
)

type Config struct {
	Gen        string        `json:"gen"`
	Sink       string        `json:"sink"`
	MOCN       string        `json:"mocn"`
	RAN        string        `json:"ran"`
	Core       string        `json:"core"`
	Metrics    string        `json:"metrics"`
	Proto      string        `json:"proto"`
	DirectSink netip.Addr    `json:"direct_sink"`
	Sizes      []int         `json:"sizes"`
	Directions []string      `json:"directions"`
	Paths      []string      `json:"paths"`
	Trial      time.Duration `json:"trial"`
	Confirm    time.Duration `json:"confirm"`
	Precision  float64       `json:"precision"`
	GenCPUs    []int         `json:"gen_cpus"`
	SinkCPUs   []int         `json:"sink_cpus"`
	Batch      int           `json:"batch"`
	Modes      []rx.Mode     `json:"modes"`
	PingCount  int           `json:"ping_count"`
	PingGap    time.Duration `json:"ping_interval"`
	PingSize   int           `json:"ping_size"`
	Logger     *slog.Logger  `json:"-"`
}

type Trial struct {
	Rate      float64       `json:"rate"`
	Sent      uint64        `json:"sent"`
	Received  uint64        `json:"received"`
	Elapsed   float64       `json:"elapsed_s"`
	Behind    bool          `json:"behind"`
	RxMode    rx.Mode       `json:"rx_mode"`
	Lossless  bool          `json:"lossless"`
	Achieved  bool          `json:"achieved"`
	MOCN      Load          `json:"mocn"`
	Sender    Load          `json:"sender"`
	Receiver  Load          `json:"receiver"`
	Metrics   Metrics       `json:"metrics"`
	MOCNNet   NetDelta      `json:"mocn_net"`
	SenderIf  string        `json:"sender_interface"`
	CounterIf string        `json:"counter_interface"`
	When      time.Time     `json:"when"`
	PerCPU    []float64     `json:"mocn_per_cpu"`
	Softnet   agent.Softnet `json:"mocn_softnet"`
}

type Load struct {
	AvgPct     float64 `json:"avg_pct"`
	MaxPct     float64 `json:"max_pct"`
	SoftIRQPct float64 `json:"softirq_pct"`
	Cores      float64 `json:"cores"`
}

type NetDelta map[string]agent.Iface

type Result struct {
	Path      string  `json:"path"`
	Direction string  `json:"direction"`
	Size      int     `json:"size"`
	FrameLen  int     `json:"frame_len"`
	Pps       float64 `json:"pps"`
	InnerGbps float64 `json:"inner_gbps"`
	WireGbps  float64 `json:"wire_gbps"`
	Limit     string  `json:"limit"`
	Confirmed Trial   `json:"confirmed"`
	Trials    []Trial `json:"trials"`
}

type Ping struct {
	Path    string       `json:"path"`
	Latency fake.Latency `json:"latency"`
}

type Run struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Config   Config    `json:"config"`
	Sessions int       `json:"sessions"`
	Radios   int       `json:"radios"`
	Gateways int       `json:"gateways"`
	Results  []Result  `json:"results"`
	Pings    []Ping    `json:"pings"`
}

type scenario struct {
	path, dir string
	tx, rx    string
	cpus      []int
	flows     []packet.Flow
	peer      netip.Addr
	uplink    bool
}

func Execute(ctx context.Context, cfg Config, save func(Run) error) (Run, error) {
	run := Run{Started: time.Now(), Config: cfg}

	var ranS, coreS []fake.Session

	if err := call(ctx, "GET", cfg.RAN+"/sessions", nil, &ranS); err != nil {
		return run, err
	}

	if err := call(ctx, "GET", cfg.Core+"/sessions", nil, &coreS); err != nil {
		return run, err
	}

	if len(ranS) == 0 || len(coreS) == 0 {
		return run, errNoSessions
	}

	run.Sessions = len(ranS)
	run.Radios = distinct(ranS)
	run.Gateways = distinct(coreS)

	for _, path := range cfg.Paths {
		for _, dir := range cfg.Directions {
			sc := buildScenario(cfg, path, dir, ranS, coreS)

			for _, size := range cfg.Sizes {
				r, err := search(ctx, cfg, sc, size)
				if err != nil {
					return run, fmt.Errorf("%s %s %d B: %w", path, dir, size, err)
				}

				run.Results = append(run.Results, r)
				cfg.Logger.InfoContext(ctx, "result", slog.String("path", path), slog.String("direction", dir),
					slog.Int("size", size), slog.Float64("mpps", r.Pps/1e6), slog.Float64("inner_gbps", r.InnerGbps),
					slog.String("limit", r.Limit), slog.Float64("mocn_cpu_avg", r.Confirmed.MOCN.AvgPct))

				if err := save(run); err != nil {
					return run, err
				}
			}
		}
	}

	if cfg.PingCount > 0 {
		for _, path := range cfg.Paths {
			to := fake.Endpoint{Addr: ranS[0].Peer, TEID: ranS[0].PeerTEID}
			if path == Direct {
				to = fake.Endpoint{Addr: cfg.DirectSink, TEID: directTEID}
			}

			var l fake.Latency

			err := call(ctx, "POST", cfg.RAN+"/ping", fake.PingConfig{
				From: ranS[0].Local, To: to, Count: cfg.PingCount, Interval: cfg.PingGap, Size: cfg.PingSize,
			}, &l)
			if err != nil {
				return run, err
			}

			run.Pings = append(run.Pings, Ping{Path: path, Latency: l})
			cfg.Logger.InfoContext(ctx, "latency", slog.String("path", path), slog.Float64("avg_us", l.AvgUs),
				slog.Int("received", l.Received))
		}
	}

	run.Finished = time.Now()

	return run, save(run)
}

func distinct(ss []fake.Session) int {
	seen := map[netip.Addr]bool{}
	for _, s := range ss {
		seen[s.Local] = true
	}

	return len(seen)
}

func buildScenario(cfg Config, path, dir string, ranS, coreS []fake.Session) scenario {
	sc := scenario{path: path, dir: dir, uplink: dir == Uplink}

	switch {
	case path == Aggregated && dir == Uplink:
		sc.tx, sc.rx, sc.cpus, sc.peer = cfg.Gen, cfg.Sink, cfg.GenCPUs, coreS[0].Peer
		for _, s := range ranS {
			sc.flows = append(sc.flows, packet.Flow{Src: s.Local, Dst: s.Peer, SrcPort: packet.Port, TEID: s.PeerTEID})
		}
	case path == Aggregated:
		sc.tx, sc.rx, sc.cpus, sc.peer = cfg.Sink, cfg.Gen, cfg.SinkCPUs, ranS[0].Peer
		for _, s := range coreS {
			sc.flows = append(sc.flows, packet.Flow{Src: s.Local, Dst: s.Peer, SrcPort: packet.Port, TEID: s.PeerTEID})
		}
	case dir == Uplink:
		sc.tx, sc.rx, sc.cpus, sc.peer = cfg.Gen, cfg.Sink, cfg.GenCPUs, ranS[0].Local
		for _, s := range ranS {
			sc.flows = append(sc.flows, packet.Flow{
				Src: s.Local, Dst: cfg.DirectSink, SrcPort: packet.Port,
				TEID: s.LocalTEID,
			})
		}
	default:
		sc.tx, sc.rx, sc.cpus, sc.peer = cfg.Sink, cfg.Gen, cfg.SinkCPUs, cfg.DirectSink
		for _, s := range ranS {
			sc.flows = append(sc.flows, packet.Flow{
				Src: cfg.DirectSink, Dst: s.Local, SrcPort: packet.Port,
				TEID: s.LocalTEID,
			})
		}
	}

	return sc
}

func search(ctx context.Context, cfg Config, sc scenario, size int) (Result, error) {
	o := packet.Options{Inner: size, Ext: cfg.Proto == "ng", Uplink: sc.uplink}
	res := Result{Path: sc.path, Direction: sc.dir, Size: size, FrameLen: o.FrameLen()}

	record := func(t Trial) {
		res.Trials = append(res.Trials, t)
		cfg.Logger.InfoContext(ctx, "trial", slog.String("path", sc.path), slog.String("direction", sc.dir),
			slog.Int("size", size), slog.Float64("rate_mpps", t.Rate/1e6),
			slog.Float64("sent_mpps", float64(t.Sent)/t.Elapsed/1e6), slog.Uint64("lost", t.Sent-min(t.Sent, t.Received)),
			slog.Bool("lossless", t.Lossless), slog.Bool("achieved", t.Achieved), slog.Float64("mocn_cpu", t.MOCN.AvgPct))
	}

	top, err := trial(ctx, cfg, sc, size, 0, cfg.Trial)
	if err != nil {
		return res, err
	}

	record(top)

	maxRate := float64(top.Sent) / top.Elapsed

	if top.Lossless {
		res.Limit = "generator"
		res.Confirmed = top
		res.finish(maxRate)

		return res, nil
	}

	lo, hi := 0.0, maxRate
	rate := float64(top.Received) / top.Elapsed * 0.98
	res.Limit = "loss"

	for hi-lo > cfg.Precision*hi {
		t, err := trial(ctx, cfg, sc, size, rate, cfg.Trial)
		if err != nil {
			return res, err
		}

		record(t)

		switch {
		case t.Lossless && t.Achieved:
			lo = rate
		case t.Lossless:
			lo, hi = float64(t.Sent)/t.Elapsed, float64(t.Sent)/t.Elapsed
			res.Limit = "generator"
		default:
			hi = rate
		}

		rate = (lo + hi) / 2
	}

	var last Trial

	for range maxRetries {
		if lo == 0 {
			break
		}

		t, err := trial(ctx, cfg, sc, size, lo, cfg.Confirm)
		if err != nil {
			return res, err
		}

		record(t)

		if t.Lossless {
			res.Confirmed = t
			res.finish(float64(t.Sent) / t.Elapsed)

			return res, nil
		}

		last = t
		lo *= 1 - 2*cfg.Precision
	}

	res.Limit = "unconfirmed"
	res.Confirmed = last

	return res, nil
}

func (r *Result) finish(pps float64) {
	r.Pps = pps
	r.InnerGbps = pps * float64(r.Size) * 8 / 1e9
	r.WireGbps = pps * float64(r.FrameLen) * 8 / 1e9
}

func trial(ctx context.Context, cfg Config, sc scenario, size int, rate float64, d time.Duration) (Trial, error) {
	t := Trial{Rate: rate, When: time.Now()}

	time.Sleep(pause)

	var start agent.RxResponse
	if err := call(ctx, "POST", sc.rx+"/rx/start", agent.RxRequest{Peer: sc.peer, Modes: cfg.Modes}, &start); err != nil {
		return t, err
	}

	stop := func() (agent.RxResponse, error) {
		var r agent.RxResponse

		err := call(ctx, "POST", sc.rx+"/rx/stop", nil, &r)

		return r, err
	}

	before, err := snapshot(ctx, cfg, sc)
	if err != nil {
		_, _ = stop()
		return t, err
	}

	type txResult struct {
		r   agent.TxResponse
		err error
	}

	done := make(chan txResult, 1)

	go func() {
		var r agent.TxResponse

		err := call(ctx, "POST", sc.tx+"/tx", agent.TxRequest{
			Flows: sc.flows, Size: size, Ext: cfg.Proto == "ng", Uplink: sc.uplink, Rate: rate, Duration: d,
			CPUs: sc.cpus, Batch: cfg.Batch,
		}, &r)
		done <- txResult{r, err}
	}()

	time.Sleep(margin)

	mid1, err1 := snapshot(ctx, cfg, sc)

	time.Sleep(d - 2*margin)

	mid2, err2 := snapshot(ctx, cfg, sc)
	sent := <-done

	time.Sleep(settle)

	got, errStop := stop()

	after, err3 := snapshot(ctx, cfg, sc)
	if err := errors.Join(err1, err2, err3, sent.err, errStop); err != nil {
		return t, err
	}

	t.Sent, t.Elapsed, t.Behind = sent.r.Sent, sent.r.Elapsed.Seconds(), sent.r.Behind
	t.Received, t.RxMode = got.Packets, got.Mode
	t.SenderIf, t.CounterIf = sent.r.Interface, got.Interface
	t.Lossless = t.Received >= t.Sent && t.Sent > 0
	t.Achieved = rate == 0 || float64(t.Sent) >= rate*t.Elapsed*achieved
	t.MOCN, t.PerCPU = load(mid1.mocn, mid2.mocn)
	t.Sender, _ = load(mid1.tx, mid2.tx)
	t.Receiver, _ = load(mid1.rx, mid2.rx)
	t.Metrics = after.metrics.delta(before.metrics)
	t.MOCNNet = netDelta(before.mocn, after.mocn)
	t.Softnet = agent.Softnet{
		Processed: after.mocn.Softnet.Processed - before.mocn.Softnet.Processed,
		Dropped:   after.mocn.Softnet.Dropped - before.mocn.Softnet.Dropped,
		Squeezed:  after.mocn.Softnet.Squeezed - before.mocn.Softnet.Squeezed,
	}

	return t, nil
}

type snap struct {
	mocn, tx, rx agent.Stats
	metrics      Metrics
}

func snapshot(ctx context.Context, cfg Config, sc scenario) (snap, error) {
	var s snap

	m, err := scrape(ctx, cfg.Metrics, metricNames)
	if err != nil {
		return s, err
	}

	s.metrics = m

	return s, errors.Join(
		call(ctx, "GET", cfg.MOCN+"/stats", nil, &s.mocn),
		call(ctx, "GET", sc.tx+"/stats", nil, &s.tx),
		call(ctx, "GET", sc.rx+"/stats", nil, &s.rx),
	)
}

func load(a, b agent.Stats) (Load, []float64) {
	var (
		l           Load
		per         []float64
		busy, total uint64
		soft        uint64
	)

	for i := range min(len(a.CPUs), len(b.CPUs)) {
		tot := b.CPUs[i].Total() - a.CPUs[i].Total()
		bz := b.CPUs[i].Busy() - a.CPUs[i].Busy()
		busy += bz
		total += tot
		soft += b.CPUs[i].SoftIRQ - a.CPUs[i].SoftIRQ

		pct := 0.0
		if tot > 0 {
			pct = float64(bz) / float64(tot) * 100
		}

		per = append(per, pct)
		l.MaxPct = max(l.MaxPct, pct)
	}

	if total > 0 {
		l.AvgPct = float64(busy) / float64(total) * 100
		l.SoftIRQPct = float64(soft) / float64(total) * 100
		l.Cores = l.AvgPct / 100 * float64(len(per))
	}

	return l, per
}

func netDelta(a, b agent.Stats) NetDelta {
	d := NetDelta{}

	for name, after := range b.Ifaces {
		before := a.Ifaces[name]
		d[name] = agent.Iface{
			RxPackets: after.RxPackets - before.RxPackets, RxDropped: after.RxDropped - before.RxDropped,
			TxPackets: after.TxPackets - before.TxPackets, TxDropped: after.TxDropped - before.TxDropped,
		}
	}

	return d
}
