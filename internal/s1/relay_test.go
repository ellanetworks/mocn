package s1_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var nas = s1ap.NASPDU{0x07, 0x41, 0x71, 0x08, 0x09, 0x10, 0x10, 0x00, 0x00, 0x00, 0x00, 0x01}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func initialUE(t *testing.T, enbUEID s1ap.ENBUES1APID) *s1ap.InitialUEMessage {
	t.Helper()

	return &s1ap.InitialUEMessage{
		ENBUES1APID:           enbUEID,
		NASPDU:                nas,
		TAI:                   s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
		EUTRANCGI:             &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 0x19b01},
		RRCEstablishmentCause: s1ap.Ptr(s1ap.RRCCauseMOSignalling),
	}
}

func expectRaw(t *testing.T, p *peer, kind string, proc s1ap.ProcedureCode) (frame, *s1ap.RawPDU) {
	t.Helper()

	f := p.nextFrame(t)

	raw, err := s1ap.ParseRawPDU(f.raw)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[s1ap.TriggeringMessage]string{
		s1ap.TriggeringInitiatingMessage: "initiating", s1ap.TriggeringSuccessfulOutcome: "successful",
		s1ap.TriggeringUnsuccessfulOutcome: "unsuccessful",
	}

	if kinds[raw.Trigger()] != kind || raw.ProcedureCode() != proc {
		t.Fatalf("got %s, want %s %s", procedureName(f.pdu), kind, proc)
	}

	return f, raw
}

func assertRelayed(t *testing.T, sent interface{ Marshal() ([]byte, error) }, got frame) {
	t.Helper()

	b, err := sent.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	in, err := s1ap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	out, err := s1ap.ParseRawPDU(got.raw)
	if err != nil {
		t.Fatal(err)
	}

	if got.stream != 1 {
		t.Fatalf("UE-associated message on stream %d, want 1", got.stream)
	}

	if in.Trigger() != out.Trigger() || in.ProcedureCode() != out.ProcedureCode() || in.Criticality() != out.Criticality() {
		t.Fatalf("relayed %s, sent %s", out.ProcedureCode(), in.ProcedureCode())
	}

	a, z := in.IEs(), out.IEs()
	if len(a) != len(z) {
		t.Fatalf("relayed %d IEs, sent %d", len(z), len(a))
	}

	for i := range a {
		if a[i].ID != z[i].ID || a[i].Criticality != z[i].Criticality {
			t.Fatalf("IE %d: relayed %s, sent %s", i, z[i].ID, a[i].ID)
		}

		switch a[i].ID {
		case s1ap.IDMMEUES1APID, s1ap.IDENBUES1APID, s1ap.IDUES1APIDs:
			continue
		}

		if !bytes.Equal(a[i].Value, z[i].Value) {
			t.Fatalf("IE %s changed: %x, sent %x", a[i].ID, z[i].Value, a[i].Value)
		}
	}
}

func uplinkNAS(t *testing.T, mmeID s1ap.MMEUES1APID, enbID s1ap.ENBUES1APID) *s1ap.UplinkNASTransport {
	t.Helper()

	return &s1ap.UplinkNASTransport{
		MMEUES1APID: mmeID, ENBUES1APID: enbID, NASPDU: nas,
		EUTRANCGI: &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 0x19b02},
		TAI:       &s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
	}
}

func ids(t *testing.T, p *s1ap.RawPDU) (s1ap.MMEUES1APID, s1ap.ENBUES1APID) {
	t.Helper()

	mme, _, err := p.MMEUES1APID()
	if err != nil {
		t.Fatal(err)
	}

	enb, _, err := p.ENBUES1APID()
	if err != nil {
		t.Fatal(err)
	}

	return mme, enb
}

type attached struct {
	enbUEID  s1ap.ENBUES1APID
	ranMMEID s1ap.MMEUES1APID
	mmeENBID s1ap.ENBUES1APID
	mmeUEID  s1ap.MMEUES1APID
}

func attach(t *testing.T, enb, mme *peer, enbUEID s1ap.ENBUES1APID, mmeUEID s1ap.MMEUES1APID) attached {
	t.Helper()

	init := initialUE(t, enbUEID)
	enb.send(t, init)

	f, raw := expectRaw(t, mme, "initiating", s1ap.ProcInitialUEMessage)
	assertRelayed(t, init, f)

	_, mmeENBID := ids(t, raw)

	dl := &s1ap.DownlinkNASTransport{MMEUES1APID: mmeUEID, ENBUES1APID: mmeENBID, NASPDU: nas}
	mme.send(t, dl)

	f, raw = expectRaw(t, enb, "initiating", s1ap.ProcDownlinkNASTransport)
	assertRelayed(t, dl, f)

	ranMMEID, gotENB := ids(t, raw)
	if gotENB != enbUEID {
		t.Fatalf("eNB UE S1AP ID toward the radio = %d, want %d", gotENB, enbUEID)
	}

	return attached{enbUEID: enbUEID, ranMMEID: ranMMEID, mmeENBID: mmeENBID, mmeUEID: mmeUEID}
}

func securityKey() s1ap.SecurityKey {
	var key s1ap.SecurityKey
	for i := range key {
		key[i] = byte(0xa0 + i)
	}

	return key
}

func contextSetup(ue attached, mmeSide bool) *s1ap.InitialContextSetupRequest {
	req := &s1ap.InitialContextSetupRequest{
		MMEUES1APID:               ue.ranMMEID,
		ENBUES1APID:               ue.enbUEID,
		UEAggregateMaximumBitRate: s1ap.UEAggregateMaximumBitRate{DL: 1000000000, UL: 500000000},
		ERABToBeSetup: []s1ap.ERABToBeSetupItemCtxtSUReq{{
			ERABID: 5, QoS: s1ap.ERABLevelQoSParameters{QCI: 9, ARP: s1ap.AllocationAndRetentionPriority{
				PriorityLevel: 15, PreemptionCapability: s1ap.PreemptionShallNotTrigger,
				PreemptionVulnerability: s1ap.PreemptionNotPreemptable,
			}},
			TransportLayerAddress: s1ap.TransportLayerAddress{10, 45, 0, 1}, GTPTEID: 0x12345678, NASPDU: nas,
		}},
		UESecurityCapabilities: s1ap.UESecurityCapabilities{EncryptionAlgorithms: 0x8000, IntegrityProtectionAlgorithms: 0xc000},
		SecurityKey:            securityKey(),
		UERadioCapability:      []byte{0xaa, 0xbb},
	}

	if mmeSide {
		req.MMEUES1APID, req.ENBUES1APID = ue.mmeUEID, ue.mmeENBID
	}

	return req
}

func TestUELifecycleThroughRelay(t *testing.T) {
	skipWithoutSCTP(t)

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mmeListener := listenMME(t, "127.0.0.20")
	h := startGatewayWith(t, s1.Config{MMEPort: mmeListener.port(), Logger: logger}, operatorA("127.0.0.20"))
	enb, mme := upAndRunning(t, h, mmeListener)

	ue := attach(t, enb, mme, 7, 1000)

	ues := h.gw.UEs()
	if len(ues) != 1 {
		t.Fatalf("UEs = %+v, want one", ues)
	}

	got := ues[0]
	if got.RadioENBUES1APID != 7 || got.RadioMMEUES1APID != uint32(ue.ranMMEID) ||
		got.MMEENBUES1APID != uint32(ue.mmeENBID) || got.MMEUES1APID == nil || *got.MMEUES1APID != 1000 ||
		got.OperatorName != "A" || got.PLMN != plmnA || got.RadioGlobalENBID.ID != 1 ||
		len(got.MMETNLAddresses) != 1 || got.MMETNLAddresses[0] != "127.0.0.20" ||
		got.TAI == nil || got.TAI.TAC != 1 || got.ECGI == nil || got.ECGI.CellID != 0x19b01 || got.Since.IsZero() {
		t.Fatalf("UE = %+v", got)
	}

	ul := &s1ap.UplinkNASTransport{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: 7, NASPDU: nas,
		EUTRANCGI: &s1ap.EUTRANCGI{PLMNIdentity: plmnID(t, "001", "01"), CellID: 0x19b02},
		TAI:       &s1ap.TAI{PLMNIdentity: plmnID(t, "001", "01"), TAC: 1},
	}
	enb.send(t, ul)

	f, raw := expectRaw(t, mme, "initiating", s1ap.ProcUplinkNASTransport)
	assertRelayed(t, ul, f)

	if m, e := ids(t, raw); m != 1000 || e != ue.mmeENBID {
		t.Fatalf("Uplink NAS Transport IDs = %d/%d, want 1000/%d", m, e, ue.mmeENBID)
	}

	if ues := h.gw.UEs(); ues[0].ECGI.CellID != 0x19b02 {
		t.Fatalf("last ECGI = %+v", ues[0].ECGI)
	}

	ics := contextSetup(ue, true)
	mme.send(t, ics)

	f, raw = expectRaw(t, enb, "initiating", s1ap.ProcInitialContextSetup)
	assertRelayed(t, ics, f)

	if m, e := ids(t, raw); m != ue.ranMMEID || e != 7 {
		t.Fatalf("Initial Context Setup Request IDs = %d/%d", m, e)
	}

	icsResp := &s1ap.InitialContextSetupResponse{
		MMEUES1APID: s1ap.Ptr(ue.ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(7)),
		ERABSetup: []s1ap.ERABSetupItemCtxtSURes{{ERABID: 5, TransportLayerAddress: s1ap.TransportLayerAddress{10, 90, 1, 21}, GTPTEID: 1}},
	}
	enb.send(t, icsResp)

	f, _ = expectRaw(t, mme, "successful", s1ap.ProcInitialContextSetup)
	assertRelayed(t, icsResp, f)

	req := &s1ap.UEContextReleaseRequest{
		MMEUES1APID: ue.ranMMEID, ENBUES1APID: 7,
		Cause: &s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: 20},
	}
	enb.send(t, req)

	f, _ = expectRaw(t, mme, "initiating", s1ap.ProcUEContextReleaseRequest)
	assertRelayed(t, req, f)

	if len(h.gw.UEs()) != 1 {
		t.Fatal("UE Context Release Request freed the UE")
	}

	cmd := &s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{MMEUES1APID: 1000, ENBUES1APID: ue.mmeENBID, Pair: true},
		Cause:     &s1ap.Cause{Group: s1ap.CauseGroupNAS, Value: 0},
	}
	mme.send(t, cmd)

	f, raw = expectRaw(t, enb, "initiating", s1ap.ProcUEContextRelease)
	assertRelayed(t, cmd, f)

	pair, _, err := raw.UES1APIDs()
	if err != nil || !pair.Pair || pair.MMEUES1APID != ue.ranMMEID || pair.ENBUES1APID != 7 {
		t.Fatalf("UE Context Release Command IDs = %+v, %v", pair, err)
	}

	complete := &s1ap.UEContextReleaseComplete{MMEUES1APID: s1ap.Ptr(ue.ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(7))}
	enb.send(t, complete)

	f, raw = expectRaw(t, mme, "successful", s1ap.ProcUEContextRelease)
	assertRelayed(t, complete, f)

	if m, e := ids(t, raw); m != 1000 || e != ue.mmeENBID {
		t.Fatalf("UE Context Release Complete IDs = %d/%d", m, e)
	}

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after release = %+v", ues)
	}

	key := securityKey()
	text := logs.String()

	secrets := []string{
		hex.EncodeToString(key[:]), strings.ToUpper(hex.EncodeToString(key[:])), fmt.Sprintf("%v", key),
		hex.EncodeToString(nas), string(key[:]),
	}

	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("logs contain a Security Key or NAS-PDU: %q", secret)
		}
	}

	if !strings.Contains(text, "INITIAL UE MESSAGE relayed") {
		t.Fatalf("logs were not captured: %s", text)
	}
}

func TestReleaseCommandWithMMEUEIDOnly(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.21")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.21"))
	enb, mme := upAndRunning(t, h, mmeListener)

	ue := attach(t, enb, mme, 3, 300)

	mme.send(t, &s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{MMEUES1APID: 300}, Cause: &s1ap.Cause{Group: s1ap.CauseGroupNAS, Value: 0},
	})

	_, raw := expectRaw(t, enb, "initiating", s1ap.ProcUEContextRelease)

	got, _, err := raw.UES1APIDs()
	if err != nil || got.Pair || got.MMEUES1APID != ue.ranMMEID {
		t.Fatalf("UE Context Release Command IDs = %+v, %v", got, err)
	}

	enb.send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: s1ap.Ptr(ue.ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(3))})
	expectRaw(t, mme, "successful", s1ap.ProcUEContextRelease)

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after release = %+v", ues)
	}
}

func expectErrorIndication(t *testing.T, p *peer, cause int, mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID) {
	t.Helper()

	ind := expect(t, p, "initiating", s1ap.ProcErrorIndication, s1ap.ParseErrorIndication)
	if ind.Cause == nil || ind.Cause.Group != s1ap.CauseGroupRadioNetwork || ind.Cause.Value != cause {
		t.Fatalf("Error Indication cause = %v, want radio network %d", ind.Cause, cause)
	}

	if (ind.MMEUES1APID == nil) != (mmeID == nil) || (mmeID != nil && *ind.MMEUES1APID != *mmeID) ||
		(ind.ENBUES1APID == nil) != (enbID == nil) || (enbID != nil && *ind.ENBUES1APID != *enbID) {
		t.Fatalf("Error Indication IDs = %v/%v, want %v/%v", ind.MMEUES1APID, ind.ENBUES1APID, mmeID, enbID)
	}
}

func expectReset(t *testing.T, p *peer, want ...s1ap.UEAssociatedLogicalS1ConnectionItem) *s1ap.Reset {
	t.Helper()

	reset := expect(t, p, "initiating", s1ap.ProcReset, s1ap.ParseReset)
	if reset.ResetType.All || len(reset.ResetType.Part) != len(want) {
		t.Fatalf("Reset = %+v, want a partial reset of %d connections", reset.ResetType, len(want))
	}

	for i, w := range want {
		got := reset.ResetType.Part[i]
		if fmt.Sprint(got.MMEUES1APID == nil, got.ENBUES1APID == nil) != fmt.Sprint(w.MMEUES1APID == nil, w.ENBUES1APID == nil) ||
			(w.MMEUES1APID != nil && *got.MMEUES1APID != *w.MMEUES1APID) ||
			(w.ENBUES1APID != nil && *got.ENBUES1APID != *w.ENBUES1APID) {
			t.Fatalf("Reset item %d = %v/%v, want %v/%v", i, got.MMEUES1APID, got.ENBUES1APID, w.MMEUES1APID, w.ENBUES1APID)
		}
	}

	return reset
}

func item(mme s1ap.MMEUES1APID, enb s1ap.ENBUES1APID) s1ap.UEAssociatedLogicalS1ConnectionItem {
	return s1ap.UEAssociatedLogicalS1ConnectionItem{MMEUES1APID: s1ap.Ptr(mme), ENBUES1APID: s1ap.Ptr(enb)}
}

func TestUnknownUEIDs(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.22")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.22"))
	enb, mme := upAndRunning(t, h, mmeListener)

	enb.send(t, uplinkNAS(t, 4242, 9))
	expectErrorIndication(t, enb, s1ap.CauseRadioNetworkUnknownMMEUES1APID, s1ap.Ptr(s1ap.MMEUES1APID(4242)), s1ap.Ptr(s1ap.ENBUES1APID(9)))

	mme.send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 5, ENBUES1APID: 4242, NASPDU: nas})
	expectErrorIndication(t, mme, s1ap.CauseRadioNetworkUnknownENBUES1APID, s1ap.Ptr(s1ap.MMEUES1APID(5)), s1ap.Ptr(s1ap.ENBUES1APID(4242)))

	enb.send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(4242)), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(9))})
	enb.quiet(t, 200*time.Millisecond)
	mme.quiet(t, 200*time.Millisecond)

	ue := attach(t, enb, mme, 1, 100)

	enb.send(t, uplinkNAS(t, ue.ranMMEID, 2))
	expectErrorIndication(t, enb, s1ap.CauseRadioNetworkUnknownPairUES1APID, &ue.ranMMEID, s1ap.Ptr(s1ap.ENBUES1APID(2)))
	expectReset(t, mme, item(100, ue.mmeENBID))

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after an inconsistent pair = %+v", ues)
	}

	ue = attach(t, enb, mme, 1, 101)

	mme.send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 999, ENBUES1APID: ue.mmeENBID, NASPDU: nas})
	expectErrorIndication(t, mme, s1ap.CauseRadioNetworkUnknownPairUES1APID, s1ap.Ptr(s1ap.MMEUES1APID(999)), &ue.mmeENBID)
	expectReset(t, enb, item(ue.ranMMEID, 1))

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after an inconsistent pair = %+v", ues)
	}

	first := attach(t, enb, mme, 1, 102)

	enb.send(t, initialUE(t, 2))
	_, raw := expectRaw(t, mme, "initiating", s1ap.ProcInitialUEMessage)
	_, secondENBID := ids(t, raw)

	mme.send(t, &s1ap.DownlinkNASTransport{MMEUES1APID: 102, ENBUES1APID: secondENBID, NASPDU: nas})
	expectErrorIndication(t, mme, s1ap.CauseRadioNetworkUnknownMMEUES1APID, s1ap.Ptr(s1ap.MMEUES1APID(102)), &secondENBID)

	r := expect(t, enb, "initiating", s1ap.ProcReset, s1ap.ParseReset)
	if len(r.ResetType.Part) != 2 || *r.ResetType.Part[0].ENBUES1APID != 2 || r.ResetType.Part[0].MMEUES1APID != nil ||
		*r.ResetType.Part[1].ENBUES1APID != 1 || *r.ResetType.Part[1].MMEUES1APID != first.ranMMEID {
		t.Fatalf("Reset after an already allocated MME UE S1AP ID = %+v", r.ResetType.Part)
	}

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after an already allocated MME UE S1AP ID = %+v", ues)
	}

	first = attach(t, enb, mme, 1, 103)

	enb.send(t, initialUE(t, 1))
	expectErrorIndication(t, enb, s1ap.CauseRadioNetworkUnknownENBUES1APID, nil, s1ap.Ptr(s1ap.ENBUES1APID(1)))
	expectReset(t, mme, item(103, first.mmeENBID))
}

func TestErrorIndicationRelayed(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.23")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.23"))
	enb, mme := upAndRunning(t, h, mmeListener)

	ue := attach(t, enb, mme, 4, 400)

	ind := &s1ap.ErrorIndication{
		MMEUES1APID: s1ap.Ptr(ue.ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(4)),
		Cause: &s1ap.Cause{Group: s1ap.CauseGroupProtocol, Value: s1ap.CauseProtocolSemanticError},
	}
	enb.send(t, ind)

	f, raw := expectRaw(t, mme, "initiating", s1ap.ProcErrorIndication)
	assertRelayed(t, ind, f)

	if m, e := ids(t, raw); m != 400 || e != ue.mmeENBID {
		t.Fatalf("Error Indication IDs = %d/%d", m, e)
	}

	if len(h.gw.UEs()) != 1 {
		t.Fatal("a protocol Error Indication freed the UE")
	}

	mme.send(t, &s1ap.ErrorIndication{
		MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(400)), ENBUES1APID: s1ap.Ptr(ue.mmeENBID),
		Cause: &s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownPairUES1APID},
	})

	_, raw = expectRaw(t, enb, "initiating", s1ap.ProcErrorIndication)
	if m, e := ids(t, raw); m != ue.ranMMEID || e != 4 {
		t.Fatalf("Error Indication IDs = %d/%d", m, e)
	}

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after an unknown-pair Error Indication = %+v", ues)
	}

	enb.quiet(t, 200*time.Millisecond)
	mme.quiet(t, 200*time.Millisecond)
}

func TestRadioLossResetsItsUEsTowardTheMME(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.24")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.24"))
	enb, mme := upAndRunning(t, h, mmeListener)

	a := attach(t, enb, mme, 1, 100)
	b := attach(t, enb, mme, 2, 101)

	enb2 := h.connectENB(t)
	enb2.send(t, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	expect(t, enb2, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	c := attach(t, enb2, mme, 1, 102)

	_ = enb.conn.Abort()

	r := expectReset(t, mme, item(100, a.mmeENBID), item(101, b.mmeENBID))
	if r.Cause == nil || r.Cause.Group != s1ap.CauseGroupTransport || r.Cause.Value != s1ap.CauseTransportResourceUnavailable {
		t.Fatalf("Reset cause = %v", r.Cause)
	}

	mme.send(t, &s1ap.ResetAcknowledge{})

	h.eventually(t, "one UE left", func() bool { return len(h.gw.UEs()) == 1 })

	if u := h.gw.UEs()[0]; u.MMEENBUES1APID != uint32(c.mmeENBID) {
		t.Fatalf("UE left = %+v", u)
	}
}

func TestRepeatedS1SetupResetsTheRadioUEs(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.25")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.25"))
	enb, mme := upAndRunning(t, h, mmeListener)

	a := attach(t, enb, mme, 1, 100)

	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	r := expectReset(t, mme, item(100, a.mmeENBID))
	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	if r.Cause == nil || r.Cause.Group != s1ap.CauseGroupRadioNetwork ||
		r.Cause.Value != s1ap.CauseRadioNetworkReleaseDueToEUTRANGeneratedReason {
		t.Fatalf("Reset cause = %v", r.Cause)
	}

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after a repeated S1 Setup = %+v", ues)
	}
}

func TestMMELossResetsItsUEsTowardTheRadios(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.26")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.26"))
	enb, mme := upAndRunning(t, h, mmeListener)

	a := attach(t, enb, mme, 1, 100)

	enb.send(t, initialUE(t, 2))
	expectRaw(t, mme, "initiating", s1ap.ProcInitialUEMessage)

	_ = mme.conn.Abort()

	r := expect(t, enb, "initiating", s1ap.ProcReset, s1ap.ParseReset)
	if len(r.ResetType.Part) != 2 || *r.ResetType.Part[0].MMEUES1APID != a.ranMMEID || *r.ResetType.Part[0].ENBUES1APID != 1 ||
		r.ResetType.Part[1].MMEUES1APID != nil || *r.ResetType.Part[1].ENBUES1APID != 2 {
		t.Fatalf("Reset after MME loss = %+v", r.ResetType.Part)
	}

	if r.Cause == nil || r.Cause.Group != s1ap.CauseGroupTransport {
		t.Fatalf("Reset cause = %v", r.Cause)
	}

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after MME loss = %+v", ues)
	}
}

func TestResetTranslation(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.27")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.27"))
	enb, mme := upAndRunning(t, h, mmeListener)

	enb2 := h.connectENB(t)
	enb2.send(t, setupRequest(t, 2, "enb2", s1ap.PagingDRXv128, ta(t, 1, "00101")))
	expect(t, enb2, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)

	cause := &s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscOMIntervention}

	a := attach(t, enb, mme, 1, 100)
	b := attach(t, enb2, mme, 1, 101)

	mme.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{All: true}})

	if ack := expect(t, mme, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge); len(ack.ConnectionList) != 0 {
		t.Fatalf("Reset Acknowledge = %+v", ack.ConnectionList)
	}

	expectReset(t, enb, item(a.ranMMEID, 1))
	expectReset(t, enb2, item(b.ranMMEID, 1))

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after a full Reset from the MME = %+v", ues)
	}

	a = attach(t, enb, mme, 1, 102)
	b = attach(t, enb, mme, 2, 103)
	c := attach(t, enb2, mme, 1, 104)

	part := []s1ap.UEAssociatedLogicalS1ConnectionItem{
		{MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(102))},
		{ENBUES1APID: s1ap.Ptr(c.mmeENBID)},
		{MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(77))},
	}
	mme.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{Part: part}})

	ack := expect(t, mme, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)
	if len(ack.ConnectionList) != 3 || *ack.ConnectionList[0].MMEUES1APID != 102 || *ack.ConnectionList[1].ENBUES1APID != c.mmeENBID ||
		*ack.ConnectionList[2].MMEUES1APID != 77 {
		t.Fatalf("Reset Acknowledge = %+v", ack.ConnectionList)
	}

	expectReset(t, enb, item(a.ranMMEID, 1))
	expectReset(t, enb2, item(c.ranMMEID, 1))

	if ues := h.gw.UEs(); len(ues) != 1 || ues[0].RadioMMEUES1APID != uint32(b.ranMMEID) {
		t.Fatalf("UEs after a partial Reset from the MME = %+v", ues)
	}

	enb.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{All: true}})
	expect(t, enb, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge)
	expectReset(t, mme, item(103, b.mmeENBID))

	a = attach(t, enb, mme, 5, 105)
	b = attach(t, enb, mme, 6, 106)

	enb.send(t, &s1ap.Reset{Cause: cause, ResetType: s1ap.ResetType{Part: []s1ap.UEAssociatedLogicalS1ConnectionItem{
		{ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(6))},
	}}})

	if ack := expect(t, enb, "successful", s1ap.ProcReset, s1ap.ParseResetAcknowledge); len(ack.ConnectionList) != 1 {
		t.Fatalf("Reset Acknowledge = %+v", ack.ConnectionList)
	}

	reset := expect(t, mme, "initiating", s1ap.ProcReset, s1ap.ParseReset)
	if len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].MMEUES1APID != 106 ||
		*reset.ResetType.Part[0].ENBUES1APID != b.mmeENBID || reset.Cause == nil || *reset.Cause != *cause {
		t.Fatalf("Reset toward the MME = %+v", reset)
	}

	if ues := h.gw.UEs(); len(ues) != 1 || ues[0].MMEENBUES1APID != uint32(a.mmeENBID) {
		t.Fatalf("UEs after a partial Reset from the radio = %+v", ues)
	}
}

func TestMMESelectionBySTMSIAcrossTwoMMEs(t *testing.T) {
	skipWithoutSCTP(t)

	first := listenMME(t, "127.0.0.28")
	second := listenMMEAt(t, "127.0.0.29", first.port())

	h := startGatewayWith(t, s1.Config{MMEPort: first.port(), IntN: func(int) int { return 0 }},
		operatorA("127.0.0.28", "127.0.0.29"))

	mme1, mme2 := first.accept(t), second.accept(t)
	enb := h.connectENB(t)
	enb.send(t, setupRequest(t, 1, "enb1", s1ap.PagingDRXv128, ta(t, 1, "00101")))

	for i, m := range []*peer{mme1, mme2} {
		setUpMME(t, m, s1ap.SupportedTAs{ta(t, 1, "00101")}, s1ap.PagingDRXv128, &s1ap.S1SetupResponse{
			ServedGUMMEIs: served(t, 1, byte(i+1)), RelativeMMECapacity: s1ap.Ptr(uint8(10)),
		})
	}

	expect(t, enb, "successful", s1ap.ProcS1Setup, s1ap.ParseS1SetupResponse)
	h.eventually(t, "both MMEs up", func() bool {
		a, _ := h.gw.MMEStatus(1, []string{"127.0.0.28"})
		b, _ := h.gw.MMEStatus(1, []string{"127.0.0.29"})

		return a.State == s1.MMEUp && b.State == s1.MMEUp
	})

	msg := initialUE(t, 1)
	msg.STMSI = &s1ap.STMSI{MMEC: 2, MTMSI: 0x01020304}
	enb.send(t, msg)
	expectRaw(t, mme2, "initiating", s1ap.ProcInitialUEMessage)

	msg = initialUE(t, 2)
	enb.send(t, msg)
	expectRaw(t, mme1, "initiating", s1ap.ProcInitialUEMessage)

	msg = initialUE(t, 3)
	msg.GUMMEI = &s1ap.GUMMEI{PLMNIdentity: plmnID(t, "001", "01"), MMEGroupID: s1ap.MMEGroupID{0, 1}, MMECode: 2}
	msg.STMSI = &s1ap.STMSI{MMEC: 1}
	enb.send(t, msg)
	expectRaw(t, mme2, "initiating", s1ap.ProcInitialUEMessage)

	mme1.quiet(t, 200*time.Millisecond)

	ues := h.gw.UEs()
	if len(ues) != 3 || ues[0].MMETNLAddresses[0] != "127.0.0.29" || ues[1].MMETNLAddresses[0] != "127.0.0.28" {
		t.Fatalf("UEs = %+v", ues)
	}
}

func TestInitialUEMessageForAnUnknownPLMN(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.30")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.30"))
	enb, mme := upAndRunning(t, h, mmeListener)

	msg := initialUE(t, 1)
	msg.TAI.PLMNIdentity = plmnID(t, "999", "99")
	enb.send(t, msg)

	expectReleaseCommand(t, enb, 1, s1ap.CauseGroupMisc, s1ap.CauseMiscUnknownPLMN)

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs = %+v", ues)
	}

	enb.send(t, initialUE(t, 1))
	expectErrorIndication(t, enb, s1ap.CauseRadioNetworkUnknownENBUES1APID, nil, s1ap.Ptr(s1ap.ENBUES1APID(1)))

	enb.send(t, msg)
	ranMMEID := expectReleaseCommand(t, enb, 1, s1ap.CauseGroupMisc, s1ap.CauseMiscUnknownPLMN)
	enb.send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: s1ap.Ptr(ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(1))})
	mme.quiet(t, 200*time.Millisecond)

	attach(t, enb, mme, 1, 200)

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(h.gw.Collectors()...)

	want := `
# HELP ellamocn_initial_ue_messages_total INITIAL UE MESSAGEs received from radios, by interface (s1) and result (relayed: sent to an MME; unknown_plmn: no operator serves the PLMN; no_mme: no selectable MME of the operator; rejected: reuses an allocated eNB UE S1AP ID, an Error Indication is sent; invalid: undecodable or missing a mandatory IE; error: internal failure or failed send to the MME).
# TYPE ellamocn_initial_ue_messages_total counter
ellamocn_initial_ue_messages_total{interface="s1",result="error"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="invalid"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="no_mme"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="rejected"} 1
ellamocn_initial_ue_messages_total{interface="s1",result="relayed"} 1
ellamocn_initial_ue_messages_total{interface="s1",result="unknown_plmn"} 2
# HELP ellamocn_ue_connections UE-associated logical S1-connections (TS 36.413 clause 3.1) relayed, by interface (s1) and operator.
# TYPE ellamocn_ue_connections gauge
ellamocn_ue_connections{interface="s1",operator="A"} 1
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(want),
		"ellamocn_initial_ue_messages_total", "ellamocn_ue_connections"); err != nil {
		t.Fatal(err)
	}
}

func expectReleaseCommand(t *testing.T, p *peer, enbID s1ap.ENBUES1APID, group s1ap.CauseGroup, value int) s1ap.MMEUES1APID {
	t.Helper()

	f := p.nextFrame(t)
	if f.stream != 1 {
		t.Fatalf("UE Context Release Command on stream %d, want 1", f.stream)
	}

	m, ok := f.pdu.(*s1ap.InitiatingMessage)
	if !ok || m.ProcedureCode != s1ap.ProcUEContextRelease {
		t.Fatalf("got %s, want UE Context Release Command", procedureName(f.pdu))
	}

	cmd, err := s1ap.ParseUEContextReleaseCommand(m.Value)
	if err != nil {
		t.Fatal(err)
	}

	if !cmd.UES1APIDs.Pair || cmd.UES1APIDs.ENBUES1APID != enbID {
		t.Fatalf("UE Context Release Command IDs = %+v, want the pair with eNB UE S1AP ID %d", cmd.UES1APIDs, enbID)
	}

	if cmd.Cause == nil || cmd.Cause.Group != group || cmd.Cause.Value != value {
		t.Fatalf("UE Context Release Command cause = %v, want %d/%d", cmd.Cause, group, value)
	}

	return cmd.UES1APIDs.MMEUES1APID
}

func TestUnroutedUEReleasedWithoutComplete(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.31")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.31"))
	enb, mme := upAndRunning(t, h, mmeListener)

	msg := initialUE(t, 1)
	msg.TAI.PLMNIdentity = plmnID(t, "999", "99")
	enb.send(t, msg)
	ranMMEID := expectReleaseCommand(t, enb, 1, s1ap.CauseGroupMisc, s1ap.CauseMiscUnknownPLMN)

	expectResetForUnansweredRelease(t, enb, ranMMEID, 1)
	enb.send(t, &s1ap.ResetAcknowledge{ConnectionList: []s1ap.UEAssociatedLogicalS1ConnectionItem{item(ranMMEID, 1)}})

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs = %+v", ues)
	}

	attach(t, enb, mme, 1, 100)
	enb.quiet(t, 200*time.Millisecond)
	mme.quiet(t, 200*time.Millisecond)
}

func expectResetForUnansweredRelease(t *testing.T, enb *peer, ranMMEID s1ap.MMEUES1APID, enbID s1ap.ENBUES1APID) {
	t.Helper()

	f := enb.nextFrame(t)
	if f.stream != 0 {
		t.Fatalf("Reset on stream %d, want 0", f.stream)
	}

	m, ok := f.pdu.(*s1ap.InitiatingMessage)
	if !ok || m.ProcedureCode != s1ap.ProcReset {
		t.Fatalf("got %s, want Reset", procedureName(f.pdu))
	}

	reset, err := s1ap.ParseReset(m.Value)
	if err != nil {
		t.Fatal(err)
	}

	if reset.ResetType.All || len(reset.ResetType.Part) != 1 || reset.ResetType.Part[0].MMEUES1APID == nil ||
		*reset.ResetType.Part[0].MMEUES1APID != ranMMEID || reset.ResetType.Part[0].ENBUES1APID == nil ||
		*reset.ResetType.Part[0].ENBUES1APID != enbID {
		t.Fatalf("Reset = %+v, want a partial reset of %d/%d", reset.ResetType, ranMMEID, enbID)
	}

	if reset.Cause == nil || reset.Cause.Group != s1ap.CauseGroupRadioNetwork ||
		reset.Cause.Value != s1ap.CauseRadioNetworkUnspecified {
		t.Fatalf("Reset cause = %v, want radio network unspecified", reset.Cause)
	}
}

func TestInitialUEMessageWhoseSendToTheMMEFails(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.33")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.33"))
	enb, mme := upAndRunning(t, h, mmeListener)

	h.gw.FailMMESends(errors.New("send failed"))

	enb.send(t, initialUE(t, 1))
	ranMMEID := expectReleaseCommand(t, enb, 1, s1ap.CauseGroupTransport, s1ap.CauseTransportResourceUnavailable)

	if ues := h.gw.UEs(); len(ues) != 0 {
		t.Fatalf("UEs after a failed send = %+v", ues)
	}

	enb.send(t, initialUE(t, 2))
	secondMMEID := expectReleaseCommand(t, enb, 2, s1ap.CauseGroupTransport, s1ap.CauseTransportResourceUnavailable)
	enb.send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: s1ap.Ptr(secondMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(2))})

	expectResetForUnansweredRelease(t, enb, ranMMEID, 1)
	enb.quiet(t, 2500*time.Millisecond)
	mme.quiet(t, 200*time.Millisecond)

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(h.gw.Collectors()...)

	want := `
# HELP ellamocn_initial_ue_messages_total INITIAL UE MESSAGEs received from radios, by interface (s1) and result (relayed: sent to an MME; unknown_plmn: no operator serves the PLMN; no_mme: no selectable MME of the operator; rejected: reuses an allocated eNB UE S1AP ID, an Error Indication is sent; invalid: undecodable or missing a mandatory IE; error: internal failure or failed send to the MME).
# TYPE ellamocn_initial_ue_messages_total counter
ellamocn_initial_ue_messages_total{interface="s1",result="error"} 2
ellamocn_initial_ue_messages_total{interface="s1",result="invalid"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="no_mme"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="rejected"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="relayed"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="unknown_plmn"} 0
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(want), "ellamocn_initial_ue_messages_total"); err != nil {
		t.Fatal(err)
	}
}

func TestInitialUEMessageWithNoSelectableMME(t *testing.T) {
	skipWithoutSCTP(t)

	mmeListener := listenMME(t, "127.0.0.32")
	h := startGateway(t, mmeListener.port(), operatorA("127.0.0.32"))
	enb, mme := upAndRunning(t, h, mmeListener)

	mme.send(t, &s1ap.MMEConfigurationUpdate{RelativeMMECapacity: s1ap.Ptr(uint8(0))})
	expect(t, mme, "successful", s1ap.ProcMMEConfigurationUpdate, s1ap.ParseMMEConfigurationUpdateAcknowledge)

	enb.send(t, initialUE(t, 1))
	ranMMEID := expectReleaseCommand(t, enb, 1, s1ap.CauseGroupTransport, s1ap.CauseTransportResourceUnavailable)
	enb.send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: s1ap.Ptr(ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(1))})
	mme.quiet(t, 200*time.Millisecond)

	msg := initialUE(t, 2)
	msg.STMSI = &s1ap.STMSI{MMEC: 1, MTMSI: 0x01020304}
	enb.send(t, msg)
	expectRaw(t, mme, "initiating", s1ap.ProcInitialUEMessage)

	_ = mme.conn.Abort()

	expect(t, enb, "initiating", s1ap.ProcReset, s1ap.ParseReset)

	enb.send(t, initialUE(t, 3))
	expectReleaseCommand(t, enb, 3, s1ap.CauseGroupTransport, s1ap.CauseTransportResourceUnavailable)

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(h.gw.Collectors()...)

	want := `
# HELP ellamocn_initial_ue_messages_total INITIAL UE MESSAGEs received from radios, by interface (s1) and result (relayed: sent to an MME; unknown_plmn: no operator serves the PLMN; no_mme: no selectable MME of the operator; rejected: reuses an allocated eNB UE S1AP ID, an Error Indication is sent; invalid: undecodable or missing a mandatory IE; error: internal failure or failed send to the MME).
# TYPE ellamocn_initial_ue_messages_total counter
ellamocn_initial_ue_messages_total{interface="s1",result="error"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="invalid"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="no_mme"} 2
ellamocn_initial_ue_messages_total{interface="s1",result="rejected"} 0
ellamocn_initial_ue_messages_total{interface="s1",result="relayed"} 1
ellamocn_initial_ue_messages_total{interface="s1",result="unknown_plmn"} 0
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(want), "ellamocn_initial_ue_messages_total"); err != nil {
		t.Fatal(err)
	}
}
