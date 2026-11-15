package assoc

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/sctptest"
)

const testPPID = 18

type (
	testSetup struct {
		ref   string
		plmns []db.PLMN
	}
	testRadio  struct{}
	testCore   struct{}
	testInfo   struct{}
	testClient = Client[testInfo, testCore]
)

func (s testSetup) Ref() string { return s.ref }

func (s testSetup) Broadcasts(plmns []db.PLMN) bool {
	return slices.ContainsFunc(s.plmns, func(p db.PLMN) bool { return slices.Contains(plmns, p) })
}

func (s testSetup) Serves(tai TAI) bool { return slices.Contains(s.plmns, tai.PLMN) }

func (testRadio) Equal(testRadio) bool { return true }

func (testRadio) Empty() bool { return true }

func (r testRadio) Pushed() testRadio { return r }

func (testCore) Equal(testCore) bool { return true }

type testProtocol struct{}

func (testProtocol) Nodes(o db.Operator) []Node[testCore] {
	if o.GlobalENBID == nil {
		return nil
	}

	id := *o.GlobalENBID
	nodes := make([]Node[testCore], 0, len(o.MMEs))

	for _, m := range o.MMEs {
		nodes = append(nodes, Node[testCore]{
			Key:          fmt.Sprintf("%d|%s|%s|%d|%s", o.ID, id.PLMN, id.Type, id.ID, strings.Join(m.TNLAddresses, ",")),
			Addrs:        m.TNLAddresses,
			SetupRequest: func(testCore) ([]byte, error) { return nil, nil },
		})
	}

	return nodes
}

func (testProtocol) CoreConfig(_ []db.PLMN, setups []testSetup) (testCore, bool) {
	return testCore{}, len(setups) > 0
}

func (testProtocol) ConfigurationUpdate(testCore, testCore) ([]byte, error) { return nil, nil }

func (testProtocol) ConfigAttrs(testCore) []slog.Attr { return nil }

func (testProtocol) HandleCore(context.Context, *Session[testSetup, testRadio, testInfo, testCore], []byte) error {
	return nil
}

func (testProtocol) CoreLostLocked(*testClient, *Core) func(context.Context) {
	return func(context.Context) {}
}

func (testProtocol) RadioUpLocked(context.Context, *Radio[testSetup, testRadio]) {}

func (testProtocol) RadioConfigLocked() testRadio { return testRadio{} }

func (testProtocol) RadioConfigUpdate(context.Context, testRadio) []byte { return nil }

func (testProtocol) RadioNotifyLocked(_ context.Context, _ *Radio[testSetup, testRadio], acked, _ testRadio) testRadio {
	return acked
}

func (testProtocol) HandleRadio(context.Context, *Radio[testSetup, testRadio], []byte) {}

func (testProtocol) RadioLost(context.Context, *Radio[testSetup, testRadio]) {}

func (testProtocol) Overloaded(testInfo) bool { return false }

func (testProtocol) UEsByOperator() map[int64]int { return nil }

var codec = sctptest.Codec[[]byte]{
	Protocol: "S1AP", PPID: testPPID,
	Unmarshal: func(b []byte) ([]byte, error) { return b, nil },
	Name:      func([]byte) string { return "message" },
}

type harness struct {
	*sctptest.Gateway
	gw *Gateway[testSetup, testRadio, testInfo, testCore]
}

func start(t *testing.T, port int, operators func(ctx context.Context) ([]db.Operator, error),
	plane *gtpu.Plane,
) *harness {
	t.Helper()

	src := &sctptest.Operators{}
	if operators == nil {
		operators = src.List
	}

	ln := sctptest.Listen(t)
	gw := New(Config{
		Listener: ln, Operators: operators, Logger: sctptest.Logger(), CorePort: port,
		ReconnectMin: 100 * time.Millisecond, ReconnectMax: 200 * time.Millisecond, Plane: plane,
	}, Names{Core: "MME", CoreAttr: "mme", PPID: testPPID, Protocol: "s1ap", CoreStreams: 2}, nil,
		Protocol[testSetup, testRadio, testInfo, testCore](testProtocol{}))

	return &harness{Gateway: sctptest.Start(t, ln, src, gw.Run), gw: gw}
}

func (h *harness) configured(addrs ...string) (ok bool) {
	h.gw.View(func() { _, ok = h.gw.ClientLocked(1, addrs) })

	return ok
}

func operatorA(mmes ...string) db.Operator {
	plmn := db.PLMN{MCC: "001", MNC: "01"}
	o := db.Operator{
		ID: 1, Name: "A", PLMNs: []db.PLMN{plmn},
		GlobalENBID: &db.GlobalENBID{PLMN: plmn, Type: db.ENBIDMacro, ID: 0xabcde},
	}

	for _, m := range mmes {
		o.MMEs = append(o.MMEs, db.CoreNode{TNLAddresses: []string{m}})
	}

	return o
}

func TestReconcileIsSerialized(t *testing.T) {
	sctptest.SkipWithoutSCTP(t)

	src := &sctptest.Operators{}
	src.Set(operatorA("127.0.0.10"))

	var block atomic.Bool

	entered, release := make(chan struct{}), make(chan struct{})

	h := start(t, 1, func(ctx context.Context) ([]db.Operator, error) {
		ops, err := src.List(ctx)

		if block.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}

		return ops, err
	}, nil)

	sctptest.Eventually(t, "first MME configured", func() bool { return h.configured("127.0.0.10") })

	block.Store(true)

	stale := make(chan error, 1)

	go func() { stale <- h.gw.Reconcile(t.Context()) }()

	<-entered

	src.Set(operatorA("127.0.0.11"))

	fresh := make(chan error, 1)

	go func() { fresh <- h.gw.Reconcile(t.Context()) }()

	sctptest.Eventually(t, "the newer reconcile to finish or wait for the older one", func() bool {
		return len(fresh) > 0 || parked("sync.Mutex.Lock", "assoc.(*Gateway[...]).Reconcile(")
	})
	close(release)

	for _, ch := range []chan error{stale, fresh} {
		if err := <-ch; err != nil {
			t.Fatal(err)
		}
	}

	if h.configured("127.0.0.10") {
		t.Fatal("an older operator snapshot was applied last")
	}

	if !h.configured("127.0.0.11") {
		t.Fatal("the latest operator snapshot was not applied")
	}
}

func parked(reason, function string) bool {
	buf := make([]byte, 1<<20)

	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}

		buf = make([]byte, 2*len(buf))
	}

	for g := range strings.SplitSeq(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		if strings.Contains(header, "["+reason) && strings.Contains(g, function) {
			return true
		}
	}

	return false
}

func (h *harness) hold(addrs ...string) (release func()) {
	c := &testClient{Peer: &Peer{addrs: addrs}, done: make(chan struct{})}

	h.gw.mu.Lock()
	h.gw.stopping[c] = struct{}{}
	h.gw.mu.Unlock()

	return func() {
		h.gw.mu.Lock()
		delete(h.gw.stopping, c)
		h.gw.mu.Unlock()
		close(c.done)
	}
}

func TestNewAssociationWaitsForTheOldOne(t *testing.T) {
	t.Parallel()

	sctptest.SkipWithoutSCTP(t)

	mme := sctptest.ListenCore(t, codec, "127.0.0.12", 0)
	h := start(t, mme.Port(), nil, nil)

	release := h.hold("127.0.0.12")

	h.Ops.Set(operatorA("127.0.0.12"))

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-mme.Conns:
		t.Fatal("new MME association started before the old one stopped")
	case <-time.After(300 * time.Millisecond):
	}

	release()

	old := mme.Accept(t)

	moved := operatorA("127.0.0.12")
	moved.GlobalENBID.ID = 0x12345
	h.Ops.Set(moved)

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	old.Closed(t)

	mme.Accept(t)
}

func TestReconcileAfterStopStartsNoAssociation(t *testing.T) {
	t.Parallel()

	sctptest.SkipWithoutSCTP(t)

	h := start(t, 1, nil, nil)
	h.Stop()

	h.Ops.Set(operatorA("127.0.0.13"))

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	if h.configured("127.0.0.13") {
		t.Fatal("MME association started after the gateway stopped")
	}
}

func TestAssociationBindsTheRouteSourceAddressAndAnchorsTheUserPlane(t *testing.T) {
	t.Parallel()

	sctptest.SkipWithoutSCTP(t)

	mme := sctptest.ListenCore(t, codec, "127.0.0.13", 0)
	op := operatorA("127.0.0.13")

	plane := gtpu.New(gtpu.Config{})
	if err := plane.SetOperators([]gtpu.Operator{{ID: op.ID, Name: op.Name, Aggregated: true}}); err != nil {
		t.Fatal(err)
	}

	h := start(t, mme.Port(), nil, plane)
	h.Ops.Set(op)

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := addresses(mme.Accept(t).Conn.RemoteAddr()); !slices.Equal(got, []string{"127.0.0.1"}) {
		t.Fatalf("association from %v, want only the source address of the route to the MME", got)
	}

	for range 200 {
		st, _ := plane.Status(op.ID)
		if st.State == gtpu.StateReady {
			if st.Interface != "lo" || !slices.Equal(st.Addresses, []netip.Addr{netip.MustParseAddr("127.0.0.1")}) {
				t.Fatalf("user plane = %+v, want lo and 127.0.0.1", st)
			}

			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("user plane not anchored to the association")
}
