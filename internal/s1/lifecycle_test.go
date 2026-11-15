package s1_test

import (
	"context"
	"errors"
	"reflect"
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

func upAndRunning(t *testing.T, h *harness, mmeListener *fakeMME) (*peer, *peer) {
	t.Helper()

	mme := mmeListener.Accept(t)
	enb := h.connectENB(t)
	enb.Send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	return enb, mme
}

func TestENBConfigurationUpdateResentWithoutResponse(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.6")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.6"))
	_, mme := upAndRunning(t, h, mmeListener)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 2, "00101")))

	first := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, first.SupportedTAs, s1ap.SupportedTAs{ta(t, 1, "00101"), ta(t, 2, "00101")})

	_ = enb2.Conn.Abort()

	again := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	if !reflect.DeepEqual(again, first) {
		t.Fatalf("resent eNB Configuration Update = %+v, want %+v", again, first)
	}

	if st, _ := h.gw.MMEStatus(1, []string{"127.0.0.6"}); st.State != s1.MMEUp {
		t.Fatalf("MME status = %+v, want up", st)
	}

	mme.Send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})

	upd := expect(t, mme, "initiating", s1ap.ProcENBConfigurationUpdate, s1ap.ParseENBConfigurationUpdate)
	assertTAs(t, upd.SupportedTAs, s1ap.SupportedTAs{ta(t, 1, "00101")})
	mme.Send(t, &s1ap.ENBConfigurationUpdateAcknowledge{})

	mme.Quiet(t, 2500*time.Millisecond)
}

func TestNonUEProceduresTerminatedLocally(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.7")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.0.7"))
	enb, mme := upAndRunning(t, h, mmeListener)

	cause := s1ap.Ptr(s1ap.CauseMiscOMIntervention.Cause())

	enb.Send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{S1Interface: s1ap.Ptr(s1ap.ResetAllResetAll)}})

	ack := expect(t, enb, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)
	if len(ack.ConnectionList) != 0 {
		t.Fatalf("Reset Acknowledge list = %+v, want none", ack.ConnectionList)
	}

	part := []s1ap.UEAssociatedLogicalS1ConnectionItem{
		{MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(7)), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(3))},
		{ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(1))},
	}

	enb.Send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{PartOfS1Interface: s1ap.Ptr(s1ap.UEAssociatedLogicalS1ConnectionListRes(part))}})

	ack = expect(t, enb, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)
	if !reflect.DeepEqual(ack.ConnectionList, part) {
		t.Fatalf("Reset Acknowledge list = %+v, want %+v", ack.ConnectionList, part)
	}

	mme.Send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{S1Interface: s1ap.Ptr(s1ap.ResetAllResetAll)}})
	expect(t, mme, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)

	b, err := (&s1ap.ErrorIndication{Cause: cause}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	ind, err := s1ap.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}

	enb.Send(t, &s1ap.ErrorIndication{Cause: cause})
	mme.Send(t, &s1ap.ErrorIndication{Cause: cause})
	mme.Send(t, rawPDU{&s1ap.InitiatingMessage{
		ProcedureCode: s1ap.ProcOverloadStart, Criticality: s1ap.CriticalityIgnore,
		Value: ind.(*s1ap.InitiatingMessage).Value,
	}})

	enb.Quiet(t, 200*time.Millisecond)

	if ind := expect(t, mme, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication); ind.Cause == nil ||
		ind.Cause.Protocol == nil || ind.CriticalityDiagnostics == nil {
		t.Fatalf("Error Indication = %+v, want a protocol cause with diagnostics", ind)
	}

	mme.Quiet(t, 200*time.Millisecond)

	enb.Send(t, rawPDU{&s1ap.InitiatingMessage{
		ProcedureCode: s1ap.ProcReset, Criticality: s1ap.CriticalityReject,
		Value: ind.(*s1ap.InitiatingMessage).Value,
	}})

	errInd := expect(t, enb, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication)
	if errInd.Cause == nil || errInd.Cause.Protocol == nil || errInd.CriticalityDiagnostics == nil {
		t.Fatalf("Error Indication = %+v, want a protocol cause with diagnostics", errInd)
	}

	mme.Quiet(t, 200*time.Millisecond)
}

func TestStartupRetriesOperatorLoad(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.9")
	src := &operators{}
	src.Set(operatorA("127.0.0.9"))

	ready := make(chan struct{})
	h := startGatewayWith(t, s1.Config{CorePort: mmeListener.Port(), Operators: func(ctx context.Context) ([]db.Operator, error) {
		select {
		case <-ready:
			return src.List(ctx)
		default:
			return nil, errors.New("database unavailable")
		}
	}})

	enb := h.connectENB(t)
	enb.Send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	enb.Quiet(t, 300*time.Millisecond)

	close(ready)

	mme := mmeListener.Accept(t)
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
}
