package ng_test

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func pws(o db.Operator) db.Operator {
	o.PWSOperator = true
	return o
}

func nrcgi(t *testing.T, gnbID uint32) ngap.NRCGI {
	t.Helper()

	return ngap.NRCGI{PLMNIdentity: plmnID(t, "001", "01"), NRCellIdentity: ngap.NRCellIdentity(uint64(gnbID)<<12 | 1)}
}

func nrTAI(t *testing.T, plmn string, tac uint32) ngap.TAI {
	t.Helper()

	return ngap.TAI{PLMNIdentity: plmnID(t, plmn[:3], plmn[3:]), TAC: ngap.TAC(tac)}
}

func writeReplaceWarning(id, serial uint16, area *ngap.WarningAreaList) *ngap.WriteReplaceWarningRequest {
	return &ngap.WriteReplaceWarningRequest{
		MessageIdentifier: ngap.MessageIdentifier(id), SerialNumber: ngap.SerialNumber(serial), WarningAreaList: area,
		RepetitionPeriod: 10, NumberOfBroadcastsRequested: 1,
	}
}

func expectSame(t *testing.T, p *peer, msg interface{ Marshal() ([]byte, error) }) {
	t.Helper()

	want, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	f := p.NextFrame(t)
	if !bytes.Equal(f.Raw, want) || f.Stream != 0 {
		t.Fatalf("got %s %x on stream %d, want %x on stream 0", procedureName(f.PDU), f.Raw, f.Stream, want)
	}
}

func expectWarningResponse(t *testing.T, amf *peer, id, serial uint16) *ngap.BroadcastCompletedAreaList {
	t.Helper()

	resp := expect(t, amf, "successful", ngap.ProcWriteReplaceWarning, ngap.ParseWriteReplaceWarningResponse)
	if resp.MessageIdentifier != ngap.MessageIdentifier(id) || resp.SerialNumber != ngap.SerialNumber(serial) {
		t.Fatalf("Write-Replace Warning Response for %d/%d, want %d/%d", resp.MessageIdentifier, resp.SerialNumber, id, serial)
	}

	return resp.BroadcastCompletedAreaList
}

func TestPWSThroughTheGateway(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.3.1")
	amfsB := listenAMFAt(t, "127.0.3.2", amfs.Port())
	h := startGateway(t, amfs.Port(), pws(operatorA("127.0.3.1")), operatorB("127.0.3.2"))

	gnb1 := h.connectGNB(t)
	gnb1.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101", "002002")))

	amfA, _ := setupAMF(t, amfs, setupResponse(t, "amfA", 255, "00101", guami(t, "00101", 2, 1, 0)))
	amfB, _ := setupAMF(t, amfsB, setupResponse(t, "amfB", 255, "002002", guami(t, "002002", 4, 3, 0)))

	resp := expect(t, gnb1, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	for len(resp.ServedGUAMIList) < 2 {
		_, raw := expectRaw(t, gnb1, "initiating", ngap.ProcAMFConfigurationUpdate)
		if ok, err := raw.Get(ngap.IDServedGUAMIList, &resp.ServedGUAMIList); !ok || err != nil {
			t.Fatalf("Served GUAMI List: %v %v", ok, err)
		}

		gnb1.Send(t, &ngap.AMFConfigurationUpdateAcknowledge{})
	}

	gnb2 := h.connectGNB(t)
	gnb2.Send(t, setupRequest(t, 2, "gnb2", ngap.PagingDRXv128, ta(t, 2, "00101")))
	expect(t, gnb2, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	expect(t, amfA, "initiating", ngap.ProcRANConfigurationUpdate, ngap.ParseRANConfigurationUpdate)
	amfA.Send(t, &ngap.RANConfigurationUpdateAcknowledge{})

	amfB.Send(t, writeReplaceWarning(4370, 1, nil))

	if l := expectWarningResponse(t, amfB, 4370, 1); l != nil {
		t.Fatalf("Broadcast Completed Area List = %+v, want none", l)
	}

	byCell := writeReplaceWarning(4370, 2, &ngap.WarningAreaList{NRCGIs: []ngap.NRCGI{nrcgi(t, 2)}})
	amfA.Send(t, byCell)
	expectSame(t, gnb2, byCell)

	completed := &ngap.BroadcastCompletedAreaList{CellIDBroadcastNR: []ngap.CellIDBroadcastNRItem{{NRCGI: nrcgi(t, 2)}}}
	gnb2.Send(t, &ngap.WriteReplaceWarningResponse{MessageIdentifier: 4370, SerialNumber: 2, BroadcastCompletedAreaList: completed})

	if got := expectWarningResponse(t, amfA, 4370, 2); !reflect.DeepEqual(got, completed) {
		t.Fatalf("Broadcast Completed Area List = %+v, want %+v", got, completed)
	}

	cancel := &ngap.PWSCancelRequest{
		MessageIdentifier: 4370, SerialNumber: 2, CancelAllWarningMessages: ngap.Ptr(ngap.CancelAllWarningMessagesTrue),
	}
	amfA.Send(t, cancel)
	expectSame(t, gnb1, cancel)
	expectSame(t, gnb2, cancel)

	gnb1.Send(t, &ngap.PWSCancelResponse{
		MessageIdentifier: 4370, SerialNumber: 2,
		BroadcastCancelledAreaList: &ngap.BroadcastCancelledAreaList{TAICancelledNR: []ngap.TAICancelledNRItem{{
			TAI:                 nrTAI(t, "00101", 1),
			CancelledCellsInTAI: []ngap.CancelledCellsInTAINRItem{{NRCGI: nrcgi(t, 1), NumberOfBroadcasts: 2}},
		}}},
	})
	amfA.Quiet(t, 100*time.Millisecond)
	gnb2.Send(t, &ngap.PWSCancelResponse{
		MessageIdentifier: 4370, SerialNumber: 2,
		BroadcastCancelledAreaList: &ngap.BroadcastCancelledAreaList{EmergencyAreaIDCancelledNR: []ngap.EmergencyAreaIDCancelledNRItem{{
			EmergencyAreaID:     ngap.EmergencyAreaID{0, 0, 1},
			CancelledCellsInEAI: []ngap.CancelledCellsInEAINRItem{{NRCGI: nrcgi(t, 2), NumberOfBroadcasts: 1}},
		}}},
	})

	cancelled := expect(t, amfA, "successful", ngap.ProcPWSCancel, ngap.ParsePWSCancelResponse)
	want := &ngap.BroadcastCancelledAreaList{CellIDCancelledNR: []ngap.CellIDCancelledNRItem{
		{NRCGI: nrcgi(t, 1), NumberOfBroadcasts: 2}, {NRCGI: nrcgi(t, 2), NumberOfBroadcasts: 1},
	}}

	if cancelled.MessageIdentifier != 4370 || cancelled.SerialNumber != 2 ||
		!reflect.DeepEqual(cancelled.BroadcastCancelledAreaList, want) {
		t.Fatalf("PWS Cancel Response = %+v, want %+v", cancelled, want)
	}

	amfA.Send(t, writeReplaceWarning(4370, 3, &ngap.WarningAreaList{EUTRACGIs: []ngap.EUTRACGI{{
		PLMNIdentity: plmnID(t, "001", "01"), EUTRACellIdentity: 1<<8 | 1,
	}}}))

	if l := expectWarningResponse(t, amfA, 4370, 3); l != nil {
		t.Fatalf("Broadcast Completed Area List = %+v, want none", l)
	}

	radio := ngap.GlobalRANNodeID{Kind: ngap.RANNodeIDGNB, PLMNIdentity: plmnID(t, "001", "01"), Value: 1, Bits: 24}
	restart := func(id ngap.GlobalRANNodeID) *ngap.PWSRestartIndication {
		return &ngap.PWSRestartIndication{
			CellIDListForRestart:          ngap.CellIDListForRestart{NRCGIs: []ngap.NRCGI{nrcgi(t, 1)}},
			GlobalRANNodeID:               id,
			TAIListForRestart:             ngap.TAIListForRestart{nrTAI(t, "00101", 1), nrTAI(t, "002002", 1)},
			EmergencyAreaIDListForRestart: ngap.EmergencyAreaIDListForRestart{{0, 0, 1}},
		}
	}

	gnb1.Send(t, restart(radio))
	expectSame(t, amfA, restart(ngap.GlobalRANNodeID{
		Kind: ngap.RANNodeIDGNB, PLMNIdentity: plmnID(t, "001", "01"), Value: 0xabcde, Bits: 22,
	}))

	gnb2.Send(t, &ngap.PWSFailureIndication{
		PWSFailedCellIDList: ngap.PWSFailedCellIDList{NRCGIs: []ngap.NRCGI{nrcgi(t, 2)}}, GlobalRANNodeID: radio,
	})

	ind := expect(t, amfA, "initiating", ngap.ProcPWSFailureIndication, ngap.ParsePWSFailureIndication)
	if ind.GlobalRANNodeID.Value != 0xabcde || ind.GlobalRANNodeID.Bits != 22 ||
		!reflect.DeepEqual(ind.PWSFailedCellIDList.NRCGIs, []ngap.NRCGI{nrcgi(t, 2)}) {
		t.Fatalf("PWS Failure Indication = %+v", ind)
	}

	for _, p := range []*peer{gnb1, gnb2, amfA, amfB} {
		p.Quiet(t, 200*time.Millisecond)
	}

	for key, want := range map[[2]string]float64{
		{"write_replace_warning", "relayed"}: 1, {"write_replace_warning", "not_pws_operator"}: 1,
		{"write_replace_warning", "no_radio"}: 1, {"pws_cancel", "relayed"}: 1,
		{"pws_restart_indication", "relayed"}: 1, {"pws_failure_indication", "relayed"}: 1,
	} {
		if got := testutil.ToFloat64(h.metrics.PWS.WithLabelValues("ng", key[0], key[1])); got != want {
			t.Fatalf("%s %s = %v, want %v", key[0], key[1], got, want)
		}
	}
}
