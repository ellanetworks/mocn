package e2ebench

import (
	"flag"
	"fmt"
	"os"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var (
	e2eScaleUEs         = flag.Int("e2ebench.scale.ues", 0, "UEs that attach and stay connected in TestE2EScale; 0 skips it")
	e2eScaleRadios      = flag.Int("e2ebench.scale.radios", 100, "eNBs or gNBs TestE2EScale spreads its UEs across")
	e2eScaleConcurrency = flag.Int("e2ebench.scale.concurrency", 256, "attachments TestE2EScale keeps in flight")
	e2eScaleRAT         = flag.String("e2ebench.scale.rat", "5g", "radio access of TestE2EScale: 4g (attach with a default bearer) or 5g (registration with a PDU session)")
	e2eScaleAggregated  = flag.Bool("e2ebench.scale.aggregated", true, "user-plane aggregation in TestE2EScale")
)

const scaleProcedureTimeout = 2 * time.Minute

func TestE2EScale(t *testing.T) {
	nUEs := *e2eScaleUEs
	if nUEs <= 0 {
		t.Skip("set -e2ebench.scale.ues to run")
	}

	var r rat

	switch *e2eScaleRAT {
	case "4g":
		r = rat4G
	case "5g":
		r = rat5G
	default:
		t.Fatalf("unknown -e2ebench.scale.rat %q", *e2eScaleRAT)
	}

	m := newBenchMOCN(t, r, *e2eScaleAggregated)
	m.timeout = scaleProcedureTimeout

	nRadios := *e2eScaleRadios
	start := time.Now()

	m.radios(t, nRadios)

	t.Logf("set up %d radios in %v", nRadios, time.Since(start).Round(time.Millisecond))

	ues := make([]benchUE, nUEs)
	latencies := make([]time.Duration, nUEs)

	for i := range ues {
		ues[i] = benchUE{op: i % len(plmns), id: uint32(i/nRadios) + 1}
		ues[i].ch = m.scaleRadio(i % nRadios).attachUE(uint64(ues[i].id))
	}

	connect := func(i int) error {
		if r == rat4G {
			return m.enbs[i%nRadios].attach(&ues[i])
		}

		if err := m.gnbs[i%nRadios].register(&ues[i]); err != nil {
			return err
		}

		return m.gnbs[i%nRadios].establish(&ues[i])
	}

	disconnect := func(i int) error {
		if r == rat4G {
			return m.enbs[i%nRadios].release(&ues[i])
		}

		return m.gnbs[i%nRadios].release(&ues[i])
	}

	baseline := scaleMemory()

	var done atomic.Int64

	stop := m.progress(t, "connected", &done, nUEs)
	cpu := cpuTime()
	start = time.Now()

	if err := m.forEachUE(nUEs, func(i int) error {
		t0 := time.Now()
		err := connect(i)
		latencies[i] = time.Since(t0)

		done.Add(1)

		return err
	}); err != nil {
		t.Fatal(err)
	}

	elapsed := time.Since(start)
	busy := cpuTime() - cpu

	stop()

	wantTunnels := 0
	if m.aggregated {
		wantTunnels = 2 * nUEs
	}

	if got := m.gatewayUEs(); got != nUEs {
		t.Fatalf("%d UEs in Ella MOCN after connecting %d", got, nUEs)
	}

	if got := m.coreUEs(); got != nUEs {
		t.Fatalf("%d UEs in the cores after connecting %d", got, nUEs)
	}

	if got := m.tunnels(); got != wantTunnels {
		t.Fatalf("%d tunnels after connecting %d UEs, want %d", got, nUEs, wantTunnels)
	}

	loaded := scaleMemory()

	slices.Sort(latencies)

	pct := func(p float64) time.Duration { return latencies[min(int(float64(nUEs)*p), nUEs-1)] }

	t.Logf("connected %d %s UEs (aggregation %v) in %v (%.0f/s) over %d radios and 2 operators, %d in flight",
		nUEs, *e2eScaleRAT, m.aggregated, elapsed.Round(time.Millisecond), float64(nUEs)/elapsed.Seconds(), nRadios,
		*e2eScaleConcurrency)
	t.Logf("CPU time (Ella MOCN and simulators) %v: %.1f cores busy, %v per UE", busy.Round(time.Millisecond),
		busy.Seconds()/elapsed.Seconds(), busy/time.Duration(nUEs))
	t.Logf("connection latency p50 %v p95 %v p99 %v max %v", pct(0.50), pct(0.95), pct(0.99), latencies[nUEs-1])
	t.Logf("process memory (Ella MOCN and simulators): heap in use %d MiB -> %d MiB, RSS %d MiB -> %d MiB, goroutines %d -> %d",
		baseline.heap>>20, loaded.heap>>20, baseline.rss>>20, loaded.rss>>20, baseline.goroutines, loaded.goroutines)
	t.Logf("per connected UE: heap %d B, RSS %d B", perUE(loaded.heap, baseline.heap, nUEs), perUE(loaded.rss, baseline.rss, nUEs))

	done.Store(0)

	stop = m.progress(t, "released", &done, nUEs)
	start = time.Now()

	if err := m.forEachUE(nUEs, func(i int) error {
		err := disconnect(i)

		done.Add(1)

		return err
	}); err != nil {
		t.Fatal(err)
	}

	stop()

	if err := m.verifyIdle(); err != nil {
		t.Fatalf("%v: %d UEs in Ella MOCN, %d in the cores, %d tunnels", err, m.gatewayUEs(), m.coreUEs(), m.tunnels())
	}

	t.Logf("released %d UEs in %v", nUEs, time.Since(start).Round(time.Millisecond))
}

func (m *benchMOCN) scaleRadio(i int) *radio {
	if m.rat == rat4G {
		return &m.enbs[i].radio
	}

	return &m.gnbs[i].radio
}

func (m *benchMOCN) forEachUE(n int, do func(i int) error) error {
	var (
		failures atomic.Int64
		firstErr atomic.Value
		wg       sync.WaitGroup
		sem      = make(chan struct{}, *e2eScaleConcurrency)
	)

	for i := range n {
		sem <- struct{}{}

		wg.Go(func() {
			defer func() { <-sem }()

			if err := do(i); err != nil {
				failures.Add(1)
				firstErr.CompareAndSwap(nil, fmt.Errorf("UE %d: %w", i, err))
			}
		})
	}

	wg.Wait()

	if err, _ := firstErr.Load().(error); err != nil {
		return fmt.Errorf("%d of %d UEs failed, first: %w", failures.Load(), n, err)
	}

	return nil
}

func (m *benchMOCN) progress(t *testing.T, what string, done *atomic.Int64, n int) func() {
	start := time.Now()
	ticker := time.NewTicker(10 * time.Second)
	stop := make(chan struct{})
	exited := make(chan struct{})

	go func() {
		defer close(exited)

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				t.Logf("%d/%d %s, %d UEs in Ella MOCN, %v elapsed", done.Load(), n, what, m.gatewayUEs(), time.Since(start).Round(time.Second))
			}
		}
	}()

	return func() {
		ticker.Stop()
		close(stop)
		<-exited
	}
}

func perUE(loaded, baseline uint64, n int) int64 {
	return (int64(loaded) - int64(baseline)) / int64(n)
}

func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}

	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

type scaleMem struct {
	heap       uint64
	rss        uint64
	goroutines int
}

func scaleMemory() scaleMem {
	goruntime.GC()

	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)

	m := scaleMem{heap: ms.HeapInuse, goroutines: goruntime.NumGoroutine()}

	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return m
	}

	for line := range strings.SplitSeq(string(status), "\n") {
		if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			var kb uint64
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d kB", &kb); err == nil {
				m.rss = kb << 10
			}
		}
	}

	return m
}
