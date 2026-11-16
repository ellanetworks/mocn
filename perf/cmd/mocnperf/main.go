package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/perf/agent"
	"github.com/ellanetworks/mocn/perf/bench"
	"github.com/ellanetworks/mocn/perf/fake"
	"github.com/ellanetworks/mocn/perf/rx"
)

const usage = `usage: mocnperf <command> [flags]

commands:
  agent   traffic generator, packet counter and host statistics, over HTTP
  ran     fake eNBs or gNBs that set up sessions through Ella MOCN
  core    fake MME and AMF, and S-GW/UPF GTP-U reflector
  bench   RFC 2544 throughput search and latency, driven through the agents
  report  summarize bench result files as Markdown tables
`

var errUsage = errors.New("invalid arguments")

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmds := map[string]func(context.Context, *slog.Logger, []string) error{
		"agent": runAgent, "ran": runRAN, "core": runCore, "bench": runBench, "report": runReport,
	}

	cmd, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	if err := cmd(ctx, log, os.Args[2:]); err != nil {
		log.ErrorContext(ctx, "failed", slog.String("command", os.Args[1]), slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func serve(ctx context.Context, log *slog.Logger, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		_ = srv.Shutdown(sctx)
	}()

	log.InfoContext(ctx, "listening", slog.String("address", addr))

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func runAgent(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	listen := fs.String("listen", ":7000", "HTTP listen address")

	if err := fs.Parse(args); err != nil {
		return err
	}

	return serve(ctx, log, *listen, agent.New(log).Handler())
}

func runRAN(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("ran", flag.ContinueOnError)
	listen := fs.String("listen", ":7001", "HTTP listen address")
	proto := fs.String("proto", "s1", "s1 (eNBs) or ng (gNBs)")
	mocn := fs.String("mocn", "", "Ella MOCN signalling address")
	radios := fs.String("radios", "", "first radio address, e.g. 10.60.0.64")
	count := fs.Int("count", 1, "number of radios, at consecutive addresses")
	sessions := fs.Int("sessions", 1, "number of UE sessions across the radios")
	window := fs.Int("window", 16, "session setups in flight per radio")
	plmn := fs.String("plmn", "00101", "PLMN, MCC then MNC")
	out := fs.String("out", "", "file to write the sessions to, as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	m, err := netip.ParseAddr(*mocn)
	if err != nil {
		return fmt.Errorf("%w: -mocn: %w", errUsage, err)
	}

	addrs, err := consecutive(*radios, *count)
	if err != nil {
		return err
	}

	p, err := parsePLMN(*plmn)
	if err != nil {
		return err
	}

	start := time.Now()

	r, err := fake.StartRAN(ctx, fake.RANConfig{
		Proto: *proto, PLMN: p, MOCN: m, Radios: addrs, Sessions: *sessions, Window: *window, Logger: log,
	})
	if err != nil {
		return err
	}
	defer r.Close()

	log.InfoContext(ctx, "sessions established", slog.Int("sessions", len(r.Sessions())),
		slog.Int("radios", len(addrs)), slog.Duration("took", time.Since(start)))

	if err := write(*out, r.Sessions()); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions", func(w http.ResponseWriter, _ *http.Request) { reply(w, r.Sessions()) })
	mux.HandleFunc("POST /ping", func(w http.ResponseWriter, req *http.Request) {
		var cfg fake.PingConfig
		if err := json.NewDecoder(req.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		l, err := r.Ping(req.Context(), cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		reply(w, l)
	})

	return serve(ctx, log, *listen, mux)
}

func runCore(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("core", flag.ContinueOnError)
	listen := fs.String("listen", ":7002", "HTTP listen address")
	sctpAddr := fs.String("sctp", "", "MME and AMF address")
	gateways := fs.String("gateways", "", "first S-GW/UPF address")
	count := fs.Int("count", 1, "number of S-GW/UPF addresses, consecutive, assigned to sessions in turn")
	plmn := fs.String("plmn", "00101", "PLMN, MCC then MNC")
	out := fs.String("out", "", "file to write the sessions to, as JSON, on exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	a, err := netip.ParseAddr(*sctpAddr)
	if err != nil {
		return fmt.Errorf("%w: -sctp: %w", errUsage, err)
	}

	gws, err := consecutive(*gateways, *count)
	if err != nil {
		return err
	}

	p, err := parsePLMN(*plmn)
	if err != nil {
		return err
	}

	c, err := fake.StartCore(ctx, fake.CoreConfig{PLMN: p, Listen: a, Gateways: gws, Logger: log})
	if err != nil {
		return err
	}
	defer c.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions", func(w http.ResponseWriter, _ *http.Request) { reply(w, c.Sessions()) })

	err = serve(ctx, log, *listen, mux)

	return errors.Join(err, write(*out, c.Sessions()))
}

func runBench(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	cfg := bench.Config{Logger: log}
	fs.StringVar(&cfg.Gen, "gen", "http://10.60.0.10:7000", "radio-side agent")
	fs.StringVar(&cfg.Sink, "sink", "http://10.61.0.30:7000", "operator-side agent")
	fs.StringVar(&cfg.MOCN, "mocn", "http://10.60.0.20:7000", "Ella MOCN host agent")
	fs.StringVar(&cfg.RAN, "ran", "http://10.60.0.10:7001", "fake radios")
	fs.StringVar(&cfg.Core, "core", "http://10.61.0.30:7002", "fake core")
	fs.StringVar(&cfg.Metrics, "metrics", "http://10.60.0.20:5030/api/v1/metrics", "Ella MOCN metrics")
	fs.StringVar(&cfg.Proto, "proto", "s1", "s1 or ng: ng adds the PDU Session Container extension header")
	direct := fs.String("direct-sink", "10.60.0.30", "operator-side host address on the radio network, for the direct path")
	sizes := fs.String("sizes", "46,494,1456", "subscriber IP packet sizes, in bytes")
	dirs := fs.String("directions", "uplink,downlink", "directions")
	paths := fs.String("paths", "aggregated,direct", "aggregated (through Ella MOCN), direct (without)")
	fs.DurationVar(&cfg.Trial, "trial", 10*time.Second, "search trial duration")
	fs.DurationVar(&cfg.Confirm, "confirm", 30*time.Second, "confirmation trial duration")
	fs.Float64Var(&cfg.Precision, "precision", 0.01, "search resolution, as a fraction of the rate")
	genCPUs := fs.String("gen-cpus", "2,3,4,5,6,7", "generator CPUs on the radio side")
	sinkCPUs := fs.String("sink-cpus", "2,3,4,5,6,7", "generator CPUs on the operator side")
	fs.IntVar(&cfg.Batch, "batch", 32, "packets per sendmmsg")
	modes := fs.String("rx-modes", "native,generic", "XDP attach modes to try, in order")
	fs.IntVar(&cfg.PingCount, "ping-count", 1000, "latency probes per path, 0 to skip")
	fs.DurationVar(&cfg.PingGap, "ping-interval", 10*time.Millisecond, "latency probe interval")
	fs.IntVar(&cfg.PingSize, "ping-size", 84, "latency probe subscriber IP packet size, in bytes")
	out := fs.String("out", "results.json", "result file")

	if err := fs.Parse(args); err != nil {
		return err
	}

	var err error

	if cfg.DirectSink, err = netip.ParseAddr(*direct); err != nil {
		return fmt.Errorf("%w: -direct-sink: %w", errUsage, err)
	}

	if cfg.Sizes, err = ints(*sizes); err != nil {
		return err
	}

	if cfg.GenCPUs, err = ints(*genCPUs); err != nil {
		return err
	}

	if cfg.SinkCPUs, err = ints(*sinkCPUs); err != nil {
		return err
	}

	cfg.Directions, cfg.Paths = strings.Split(*dirs, ","), strings.Split(*paths, ",")
	for _, m := range strings.Split(*modes, ",") {
		cfg.Modes = append(cfg.Modes, rx.Mode(m))
	}

	_, err = bench.Execute(ctx, cfg, func(r bench.Run) error { return write(*out, r) })

	return err
}

func runReport(_ context.Context, _ *slog.Logger, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: no result files", errUsage)
	}

	var runs []bench.Run

	for _, f := range args {
		b, err := readIn(f)
		if err != nil {
			return err
		}

		var r bench.Run
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}

		runs = append(runs, r)
	}

	_, err := os.Stdout.WriteString(bench.Report(runs))

	return err
}

func readIn(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	return root.ReadFile(filepath.Base(path))
}

func consecutive(first string, n int) ([]netip.Addr, error) {
	a, err := netip.ParseAddr(first)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	out := make([]netip.Addr, 0, n)
	for range n {
		out = append(out, a)
		a = a.Next()
	}

	return out, nil
}

func parsePLMN(s string) (db.PLMN, error) {
	if len(s) != 5 && len(s) != 6 {
		return db.PLMN{}, fmt.Errorf("%w: PLMN %q", errUsage, s)
	}

	return db.PLMN{MCC: s[:3], MNC: s[3:]}, nil
}

func ints(s string) ([]int, error) {
	var out []int

	for f := range strings.SplitSeq(s, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", errUsage, s, err)
		}

		out = append(out, v)
	}

	return out, nil
}

func write(path string, v any) error {
	if path == "" {
		return nil
	}

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, b, 0o600)
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
