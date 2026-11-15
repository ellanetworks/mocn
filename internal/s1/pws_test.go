package s1_test

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func pws(o db.Operator) db.Operator {
	o.PWSOperator = true
	return o
}

func ecgi(t *testing.T, plmn string, enbID, cell uint32) s1ap.EUTRANCGI {
	t.Helper()

	return s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, plmn[:3], plmn[3:]), CellID: s1ap.CellIdentity(enbID<<8 | cell)}
}

func writeReplaceWarning(id, serial uint16, area *s1ap.WarningAreaList) *s1ap.WriteReplaceWarningRequest {
	return &s1ap.WriteReplaceWarningRequest{
		MessageIdentifier: s1ap.MessageIdentifier(id), SerialNumber: s1ap.SerialNumber(serial), WarningAreaList: area,
		RepetitionPeriod: 10, NumberofBroadcastRequest: 1,
	}
}

func marshal(t *testing.T, msg interface{ Marshal() ([]byte, error) }) []byte {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func expectWarningResponse(t *testing.T, mme *peer, id, serial uint16) *s1ap.BroadcastCompletedAreaList {
	t.Helper()

	resp := expect(t, mme, "successful", s1ap.ProcWriteReplaceWarning, s1ap.ParseWriteReplaceWarningResponse)
	if resp.MessageIdentifier != s1ap.MessageIdentifier(id) || resp.SerialNumber != s1ap.SerialNumber(serial) {
		t.Fatalf("Write-Replace Warning Response for %d/%d, want %d/%d", resp.MessageIdentifier, resp.SerialNumber, id, serial)
	}

	return resp.BroadcastCompletedAreaList
}

func expectKillResponse(t *testing.T, mme *peer, id, serial uint16) *s1ap.BroadcastCancelledAreaList {
	t.Helper()

	resp := expect(t, mme, "successful", s1ap.ProcKill, s1ap.ParseKillResponse)
	if resp.MessageIdentifier != s1ap.MessageIdentifier(id) || resp.SerialNumber != s1ap.SerialNumber(serial) {
		t.Fatalf("Kill Response for %d/%d, want %d/%d", resp.MessageIdentifier, resp.SerialNumber, id, serial)
	}

	return resp.BroadcastCancelledAreaList
}

func TestWriteReplaceWarningFromThePWSOperator(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoOperatorsWith(t, s1.Config{}, pws(operatorA("127.0.2.1")), operatorB("127.0.2.2"))
	radios := []*peer{x.enbA, x.enbB, x.enbAB}

	x.mmeB.Send(t, writeReplaceWarning(4370, 1, nil))

	if l := expectWarningResponse(t, x.mmeB, 4370, 1); l != nil {
		t.Fatalf("Broadcast Completed Area List = %+v, want none", l)
	}

	everywhere := writeReplaceWarning(4370, 2, nil)
	x.mmeA.Send(t, everywhere)

	for _, enb := range radios {
		expectSame(t, enb, marshal(t, everywhere))
	}

	x.enbA.Send(t, &s1ap.WriteReplaceWarningResponse{
		MessageIdentifier: 4370, SerialNumber: 2,
		BroadcastCompletedAreaList: &s1ap.BroadcastCompletedAreaList{
			CellIDBroadcast: &s1ap.CellIDBroadcast{{ECGI: ecgi(t, "00101", 1, 1)}},
		},
	})
	x.mmeA.Quiet(t, 100*time.Millisecond)
	x.enbB.Send(t, &s1ap.WriteReplaceWarningResponse{
		MessageIdentifier: 4370, SerialNumber: 2,
		BroadcastCompletedAreaList: &s1ap.BroadcastCompletedAreaList{
			TAIBroadcast: &s1ap.TAIBroadcast{{
				TAI: tai(t, "002002", 2), CompletedCellInTAI: []s1ap.CompletedCellInTAIItem{{ECGI: ecgi(t, "002002", 2, 1)}},
			}},
		},
	})
	x.mmeA.Quiet(t, 200*time.Millisecond)
	x.enbAB.Send(t, &s1ap.WriteReplaceWarningResponse{MessageIdentifier: 4370, SerialNumber: 2})

	got := expectWarningResponse(t, x.mmeA, 4370, 2)
	want := &s1ap.BroadcastCompletedAreaList{CellIDBroadcast: &s1ap.CellIDBroadcast{
		{ECGI: ecgi(t, "00101", 1, 1)}, {ECGI: ecgi(t, "002002", 2, 1)},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Broadcast Completed Area List = %+v, want %+v", got, want)
	}

	byTAI := writeReplaceWarning(4371, 1, &s1ap.WarningAreaList{TrackingAreaListforWarning: &s1ap.TAIListforWarning{tai(t, "002002", 1)}})
	x.mmeA.Send(t, byTAI)
	expectSame(t, x.enbAB, marshal(t, byTAI))

	completed := &s1ap.BroadcastCompletedAreaList{TAIBroadcast: &s1ap.TAIBroadcast{{
		TAI: tai(t, "002002", 1), CompletedCellInTAI: []s1ap.CompletedCellInTAIItem{{ECGI: ecgi(t, "00101", 3, 1)}},
	}}}
	x.enbAB.Send(t, &s1ap.WriteReplaceWarningResponse{
		MessageIdentifier: 4371, SerialNumber: 1, BroadcastCompletedAreaList: completed,
	})

	if got := expectWarningResponse(t, x.mmeA, 4371, 1); !reflect.DeepEqual(got, completed) {
		t.Fatalf("Broadcast Completed Area List = %+v, want %+v", got, completed)
	}

	byCell := writeReplaceWarning(4372, 1, &s1ap.WarningAreaList{CellIDList: &s1ap.ECGIList{ecgi(t, "00101", 1, 5)}})
	x.mmeA.Send(t, byCell)
	x.mmeA.Send(t, byCell)
	expectSame(t, x.enbA, marshal(t, byCell))
	expectSame(t, x.enbA, marshal(t, byCell))

	cell := &s1ap.BroadcastCompletedAreaList{CellIDBroadcast: &s1ap.CellIDBroadcast{{ECGI: ecgi(t, "00101", 1, 5)}}}
	x.enbA.Send(t, &s1ap.WriteReplaceWarningResponse{MessageIdentifier: 4372, SerialNumber: 1, BroadcastCompletedAreaList: cell})

	if got := expectWarningResponse(t, x.mmeA, 4372, 1); !reflect.DeepEqual(got, cell) {
		t.Fatalf("Broadcast Completed Area List = %+v, want %+v", got, cell)
	}

	x.enbA.Send(t, &s1ap.WriteReplaceWarningResponse{MessageIdentifier: 4372, SerialNumber: 1})

	if got := expectWarningResponse(t, x.mmeA, 4372, 1); got != nil {
		t.Fatalf("Broadcast Completed Area List = %+v, want none", got)
	}

	x.mmeA.Send(t, writeReplaceWarning(4373, 1, &s1ap.WarningAreaList{CellIDList: &s1ap.ECGIList{ecgi(t, "00101", 9, 1)}}))

	if got := expectWarningResponse(t, x.mmeA, 4373, 1); got != nil {
		t.Fatalf("Broadcast Completed Area List = %+v, want none", got)
	}

	for _, p := range append(radios, x.mmeA, x.mmeB) {
		p.Quiet(t, 200*time.Millisecond)
	}

	for result, want := range map[string]float64{"relayed": 4, "not_pws_operator": 1, "no_radio": 1, "invalid": 0} {
		if got := testutil.ToFloat64(x.h.metrics.PWS.WithLabelValues("s1", "write_replace_warning", result)); got != want {
			t.Fatalf("write_replace_warning %s = %v, want %v", result, got, want)
		}
	}
}

func TestKillAnsweredWhenTheRadiosLeave(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoOperatorsWith(t, s1.Config{WarningWait: time.Minute}, pws(operatorA("127.0.2.3")), operatorB("127.0.2.4"))

	x.mmeB.Send(t, &s1ap.KillRequest{MessageIdentifier: 4370, SerialNumber: 1})

	if l := expectKillResponse(t, x.mmeB, 4370, 1); l != nil {
		t.Fatalf("Broadcast Cancelled Area List = %+v, want none", l)
	}

	kill := &s1ap.KillRequest{
		MessageIdentifier: 4370, SerialNumber: 2, KillAllWarningMessages: s1ap.Ptr(s1ap.KillAllWarningMessages(0)),
	}
	x.mmeA.Send(t, kill)

	for _, enb := range []*peer{x.enbA, x.enbB, x.enbAB} {
		expectSame(t, enb, marshal(t, kill))
	}

	cancelled := &s1ap.BroadcastCancelledAreaList{TAICancelled: &s1ap.TAICancelled{{
		TAI:                tai(t, "00101", 1),
		CancelledCellInTAI: []s1ap.CancelledCellInTAIItem{{ECGI: ecgi(t, "00101", 1, 1), NumberOfBroadcasts: 0}},
	}}}
	x.enbA.Send(t, &s1ap.KillResponse{MessageIdentifier: 4370, SerialNumber: 2, BroadcastCancelledAreaList: cancelled})
	x.mmeA.Quiet(t, 100*time.Millisecond)
	x.enbAB.Send(t, &s1ap.KillResponse{
		MessageIdentifier: 4370, SerialNumber: 2,
		BroadcastCancelledAreaList: &s1ap.BroadcastCancelledAreaList{TAICancelled: &s1ap.TAICancelled{{
			TAI:                tai(t, "00101", 1),
			CancelledCellInTAI: []s1ap.CancelledCellInTAIItem{{ECGI: ecgi(t, "00101", 3, 2), NumberOfBroadcasts: 0}},
		}}},
	})
	x.mmeA.Quiet(t, 200*time.Millisecond)

	if err := x.enbB.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	want := &s1ap.BroadcastCancelledAreaList{TAICancelled: &s1ap.TAICancelled{{
		TAI: tai(t, "00101", 1),
		CancelledCellInTAI: []s1ap.CancelledCellInTAIItem{
			{ECGI: ecgi(t, "00101", 1, 1), NumberOfBroadcasts: 0}, {ECGI: ecgi(t, "00101", 3, 2), NumberOfBroadcasts: 0},
		},
	}}}

	if got := expectKillResponse(t, x.mmeA, 4370, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("Broadcast Cancelled Area List = %+v, want %+v", got, want)
	}
}

func TestKillAnsweredWithoutTheSilentRadios(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoOperatorsWith(t, s1.Config{WarningWait: 500 * time.Millisecond},
		pws(operatorA("127.0.2.5")), operatorB("127.0.2.6"))

	kill := &s1ap.KillRequest{
		MessageIdentifier: 4370, SerialNumber: 3,
		WarningAreaList: &s1ap.WarningAreaList{EmergencyAreaIDList: &s1ap.EmergencyAreaIDList{{0, 0, 1}}},
	}
	x.mmeA.Send(t, kill)

	for _, enb := range []*peer{x.enbA, x.enbB, x.enbAB} {
		expectSame(t, enb, marshal(t, kill))
	}

	cancelled := &s1ap.BroadcastCancelledAreaList{EmergencyAreaIDCancelled: &s1ap.EmergencyAreaIDCancelled{{
		EmergencyAreaID:    s1ap.EmergencyAreaID{0, 0, 1},
		CancelledCellInEAI: []s1ap.CancelledCellInEAIItem{{ECGI: ecgi(t, "002002", 2, 1), NumberOfBroadcasts: 4}},
	}}}
	x.enbB.Send(t, &s1ap.KillResponse{MessageIdentifier: 4370, SerialNumber: 3, BroadcastCancelledAreaList: cancelled})

	if got := expectKillResponse(t, x.mmeA, 4370, 3); !reflect.DeepEqual(got, cancelled) {
		t.Fatalf("Broadcast Cancelled Area List = %+v, want %+v", got, cancelled)
	}

	x.enbA.Send(t, &s1ap.KillResponse{MessageIdentifier: 4370, SerialNumber: 3})
	x.mmeA.Quiet(t, 300*time.Millisecond)

	h := x.h
	h.eventually(t, "silent radios counted", func() bool {
		return testutil.ToFloat64(h.metrics.PWS.WithLabelValues("s1", "kill", "timeout")) == 2
	})

	for result, want := range map[string]float64{"relayed": 1, "not_pws_operator": 0, "no_radio": 0} {
		if got := testutil.ToFloat64(x.h.metrics.PWS.WithLabelValues("s1", "kill", result)); got != want {
			t.Fatalf("kill %s = %v, want %v", result, got, want)
		}
	}
}

func gatewayENBID(t *testing.T) s1ap.GlobalENBID {
	t.Helper()

	return s1ap.GlobalENBID{PLMNIdentity: plmnID(t, "001", "01"), ENBID: s1ap.ENBID{MacroENBID: s1ap.Ptr(s1ap.MacroENBID(0xabcde))}}
}

func radioENBID(t *testing.T, id uint32) s1ap.GlobalENBID {
	t.Helper()

	return s1ap.GlobalENBID{PLMNIdentity: plmnID(t, "001", "01"), ENBID: s1ap.ENBID{MacroENBID: s1ap.Ptr(s1ap.MacroENBID(id))}}
}

func restartIndication(t *testing.T, id s1ap.GlobalENBID, enbID uint32, cells ...uint32) *s1ap.PWSRestartIndication {
	t.Helper()

	ecgis := make([]s1ap.EUTRANCGI, 0, len(cells))
	for _, c := range cells {
		ecgis = append(ecgis, ecgi(t, "00101", enbID, c))
	}

	return &s1ap.PWSRestartIndication{
		ECGIListForRestart:            ecgis,
		GlobalENBID:                   id,
		TAIListForRestart:             []s1ap.TAI{tai(t, "00101", 1), tai(t, "002002", 1)},
		EmergencyAreaIDListForRestart: []s1ap.EmergencyAreaID{{0, 0, 1}},
	}
}

func expectSameOnStream0(t *testing.T, p *peer, msg interface{ Marshal() ([]byte, error) }) {
	t.Helper()

	f := p.NextFrame(t)
	if want := marshal(t, msg); !bytes.Equal(f.Raw, want) || f.Stream != 0 {
		t.Fatalf("got %s %x on stream %d, want %x on stream 0", procedureName(f.PDU), f.Raw, f.Stream, want)
	}
}

func TestPWSIndicationsRelayedToThePWSOperator(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	x := startTwoOperatorsWith(t, s1.Config{}, pws(operatorA("127.0.2.7")), operatorB("127.0.2.8"))

	x.enbAB.Send(t, restartIndication(t, radioENBID(t, 3), 3, 1))
	expectSameOnStream0(t, x.mmeA, restartIndication(t, gatewayENBID(t), 3, 1))

	x.enbA.Send(t, &s1ap.PWSFailureIndication{
		PWSFailedECGIList: []s1ap.EUTRANCGI{ecgi(t, "00101", 1, 1)}, GlobalENBID: radioENBID(t, 1),
	})
	expectSameOnStream0(t, x.mmeA, &s1ap.PWSFailureIndication{
		PWSFailedECGIList: []s1ap.EUTRANCGI{ecgi(t, "00101", 1, 1)}, GlobalENBID: gatewayENBID(t),
	})

	x.enbB.Send(t, initiating(s1ap.ProcPWSRestartIndication, s1ap.CriticalityReject, bodyOf(t, &s1ap.PWSFailureIndication{
		PWSFailedECGIList: []s1ap.EUTRANCGI{ecgi(t, "002002", 2, 1)}, GlobalENBID: radioENBID(t, 2),
	})))

	ind := expect(t, x.enbB, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication)
	if ind.Cause == nil || ind.Cause.Protocol == nil || ind.CriticalityDiagnostics == nil ||
		ind.CriticalityDiagnostics.ProcedureCode == nil || *ind.CriticalityDiagnostics.ProcedureCode != s1ap.ProcPWSRestartIndication {
		t.Fatalf("Error Indication = %+v", ind)
	}

	for _, p := range []*peer{x.mmeA, x.mmeB} {
		p.Quiet(t, 200*time.Millisecond)
	}

	for key, want := range map[[2]string]float64{
		{"pws_restart_indication", "relayed"}: 1, {"pws_restart_indication", "invalid"}: 1,
		{"pws_failure_indication", "relayed"}: 1, {"pws_restart_indication", "no_mme"}: 0,
	} {
		if got := testutil.ToFloat64(x.h.metrics.PWS.WithLabelValues("s1", key[0], key[1])); got != want {
			t.Fatalf("%s %s = %v, want %v", key[0], key[1], got, want)
		}
	}
}

func TestPWSRestartIndicationKeptUntilAnMMEIsUp(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.2.9")
	h := startGateway(t, mmeListener.Port(), pws(operatorA("127.0.2.9")))
	enb, mme := upAndRunning(t, h, mmeListener)

	enb2 := h.connectENB(t)
	setUpENB(t, enb2, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	if err := mme.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	h.eventually(t, "MME not up", func() bool {
		st, _ := h.gw.MMEStatus(1, []string{"127.0.2.9"})
		return st.State != s1.MMEUp
	})

	enb.Send(t, restartIndication(t, radioENBID(t, 1), 1, 1))
	enb.Send(t, restartIndication(t, radioENBID(t, 1), 1, 2))
	enb.Send(t, restartIndication(t, radioENBID(t, 1), 1, 2, 1))
	enb.Send(t, &s1ap.PWSFailureIndication{
		PWSFailedECGIList: []s1ap.EUTRANCGI{ecgi(t, "00101", 1, 3)}, GlobalENBID: radioENBID(t, 1),
	})
	enb2.Send(t, restartIndication(t, radioENBID(t, 2), 2, 1))

	h.eventually(t, "PWS indications counted", func() bool {
		return testutil.ToFloat64(h.metrics.PWS.WithLabelValues("s1", "pws_restart_indication", "no_mme")) == 4 &&
			testutil.ToFloat64(h.metrics.PWS.WithLabelValues("s1", "pws_failure_indication", "no_mme")) == 1
	})

	if err := enb2.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	h.eventually(t, "second radio gone", func() bool { return len(h.gw.RANNodes()) == 1 })

	mme = mmeListener.Accept(t)
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})

	want := map[string]bool{
		string(marshal(t, restartIndication(t, gatewayENBID(t), 1, 2, 1))): true,
		string(marshal(t, restartIndication(t, gatewayENBID(t), 2, 1))):    true,
	}

	for range 2 {
		f := mme.NextFrame(t)
		if !want[string(f.Raw)] || f.Stream != 0 {
			t.Fatalf("got %s %x on stream %d, want one kept PWS Restart Indication", procedureName(f.PDU), f.Raw, f.Stream)
		}

		delete(want, string(f.Raw))
	}

	mme.Quiet(t, 300*time.Millisecond)

	if err := mme.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	mme = mmeListener.Accept(t)
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})
	mme.Quiet(t, 300*time.Millisecond)
}

func TestPWSIndicationsWithoutAPWSOperator(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.2.10")
	h := startGateway(t, mmeListener.Port(), operatorA("127.0.2.10"))
	enb, mme := upAndRunning(t, h, mmeListener)

	enb.Send(t, restartIndication(t, radioENBID(t, 1), 1, 1))
	enb.Send(t, &s1ap.PWSFailureIndication{
		PWSFailedECGIList: []s1ap.EUTRANCGI{ecgi(t, "00101", 1, 1)}, GlobalENBID: radioENBID(t, 1),
	})
	mme.Quiet(t, 300*time.Millisecond)

	for _, proc := range []string{"pws_restart_indication", "pws_failure_indication"} {
		if got := testutil.ToFloat64(h.metrics.PWS.WithLabelValues("s1", proc, "no_pws_operator")); got != 1 {
			t.Fatalf("%s no_pws_operator = %v, want 1", proc, got)
		}
	}

	h.Ops.Set(pws(operatorA("127.0.2.10")))

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	mme.Quiet(t, 300*time.Millisecond)
}

func TestOwnsCell(t *testing.T) {
	t.Parallel()

	a, b := plmnA, plmnB

	for _, tc := range []struct {
		id    db.GlobalENBID
		plmn  string
		cell  uint32
		owned bool
	}{
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDMacro, ID: 0xabcde}, "00101", 0xabcde<<8 | 0xff, true},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDMacro, ID: 0xabcde}, "00101", 0xabcdf<<8 | 0x01, false},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDHome, ID: 0xabcdef1}, "00101", 0xabcdef1, true},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDHome, ID: 0xabcdef1}, "00101", 0xabcdef2, false},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDShortMacro, ID: 0x3ffff}, "00101", 0x3ffff<<10 | 0x3ff, true},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDShortMacro, ID: 0x3ffff}, "00101", 0x3fffe<<10 | 0x3ff, false},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDLongMacro, ID: 0x1fffff}, "00101", 0x1fffff<<7 | 0x7f, true},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDLongMacro, ID: 0x1fffff}, "00101", 0x0fffff<<7 | 0x7f, false},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDMacro, ID: 1}, "002002", 1<<8 | 1, true},
		{db.GlobalENBID{PLMN: a, Type: db.ENBIDMacro, ID: 1}, "00303", 1<<8 | 1, false},
	} {
		got := s1.OwnsCell(tc.id, []db.PLMN{a, b}, s1ap.EUTRANCGI{
			PLMNIdentity: plmnID(t, tc.plmn[:3], tc.plmn[3:]), CellID: s1ap.CellIdentity(tc.cell),
		})
		if got != tc.owned {
			t.Fatalf("%s owns %s/%07x = %v, want %v", tc.id.Ref(), tc.plmn, tc.cell, got, tc.owned)
		}
	}
}

func TestWarningFromTwoMMEsOfThePWSOperator(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	h, enb, mme1, mme2 := twoMMEs(t, "127.0.2.11", "127.0.2.12")
	h.Ops.Set(pws(operatorA("127.0.2.11", "127.0.2.12")))

	if err := h.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	req := writeReplaceWarning(4370, 9, nil)
	mme1.Send(t, req)
	expectSame(t, enb, marshal(t, req))
	mme2.Send(t, req)
	expectSame(t, enb, marshal(t, req))

	cell := &s1ap.BroadcastCompletedAreaList{CellIDBroadcast: &s1ap.CellIDBroadcast{{ECGI: ecgi(t, "00101", 1, 1)}}}
	enb.Send(t, &s1ap.WriteReplaceWarningResponse{MessageIdentifier: 4370, SerialNumber: 9, BroadcastCompletedAreaList: cell})

	if got := expectWarningResponse(t, mme1, 4370, 9); !reflect.DeepEqual(got, cell) {
		t.Fatalf("first Broadcast Completed Area List = %+v, want %+v", got, cell)
	}

	mme2.Quiet(t, 200*time.Millisecond)
	enb.Send(t, &s1ap.WriteReplaceWarningResponse{MessageIdentifier: 4370, SerialNumber: 9, BroadcastCompletedAreaList: cell})

	if got := expectWarningResponse(t, mme2, 4370, 9); !reflect.DeepEqual(got, cell) {
		t.Fatalf("second Broadcast Completed Area List = %+v, want %+v", got, cell)
	}

	mme1.Quiet(t, 200*time.Millisecond)
}

func TestInvalidWarningRequestFromTheMME(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.2.13")
	h := startGateway(t, mmeListener.Port(), pws(operatorA("127.0.2.13")))
	enb, mme := upAndRunning(t, h, mmeListener)

	body := bodyOf(t, &s1ap.ErrorIndication{Cause: s1ap.Ptr(s1ap.CauseMiscUnspecified.Cause())})

	for _, proc := range []s1ap.ProcedureCode{s1ap.ProcWriteReplaceWarning, s1ap.ProcKill} {
		mme.Send(t, initiating(proc, s1ap.CriticalityReject, body))
		assertDiagnostics(t, expectProtocolError(t, mme, s1ap.CauseProtocolAbstractSyntaxErrorReject).CriticalityDiagnostics,
			proc, s1ap.Ptr(s1ap.CriticalityReject))
	}

	enb.Quiet(t, 200*time.Millisecond)

	for _, proc := range []string{"write_replace_warning", "kill"} {
		if got := testutil.ToFloat64(h.metrics.PWS.WithLabelValues("s1", proc, "invalid")); got != 1 {
			t.Fatalf("%s invalid = %v, want 1", proc, got)
		}
	}
}

func rawResponse(t *testing.T, msg interface{ Marshal() ([]byte, error) }, edit func(p *s1ap.RawPDU)) []byte {
	t.Helper()

	p, err := s1ap.ParseRawPDU(marshal(t, msg))
	if err != nil {
		t.Fatal(err)
	}

	edit(p)

	out, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestInvalidWarningResponsesFromTheRadio(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.2.14")
	h := startGatewayWith(t, s1.Config{CorePort: mmeListener.Port(), WarningWait: time.Minute},
		pws(operatorA("127.0.2.14")))
	enb, mme := upAndRunning(t, h, mmeListener)

	unknownReject := rawResponse(t, &s1ap.WriteReplaceWarningResponse{MessageIdentifier: 4370, SerialNumber: 1},
		func(p *s1ap.RawPDU) {
			if err := p.Insert(p.Len(), s1ap.RawIE{ID: 999, Criticality: s1ap.CriticalityReject, Value: []byte{0}}); err != nil {
				t.Fatal(err)
			}
		})
	badList := rawResponse(t, &s1ap.WriteReplaceWarningResponse{
		MessageIdentifier: 4370, SerialNumber: 2,
		BroadcastCompletedAreaList: &s1ap.BroadcastCompletedAreaList{
			CellIDBroadcast: &s1ap.CellIDBroadcast{{ECGI: ecgi(t, "00101", 1, 1)}},
		},
	}, func(p *s1ap.RawPDU) {
		if err := p.SetValue(indexOf(t, p, s1ap.IDBroadcastCompletedAreaList), []byte{0x40}); err != nil {
			t.Fatal(err)
		}
	})

	for serial, answer := range map[uint16][]byte{1: unknownReject, 2: badList} {
		mme.Send(t, writeReplaceWarning(4370, serial, nil))
		enb.Next(t)
		enb.SendRaw(t, answer)

		if got := expectWarningResponse(t, mme, 4370, serial); got != nil {
			t.Fatalf("Broadcast Completed Area List = %+v, want none", got)
		}

		if serial == 2 {
			ind := expect(t, enb, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication)
			if ind.Cause == nil || !ind.Cause.Equal(s1ap.CauseProtocolTransferSyntaxError.Cause()) {
				t.Fatalf("Error Indication = %+v", ind)
			}
		}

		enb.Quiet(t, 200*time.Millisecond)
	}
}

func TestWarningPendingWhenTheMMEIsLost(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.2.15")
	h := startGatewayWith(t, s1.Config{CorePort: mmeListener.Port(), WarningWait: 300 * time.Millisecond},
		pws(operatorA("127.0.2.15")))
	enb, mme := upAndRunning(t, h, mmeListener)

	req := writeReplaceWarning(4370, 1, nil)
	mme.Send(t, req)
	expectSame(t, enb, marshal(t, req))

	if err := mme.Conn.Abort(); err != nil {
		t.Fatal(err)
	}

	mme = mmeListener.Accept(t)
	setUpMME(t, mme, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
		ServedGUMMEIs: served(t, 1, 1), RelativeMMECapacity: s1ap.Ptr(uint8(1)),
	})

	h.eventually(t, "the silent radio counted", func() bool {
		return testutil.ToFloat64(h.metrics.PWS.WithLabelValues("s1", "write_replace_warning", "timeout")) == 1
	})

	mme.Quiet(t, 300*time.Millisecond)
	enb.Send(t, &s1ap.WriteReplaceWarningResponse{MessageIdentifier: 4370, SerialNumber: 1})
	mme.Quiet(t, 300*time.Millisecond)
}
