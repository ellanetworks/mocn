package s1_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/sctptest"
)

type (
	frame     = sctptest.Frame[s1ap.PDU]
	peer      = sctptest.Peer[s1ap.PDU]
	fakeMME   = sctptest.Core[s1ap.PDU]
	operators = sctptest.Operators
)

var codec = sctptest.Codec[s1ap.PDU]{Protocol: "S1AP", PPID: s1.PPID, Unmarshal: s1ap.Unmarshal, Name: procedureName}

func expect[T any](t *testing.T, p *peer, kind string, proc s1ap.ProcedureCode,
	parse func([]byte) (*T, error),
) *T {
	t.Helper()

	pdu := p.Next(t)

	var (
		value []byte
		code  s1ap.ProcedureCode
		got   string
	)

	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		value, code, got = m.Value, m.ProcedureCode, "initiating"
	case *s1ap.SuccessfulOutcome:
		value, code, got = m.Value, m.ProcedureCode, "successful"
	case *s1ap.UnsuccessfulOutcome:
		value, code, got = m.Value, m.ProcedureCode, "unsuccessful"
	}

	if got != kind || code != proc {
		t.Fatalf("got %s %s, want %s %s", got, code, kind, proc)
	}

	msg, err := parse(value)
	if err != nil {
		t.Fatalf("parse %s: %v", proc, err)
	}

	return msg
}

func skipWithoutSCTP(t *testing.T) {
	t.Helper()

	sctptest.SkipWithoutSCTP(t)
}

func listenMME(t *testing.T, ip string) *fakeMME {
	t.Helper()

	return sctptest.ListenCore(t, codec, ip, 0)
}

func listenMMEAt(t *testing.T, ip string, port int) *fakeMME {
	t.Helper()

	return sctptest.ListenCore(t, codec, ip, port)
}

type harness struct {
	*sctptest.Gateway
	gw      *s1.Gateway
	metrics *metrics.Metrics
}

func startGateway(t *testing.T, mmePort int, ops ...db.Operator) *harness {
	t.Helper()

	return startGatewayWith(t, s1.Config{CorePort: mmePort}, ops...)
}

func startGatewayWith(t *testing.T, cfg s1.Config, ops ...db.Operator) *harness {
	t.Helper()

	ln := sctptest.Listen(t)

	src := &operators{}
	src.Set(ops...)

	cfg.Listener, cfg.SetupWait, cfg.RetryInterval = ln, time.Second, 500*time.Millisecond

	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 2 * time.Second
	}

	if cfg.Metrics == nil {
		cfg.Metrics = metrics.New()
	}

	cfg.ReconnectMin, cfg.ReconnectMax = 100*time.Millisecond, 200*time.Millisecond

	if cfg.Logger == nil {
		cfg.Logger = sctptest.Logger()
	}

	if cfg.Operators == nil {
		cfg.Operators = src.List
	}

	gw := s1.New(cfg)

	return &harness{Gateway: sctptest.Start(t, ln, src, gw.Run), gw: gw, metrics: cfg.Metrics}
}

func (h *harness) connectENB(t *testing.T) *peer {
	t.Helper()

	return sctptest.Dial(t, codec, h.Addr)
}

func (h *harness) connectENBFrom(t *testing.T, ip string) *peer {
	t.Helper()

	return sctptest.DialFrom(t, codec, ip, h.Addr)
}

func (h *harness) eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	sctptest.Eventually(t, what, ok)
}

func plmnID(t *testing.T, mcc, mnc string) s1ap.PLMNIdentity {
	t.Helper()

	return sctptest.PLMNID[s1ap.PLMNIdentity](t, mcc, mnc)
}

func procedureName(pdu s1ap.PDU) string {
	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		return "initiating " + m.ProcedureCode.String()
	case *s1ap.SuccessfulOutcome:
		return "successful " + m.ProcedureCode.String()
	case *s1ap.UnsuccessfulOutcome:
		return "unsuccessful " + m.ProcedureCode.String()
	}

	return "unknown"
}
