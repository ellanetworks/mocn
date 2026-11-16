package e2ebench

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"os"
	goruntime "runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ellanetworks/core/per/ap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/logging"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/sctptest"
)

var (
	e2eBenchLogLevel     = flag.String("e2ebench.loglevel", "info", "Ella MOCN log level in E2E benchmarks")
	e2eBenchLogFile      = flag.String("e2ebench.logfile", "", "file the Ella MOCN logs of E2E benchmarks are written to; empty discards them")
	e2eBenchUEsPerRadio  = flag.Int("e2ebench.ues-per-radio", 1, "UEs that share one eNB or gNB in the par variants of UE benchmarks")
	e2eBenchPagingRadios = flag.Int("e2ebench.paging.radios", 8, "eNBs or gNBs every Paging fans out to in the paging benchmarks")
)

const e2eBenchTimeout = 5 * time.Second

var (
	plmns     = [2]db.PLMN{{MCC: "001", MNC: "01"}, {MCC: "002", MNC: "002"}}
	coreAddrs = [2]string{"127.0.40.1", "127.0.40.2"}
	ranGW     = netip.MustParseAddr("10.1.0.1")
	coreGW    = netip.MustParseAddr("127.0.0.1")
	coreUPs   = [2]netip.Addr{netip.MustParseAddr("10.45.0.1"), netip.MustParseAddr("10.45.0.2")}
	radioUP   = netip.MustParseAddr("10.90.1.21")
)

func radioTEID(radio int, ue uint32) uint32 {
	return uint32(radio+1)<<20 | ue&0xfffff
}

var securityKey = [32]byte{
	0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf,
	0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf,
}

var errTimeout = errors.New("timed out")

func TestMain(m *testing.M) {
	flag.Parse()

	if f := flag.Lookup("test.bench"); f != nil && f.Value.String() != "" {
		fmt.Printf("e2e-log-level: %s\n", *e2eBenchLogLevel)
		fmt.Printf("e2e-ues-per-radio: %d\n", *e2eBenchUEsPerRadio)
		fmt.Printf("e2e-paging-radios: %d\n", *e2eBenchPagingRadios)
	}

	os.Exit(m.Run())
}

type rat int

const (
	rat4G rat = iota
	rat5G
)

type benchMOCN struct {
	owner       testing.TB
	rat         rat
	aggregated  bool
	plane       *gtpu.Plane
	s1          *s1.Gateway
	ng          *ng.Gateway
	gw          *sctptest.Gateway
	uesPerRadio int
	timeout     time.Duration

	failOnce sync.Once
	firstErr error
	broken   chan struct{}
	readers  sync.WaitGroup

	connMu sync.Mutex
	conns  []*sctp.SCTPConn

	mmes [2]*mme
	amfs [2]*amf

	mu      sync.Mutex
	enbs    []*enb
	gnbs    []*gnb
	workers []*benchWorker
}

func newBenchMOCN(tb testing.TB, r rat, aggregated bool) *benchMOCN {
	tb.Helper()

	sctptest.SkipWithoutSCTP(tb)

	m := &benchMOCN{
		owner: tb, rat: r, aggregated: aggregated, broken: make(chan struct{}), uesPerRadio: max(*e2eBenchUEsPerRadio, 1),
		timeout: e2eBenchTimeout,
	}
	tb.Cleanup(m.close)

	logger := benchLogger(tb)

	port := 0
	ops := make([]db.Operator, len(plmns))

	for i := range ops {
		ln := m.listenCore(tb, coreAddrs[i], port)
		port = ln.Addr().(*sctp.SCTPAddr).Port

		ops[i] = db.Operator{
			ID: int64(i + 1), Name: fmt.Sprintf("op%d", i+1), PLMNs: []db.PLMN{plmns[i]},
			UserPlane: db.UserPlane{Aggregation: aggregated},
		}
		nodes := []db.CoreNode{{TNLAddresses: []string{coreAddrs[i]}}}

		switch r {
		case rat4G:
			ops[i].GlobalENBID, ops[i].MMEs = &db.GlobalENBID{PLMN: plmns[i], Type: db.ENBIDMacro, ID: uint32(0x100 + i)}, nodes
			m.mmes[i] = &mme{m: m, op: i, plmn: mustPLMN[s1PLMN](plmns[i]), ues: map[uint32]uint32{}}
			m.serveCore(ln, s1PPID, &m.mmes[i].coreNode, m.mmes[i].handle)
		case rat5G:
			ops[i].GlobalGNBID, ops[i].AMFs = &db.GlobalGNBID{PLMN: plmns[i], ID: uint32(0x100 + i), Length: 22}, nodes
			m.amfs[i] = &amf{m: m, op: i, plmn: mustPLMN[ngPLMN](plmns[i]), ues: map[uint64]uint32{}}
			m.serveCore(ln, ngPPID, &m.amfs[i].coreNode, m.amfs[i].handle)
		}
	}

	if aggregated {
		m.plane = gtpu.New(gtpu.Config{RANAddress: ranGW, RANInterface: "lo", Host: sctptest.LoopbackHost{}, Logger: logger})

		up := make([]gtpu.Operator, 0, len(ops))
		for _, o := range ops {
			up = append(up, gtpu.Operator{ID: o.ID, Name: o.Name, Aggregated: true})
		}

		if err := m.plane.SetOperators(up); err != nil {
			tb.Fatalf("set the user-plane operators: %v", err)
		}
	}

	ln := sctptest.Listen(tb)
	src := &sctptest.Operators{}
	src.Set(ops...)

	cfg := assoc.Config{
		Listener: ln, Operators: src.List, Metrics: metrics.New(), Logger: logger, CorePort: port, Plane: m.plane,
	}

	switch r {
	case rat4G:
		m.s1 = s1.New(cfg)
		m.gw = sctptest.Start(tb, ln, src, m.s1.Run)
	case rat5G:
		m.ng = ng.New(cfg)
		m.gw = sctptest.Start(tb, ln, src, m.ng.Run)
	}

	return m
}

func mustPLMN[T ~[3]byte](p db.PLMN) T {
	id, err := plmnid.Encode[T](p)
	if err != nil {
		panic(err)
	}

	return id
}

var benchLogs struct {
	once sync.Once
	sink io.Writer
	err  error
}

func benchLogger(tb testing.TB) *slog.Logger {
	tb.Helper()

	var level slog.Level
	if err := level.UnmarshalText([]byte(*e2eBenchLogLevel)); err != nil {
		tb.Fatalf("parse -e2ebench.loglevel: %v", err)
	}

	benchLogs.once.Do(func() {
		benchLogs.sink = io.Discard

		if *e2eBenchLogFile != "" {
			benchLogs.sink, benchLogs.err = os.OpenFile(*e2eBenchLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		}
	})

	if benchLogs.err != nil {
		tb.Fatalf("open -e2ebench.logfile: %v", benchLogs.err)
	}

	return logging.New(benchLogs.sink, level)
}

func (m *benchMOCN) fail(err error) {
	m.failOnce.Do(func() {
		m.firstErr = err
		close(m.broken)
	})
}

func (m *benchMOCN) err() error {
	select {
	case <-m.broken:
		return m.firstErr
	default:
		return nil
	}
}

func (m *benchMOCN) track(conn *sctp.SCTPConn) {
	m.connMu.Lock()
	m.conns = append(m.conns, conn)
	m.connMu.Unlock()
}

func (m *benchMOCN) close() {
	if m.gw != nil {
		m.gw.Stop()
	}

	m.connMu.Lock()
	for _, c := range m.conns {
		_ = c.Abort()
	}
	m.connMu.Unlock()

	m.readers.Wait()
}

func (m *benchMOCN) listenCore(tb testing.TB, ip string, port int) *sctp.Listener {
	tb.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(context.Background(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.ParseIP(ip)}}, Port: port})
	if err != nil {
		tb.Fatalf("listen %s: %v", ip, err)
	}

	tb.Cleanup(func() { _ = ln.Close() })

	return ln
}

type coreNode struct {
	cur atomic.Pointer[node]
}

func (c *coreNode) node() (node, error) {
	n := c.cur.Load()
	if n == nil {
		return node{}, errors.New("no association from Ella MOCN")
	}

	return *n, nil
}

func (m *benchMOCN) serveCore(ln *sctp.Listener, ppid uint32, c *coreNode, handle func(n node, raw []byte) error) {
	m.readers.Go(func() {
		accepted := 0

		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			if err := conn.PrepareAccepted(slog.New(slog.DiscardHandler)); err != nil {
				_ = conn.Abort()

				m.fail(fmt.Errorf("prepare the association from Ella MOCN: %w", err))

				continue
			}

			m.track(conn)

			if accepted++; accepted > 1 {
				m.fail(fmt.Errorf("second association with the core network node at %s", ln.Addr()))
			}

			n := node{conn: conn, ppid: ppid}
			c.cur.Store(&n)
			m.serve(n, func(raw []byte) error { return handle(n, raw) })
		}
	})
}

func (m *benchMOCN) dialRadio(tb testing.TB, ppid uint32) node {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), e2eBenchTimeout)
	defer cancel()

	conn, err := sctp.Dial(ctx, "sctp", nil, m.gw.Addr, sctp.InitMsg{NumOstreams: 2, MaxInstreams: 2})
	if err != nil {
		tb.Fatalf("dial Ella MOCN: %v", err)
	}

	m.track(conn)

	return node{conn: conn, ppid: ppid}
}

func (m *benchMOCN) serve(n node, handle func(raw []byte) error) {
	m.readers.Go(func() {
		buf := make([]byte, sctp.MaxMessageSize)

		for {
			size, info, err := n.conn.ReadMsg(buf)
			if err != nil {
				return
			}

			if info == nil || sctp.PPIDWireOrder(info.PPID) != n.ppid {
				continue
			}

			if err := handle(slices.Clone(buf[:size])); err != nil {
				m.fail(err)
			}
		}
	})
}

func awaitOn[T any](m *benchMOCN, ch <-chan T) (T, error) {
	timer := time.NewTimer(m.timeout)
	defer timer.Stop()

	var zero T

	select {
	case v := <-ch:
		return v, nil
	case <-m.broken:
		return zero, m.firstErr
	case <-timer.C:
		return zero, errTimeout
	}
}

func (m *benchMOCN) await(ch <-chan []byte) ([]byte, error) {
	return awaitOn(m, ch)
}

func (m *benchMOCN) eventually(what string, ok func() bool) error {
	deadline := time.Now().Add(m.timeout)

	for !ok() {
		if err := m.err(); err != nil {
			return err
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%w waiting for %s", errTimeout, what)
		}

		time.Sleep(time.Millisecond)
	}

	return nil
}

func (m *benchMOCN) coresUp() bool {
	for i, addr := range coreAddrs {
		id, addrs := int64(i+1), []string{addr}

		switch m.rat {
		case rat4G:
			if st, ok := m.s1.MMEStatus(id, addrs); !ok || st.State != s1.MMEUp {
				return false
			}
		case rat5G:
			if st, ok := m.ng.AMFStatus(id, addrs); !ok || st.State != ng.AMFUp {
				return false
			}
		}
	}

	return true
}

func (m *benchMOCN) gatewayUEs() int {
	if m.rat == rat4G {
		return len(m.s1.UEs())
	}

	return len(m.ng.UEs())
}

func (m *benchMOCN) coreUEs() int {
	n := 0

	for i := range plmns {
		if m.rat == rat4G {
			n += m.mmes[i].count()
		} else {
			n += m.amfs[i].count()
		}
	}

	return n
}

func (m *benchMOCN) tunnels() int {
	if m.plane == nil {
		return 0
	}

	return m.plane.Tunnels()
}

func (m *benchMOCN) verifyIdle() error {
	if err := m.err(); err != nil {
		return err
	}

	return m.eventually("every UE context and tunnel released", func() bool {
		return m.gatewayUEs() == 0 && m.coreUEs() == 0 && m.tunnels() == 0
	})
}

func (m *benchMOCN) mustVerifyIdle(tb testing.TB) {
	tb.Helper()

	if err := m.verifyIdle(); err != nil {
		tb.Fatalf("%v: %d UEs in Ella MOCN, %d in the cores, %d tunnels", err, m.gatewayUEs(), m.coreUEs(), m.tunnels())
	}
}

func (m *benchMOCN) checkUplink(op int, tla ap.TransportLayerAddress) error {
	want := coreUPs[op]
	if m.aggregated {
		want = ranGW
	}

	if got, _ := netip.AddrFromSlice(tla.Bytes()); got != want {
		return fmt.Errorf("uplink tunnel toward %v reached the radio, want %v", got, want)
	}

	return nil
}

func (m *benchMOCN) checkDownlink(tla ap.TransportLayerAddress) error {
	want := radioUP
	if m.aggregated {
		want = coreGW
	}

	if got, _ := netip.AddrFromSlice(tla.Bytes()); got != want {
		return fmt.Errorf("downlink tunnel toward %v reached the core, want %v", got, want)
	}

	return nil
}

type node struct {
	conn *sctp.SCTPConn
	ppid uint32
}

type marshaler interface {
	Marshal() ([]byte, error)
}

func (n node) send(msg marshaler, stream uint16) error {
	b, err := msg.Marshal()
	if err != nil {
		return err
	}

	_, err = n.conn.WriteMsg(b, &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(n.ppid), Stream: stream})

	return err
}

type radio struct {
	node
	m     *benchMOCN
	index int
	setUp chan struct{}
	once  sync.Once

	mu    sync.Mutex
	ues   map[uint64]chan []byte
	pages map[uint32]chan int
}

func newRadio(m *benchMOCN, n node, index int) radio {
	return radio{node: n, m: m, index: index, setUp: make(chan struct{}), ues: map[uint64]chan []byte{}, pages: map[uint32]chan int{}}
}

func (r *radio) ready() {
	r.once.Do(func() { close(r.setUp) })
}

func (r *radio) attachUE(id uint64) chan []byte {
	ch := make(chan []byte, 4)

	r.mu.Lock()
	r.ues[id] = ch
	r.mu.Unlock()

	return ch
}

func (r *radio) deliver(id uint64, raw []byte) error {
	r.mu.Lock()
	ch, ok := r.ues[id]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("message for unknown radio UE ID %d", id)
	}

	select {
	case ch <- raw:
		return nil
	default:
		return fmt.Errorf("unanswered messages for radio UE ID %d", id)
	}
}

func (r *radio) awaitPages(token uint32, ch chan int) {
	r.mu.Lock()
	r.pages[token] = ch
	r.mu.Unlock()
}

func (r *radio) paged(token uint32) error {
	r.mu.Lock()
	ch, ok := r.pages[token]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("paging for unknown token %#x", token)
	}

	select {
	case ch <- r.index:
		return nil
	default:
		return fmt.Errorf("paging %#x delivered more than once", token)
	}
}

type benchUE struct {
	op     int
	id     uint32
	ch     chan []byte
	coreID uint64
}

type benchWorker struct {
	m     *benchMOCN
	enb   *enb
	gnb   *gnb
	ue    benchUE
	token uint32
	pages chan int
}

func (m *benchMOCN) worker(tb testing.TB, sc benchScenario, index int) *benchWorker {
	tb.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	for len(m.workers) <= index {
		m.workers = append(m.workers, m.newWorker(tb, sc, len(m.workers)))
	}

	return m.workers[index]
}

func (m *benchMOCN) newWorker(tb testing.TB, sc benchScenario, index int) *benchWorker {
	tb.Helper()

	w := &benchWorker{m: m, ue: benchUE{op: index % len(plmns), id: uint32(index%m.uesPerRadio) + 1}}

	if sc.paging {
		w.token = uint32(index) + 1
		w.pages = make(chan int, *e2eBenchPagingRadios)

		for i := range *e2eBenchPagingRadios {
			m.radioLocked(tb, i).awaitPages(w.token, w.pages)
		}
	} else {
		r := m.radioLocked(tb, index/m.uesPerRadio)
		w.ue.ch = r.attachUE(uint64(w.ue.id))
	}

	switch m.rat {
	case rat4G:
		w.enb = m.enbs[index/m.uesPerRadio%len(m.enbs)]
	case rat5G:
		w.gnb = m.gnbs[index/m.uesPerRadio%len(m.gnbs)]
	}

	if err := sc.cycle(w, samples{}); err != nil {
		tb.Fatalf("warm-up cycle of worker %d: %v", index, err)
	}

	return w
}

func (m *benchMOCN) radioLocked(tb testing.TB, index int) *radio {
	tb.Helper()

	for {
		var r *radio

		switch m.rat {
		case rat4G:
			if index < len(m.enbs) {
				return &m.enbs[index].radio
			}

			e := m.startENB(tb, len(m.enbs))
			m.enbs, r = append(m.enbs, e), &e.radio
		case rat5G:
			if index < len(m.gnbs) {
				return &m.gnbs[index].radio
			}

			g := m.startGNB(tb, len(m.gnbs))
			m.gnbs, r = append(m.gnbs, g), &g.radio
		}

		if _, err := awaitOn(m, r.setUp); err != nil {
			tb.Fatalf("set up radio %d: %v", index, err)
		}

		if err := m.eventually("both operators' core network nodes up", m.coresUp); err != nil {
			tb.Fatal(err)
		}
	}
}

func (m *benchMOCN) radios(tb testing.TB, n int) {
	tb.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.radioLocked(tb, n-1)
}

type samples map[string][]time.Duration

func (s samples) add(procedure string, d time.Duration) {
	s[procedure] = append(s[procedure], d)
}

func (s samples) merge(other samples) {
	for procedure, ds := range other {
		s[procedure] = append(s[procedure], ds...)
	}
}

func (s samples) report(b *testing.B) {
	elapsed := b.Elapsed()

	for procedure, ds := range s {
		if len(ds) == 0 {
			continue
		}

		if elapsed > 0 {
			b.ReportMetric(float64(len(ds))/elapsed.Seconds(), procedure+"/s")
		}

		slices.Sort(ds)

		for _, q := range []struct {
			name string
			at   float64
		}{{"p50", 0.50}, {"p95", 0.95}, {"p99", 0.99}} {
			if float64(len(ds))*(1-q.at) < 1 {
				continue
			}

			rank := int(math.Ceil(q.at * float64(len(ds))))
			b.ReportMetric(float64(ds[rank-1].Nanoseconds()), procedure+"-"+q.name+"-ns")
		}
	}
}

type benchScenario struct {
	rat        rat
	aggregated bool
	paging     bool
	cycle      func(w *benchWorker, s samples) error
}

func runE2E(b *testing.B, sc benchScenario) {
	m := newBenchMOCN(b, sc.rat, sc.aggregated)

	if sc.paging {
		m.radios(b, *e2eBenchPagingRadios)
	}

	b.Run("seq", func(b *testing.B) {
		w := m.worker(b, sc, 0)
		s := samples{}

		b.ReportAllocs()

		for b.Loop() {
			if err := sc.cycle(w, s); err != nil {
				b.Fatal(err)
			}
		}

		s.report(b)
		m.mustVerifyIdle(b)
	})

	for _, p := range []int{1, 8} {
		b.Run(fmt.Sprintf("par/p=%d", p), func(b *testing.B) {
			workers := make([]*benchWorker, p*goruntime.GOMAXPROCS(0))
			for i := range workers {
				workers[i] = m.worker(b, sc, i)
			}

			var (
				mu   sync.Mutex
				all  = samples{}
				next atomic.Int64
			)

			b.ReportAllocs()
			b.SetParallelism(p)
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				w := workers[next.Add(1)-1]
				s := samples{}

				for pb.Next() {
					if err := sc.cycle(w, s); err != nil {
						b.Error(err)
						return
					}
				}

				mu.Lock()
				all.merge(s)
				mu.Unlock()
			})

			b.StopTimer()

			all.report(b)
			m.mustVerifyIdle(b)
		})
	}
}
