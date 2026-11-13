package s1_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
)

type rawPDU struct {
	pdu s1ap.PDU
}

func (r rawPDU) Marshal() ([]byte, error) {
	return s1ap.Marshal(r.pdu)
}

type recordingRelay struct {
	ranLost atomic.Int32
}

func (*recordingRelay) FromRAN(context.Context, *s1.RANAssociation, s1ap.PDU, []byte) {}

func (*recordingRelay) FromMME(context.Context, *s1.MMEAssociation, s1ap.PDU, []byte) {}

func (r *recordingRelay) RANLost(context.Context, *s1.RANAssociation, s1ap.Cause) { r.ranLost.Add(1) }

func (*recordingRelay) MMELost(context.Context, *s1.MMEAssociation) {}

func upAndRunning(t *testing.T, h *harness, mmeListener *fakeMME) (*peer, *peer) {
	t.Helper()

	mme := mmeListener.accept(t)
	enb := h.connectENB(t)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	return enb, mme
}

func TestENBConfigurationUpdateResentWithoutResponse(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.6")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.6"))
	_, mme := upAndRunning(t, h, mmeListener)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 2, "00101")))

	first := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, first.SupportedTAs, s1ap.SupportedTAs{ta(t, 1, "00101"), ta(t, 2, "00101")})

	_ = enb2.conn.Abort()

	again := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	if !reflect.DeepEqual(again, first) {
		t.Fatalf("resent eNB Configuration Update = %+v, want %+v", again, first)
	}

	if st, _ := h.gw.MMEStatus(1, []string{"127.0.0.6"}); st.State != s1.MMEUp {
		t.Fatalf("MME status = %+v, want up", st)
	}

	mme.send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})

	upd := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, upd.SupportedTAs, s1ap.SupportedTAs{ta(t, 1, "00101")})
	mme.send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})

	mme.quiet(t, 2500*time.Millisecond)
}

func TestNonUEProceduresTerminatedLocally(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.7")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.7"))
	enb, mme := upAndRunning(t, h, mmeListener)

	cause := &s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscOMIntervention}

	enb.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{All: true}})

	ack := expect(t, enb, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)
	if len(ack.ConnectionList) != 0 {
		t.Fatalf("Reset Acknowledge list = %+v, want none", ack.ConnectionList)
	}

	part := []s1ap.UEAssociatedLogicalS1ConnectionItem{
		{MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(7)), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(3))},
		{ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(1))},
	}

	enb.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{Part: part}})

	ack = expect(t, enb, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)
	if !reflect.DeepEqual(ack.ConnectionList, part) {
		t.Fatalf("Reset Acknowledge list = %+v, want %+v", ack.ConnectionList, part)
	}

	mme.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{All: true}})
	expect(t, mme, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)

	b, err := (&s1ap.ErrorIndication{Cause: cause}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	ind, err := s1ap.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}

	enb.send(t, &s1ap.ErrorIndication{Cause: cause})
	mme.send(t, &s1ap.ErrorIndication{Cause: cause})
	mme.send(t, rawPDU{&s1ap.InitiatingMessage{
		ProcedureCode: s1ap.ProcOverloadStart, Criticality: s1ap.CriticalityIgnore,
		Value: ind.(*s1ap.InitiatingMessage).Value,
	}})

	enb.quiet(t, 200*time.Millisecond)

	if ind := expect(t, mme, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication); ind.Cause == nil ||
		ind.Cause.Group != s1ap.CauseGroupProtocol || ind.CriticalityDiagnostics == nil {
		t.Fatalf("Error Indication = %+v, want a protocol cause with diagnostics", ind)
	}

	mme.quiet(t, 200*time.Millisecond)

	enb.send(t, rawPDU{&s1ap.InitiatingMessage{
		ProcedureCode: s1ap.ProcReset, Criticality: s1ap.CriticalityReject,
		Value: ind.(*s1ap.InitiatingMessage).Value,
	}})

	errInd := expect(t, enb, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication)
	if errInd.Cause == nil || errInd.Cause.Group != s1ap.CauseGroupProtocol || errInd.CriticalityDiagnostics == nil {
		t.Fatalf("Error Indication = %+v, want a protocol cause with diagnostics", errInd)
	}

	mme.quiet(t, 200*time.Millisecond)
}

func TestRepeatedS1SetupReinitialisesUEContexts(t *testing.T) {
	skipWithoutSCTP(t)

	relay := &recordingRelay{}
	mmeListener := listenMME(t, "127.0.0.8")
	h := startGatewayWith(t, s1.Config{MMEPort: mmeListener.port(), Relay: relay}, operatorA("127.0.0.8"))
	enb, _ := upAndRunning(t, h, mmeListener)

	if n := relay.ranLost.Load(); n != 0 {
		t.Fatalf("RANLost called %d times after the first S1 Setup", n)
	}

	setUpENB(t, enb, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	if n := relay.ranLost.Load(); n != 1 {
		t.Fatalf("RANLost called %d times after a repeated S1 Setup, want 1", n)
	}
}

func TestStartupRetriesOperatorLoad(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.9")
	src := &operators{}
	src.set(operatorA("127.0.0.9"))

	ready := make(chan struct{})
	h := startGatewayWith(t, s1.Config{MMEPort: mmeListener.port(), Operators: func(ctx context.Context) ([]db.Operator, error) {
		select {
		case <-ready:
			return src.list(ctx)
		default:
			return nil, errors.New("database unavailable")
		}
	}})

	enb := h.connectENB(t)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	enb.quiet(t, 300*time.Millisecond)

	close(ready)

	mme := mmeListener.accept(t)
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
}

func TestReconcileIsSerialized(t *testing.T) {
	skipWithoutSCTP(t)

	src := &operators{}
	src.set(operatorA("127.0.0.10"))

	var block atomic.Bool

	entered, release := make(chan struct{}), make(chan struct{})

	h := startGatewayWith(t, s1.Config{MMEPort: 1, Operators: func(ctx context.Context) ([]db.Operator, error) {
		ops, err := src.list(ctx)

		if block.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}

		return ops, err
	}})

	h.eventually(t, "first MME configured", func() bool {
		_, ok := h.gw.MMEStatus(1, []string{"127.0.0.10"})
		return ok
	})

	block.Store(true)

	stale := make(chan error, 1)

	go func() { stale <- h.gw.Reconcile(t.Context()) }()

	<-entered

	src.set(operatorA("127.0.0.11"))

	fresh := make(chan error, 1)

	go func() { fresh <- h.gw.Reconcile(t.Context()) }()

	time.Sleep(100 * time.Millisecond)
	close(release)

	for _, ch := range []chan error{stale, fresh} {
		if err := <-ch; err != nil {
			t.Fatal(err)
		}
	}

	if _, ok := h.gw.MMEStatus(1, []string{"127.0.0.10"}); ok {
		t.Fatal("an older operator snapshot was applied last")
	}

	if _, ok := h.gw.MMEStatus(1, []string{"127.0.0.11"}); !ok {
		t.Fatal("the latest operator snapshot was not applied")
	}
}

func TestNewMMEAssociationWaitsForTheOldOne(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.12")
	h := startGateway(t, mmeListener.port())

	release := h.gw.HoldMMEAssociation("127.0.0.12")

	h.ops.set(operatorA("127.0.0.12"))

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-mmeListener.conns:
		t.Fatal("new MME association started before the old one stopped")
	case <-time.After(300 * time.Millisecond):
	}

	release()

	old := mmeListener.accept(t)

	moved := operatorA("127.0.0.12")
	moved.GlobalENBID.ID = 0x12345
	h.ops.set(moved)

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	old.closed(t)

	mmeListener.accept(t)
}

func TestReconcileAfterStopStartsNoMMEAssociation(t *testing.T) {
	skipWithoutSCTP(t)

	h := startGateway(t, 1)
	h.stop()

	h.ops.set(operatorA("127.0.0.13"))

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, ok := h.gw.MMEStatus(1, []string{"127.0.0.13"}); ok {
		t.Fatal("MME association started after the gateway stopped")
	}
}
