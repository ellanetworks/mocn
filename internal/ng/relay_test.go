package ng_test

import (
	"bytes"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/mocn/internal/ng"
)

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

func uli(t *testing.T, plmn string, cell uint64) ngap.UserLocationInformation {
	t.Helper()

	id := plmnID(t, plmn[:3], plmn[3:])

	return ngap.UserLocationInformation{
		Kind: ngap.UserLocationNR, PLMNIdentity: id, CellIdentity: cell, TAI: ngap.TAI{PLMNIdentity: id, TAC: 1},
	}
}

func initialUE(t *testing.T, ranID ngap.RANUENGAPID, plmn string) *ngap.InitialUEMessage {
	t.Helper()

	return &ngap.InitialUEMessage{
		RANUENGAPID: ranID, NASPDU: nas, UserLocationInformation: uli(t, plmn, 0x19b01),
		RRCEstablishmentCause: ngap.Ptr(ngap.RRCEstablishmentCause(3)),
	}
}

type attached struct {
	ranID    ngap.RANUENGAPID
	ranAMFID ngap.AMFUENGAPID
	amfRANID ngap.RANUENGAPID
	amfID    ngap.AMFUENGAPID
}

func attachWith(t *testing.T, gnb, amf *peer, init *ngap.InitialUEMessage, amfID ngap.AMFUENGAPID) attached {
	t.Helper()

	gnb.send(t, init)

	f, raw := expectRaw(t, amf, "initiating", ngap.ProcInitialUEMessage)
	assertRelayed(t, init, f)

	_, amfRANID := ueIDs(t, raw)

	dl := &ngap.DownlinkNASTransport{AMFUENGAPID: amfID, RANUENGAPID: amfRANID, NASPDU: nas}
	amf.send(t, dl)

	f, raw = expectRaw(t, gnb, "initiating", ngap.ProcDownlinkNASTransport)
	assertRelayed(t, dl, f)

	ranAMFID, gotRAN := ueIDs(t, raw)
	if gotRAN != init.RANUENGAPID {
		t.Fatalf("RAN UE NGAP ID toward the radio = %d, want %d", gotRAN, init.RANUENGAPID)
	}

	return attached{ranID: init.RANUENGAPID, ranAMFID: ranAMFID, amfRANID: amfRANID, amfID: amfID}
}

func attach(t *testing.T, gnb, amf *peer, ranID ngap.RANUENGAPID, amfID ngap.AMFUENGAPID) attached {
	t.Helper()

	return attachWith(t, gnb, amf, initialUE(t, ranID, "00101"), amfID)
}

func securityKey() ngap.SecurityKey {
	var key ngap.SecurityKey
	for i := range key {
		key[i] = byte(0xa0 + i)
	}

	return key
}

func contextSetup(t *testing.T, ue attached) *ngap.InitialContextSetupRequest {
	t.Helper()

	return &ngap.InitialContextSetupRequest{
		AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID,
		GUAMI:                  guami(t, "00101", 2, 1, 0),
		AllowedNSSAI:           ngap.AllowedNSSAI{{SNSSAI: slice.SNSSAI}},
		UESecurityCapabilities: ngap.UESecurityCapabilities{NREncryptionAlgorithms: 0xe000, NRIntegrityProtectionAlgorithms: 0xe000},
		SecurityKey:            securityKey(),
		NASPDU:                 &nas,
	}
}

func TestUELifecycleThroughRelay(t *testing.T) {
	skipWithoutSCTP(t)

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	amfs := listenAMF(t, "127.0.1.10")
	h := startGatewayWith(t, ng.Config{AMFPort: amfs.port(), Logger: logger}, operatorA("127.0.1.10"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 7, 1<<39+5)

	ues := h.gw.UEs()
	if len(ues) != 1 {
		t.Fatalf("UEs = %+v, want one", ues)
	}

	got := ues[0]
	if got.RadioRANUENGAPID != 7 || got.RadioAMFUENGAPID != uint64(ue.ranAMFID) ||
		got.AMFRANUENGAPID != uint32(ue.amfRANID) || got.AMFUENGAPID == nil || *got.AMFUENGAPID != 1<<39+5 ||
		got.OperatorName != "A" || got.PLMN != plmnA || got.RadioGlobalGNBID.ID != 1 ||
		len(got.AMFTNLAddresses) != 1 || got.AMFTNLAddresses[0] != "127.0.1.10" ||
		got.TAI == nil || got.TAI.TAC != 1 || got.NRCGI == nil || got.NRCGI.CellID != 0x19b01 || got.Since.IsZero() {
		t.Fatalf("UE = %+v", got)
	}

	ul := &ngap.UplinkNASTransport{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: 7, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 0x19b02))}
	gnb.send(t, ul)

	f, raw := expectRaw(t, amf, "initiating", ngap.ProcUplinkNASTransport)
	assertRelayed(t, ul, f)

	if a, r := ueIDs(t, raw); a != ue.amfID || r != ue.amfRANID {
		t.Fatalf("Uplink NAS Transport IDs = %d/%d, want %d/%d", a, r, ue.amfID, ue.amfRANID)
	}

	if ues := h.gw.UEs(); ues[0].NRCGI.CellID != 0x19b02 {
		t.Fatalf("last NR CGI = %+v", ues[0].NRCGI)
	}

	ics := contextSetup(t, ue)
	amf.send(t, ics)

	f, raw = expectRaw(t, gnb, "initiating", ngap.ProcInitialContextSetup)
	assertRelayed(t, ics, f)

	if a, r := ueIDs(t, raw); a != ue.ranAMFID || r != 7 {
		t.Fatalf("Initial Context Setup Request IDs = %d/%d", a, r)
	}

	gnb.send(t, &ngap.InitialContextSetupResponse{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: new(ngap.RANUENGAPID(7))})
	expectRaw(t, amf, "successful", ngap.ProcInitialContextSetup)

	cmd := &ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID, Pair: true},
		Cause:     &ngap.Cause{Group: ngap.CauseGroupNAS, Value: 0},
	}
	amf.send(t, cmd)

	f, raw = expectRaw(t, gnb, "initiating", ngap.ProcUEContextRelease)
	assertRelayed(t, cmd, f)

	pair, _, err := raw.UENGAPIDs()
	if err != nil || !pair.Pair || pair.AMFUENGAPID != ue.ranAMFID || pair.RANUENGAPID != 7 {
		t.Fatalf("UE NGAP IDs = %+v, %v", pair, err)
	}

	complete := &ngap.UEContextReleaseComplete{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: new(ngap.RANUENGAPID(7))}
	gnb.send(t, complete)

	f, _ = expectRaw(t, amf, "successful", ngap.ProcUEContextRelease)
	assertRelayed(t, complete, f)

	h.eventually(t, "the UE context removed", func() bool { return len(h.gw.UEs()) == 0 })

	key := securityKey()
	for _, secret := range []string{hex.EncodeToString(nas), hex.EncodeToString(key[:]), string(nas)} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs contain %q", secret)
		}
	}
}

func TestReleaseCommandWithAMFUEIDOnly(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.11")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.11"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 9, 77)

	amf.send(t, &ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{AMFUENGAPID: 77},
		Cause:     &ngap.Cause{Group: ngap.CauseGroupNAS, Value: 0},
	})

	_, raw := expectRaw(t, gnb, "initiating", ngap.ProcUEContextRelease)

	ids, _, err := raw.UENGAPIDs()
	if err != nil || ids.Pair || ids.AMFUENGAPID != ue.ranAMFID {
		t.Fatalf("UE NGAP IDs = %+v, %v", ids, err)
	}
}

func TestUnknownUEIDs(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.12")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.12"))
	gnb, amf := upAndRunning(t, h, amfs)

	gnb.send(t, &ngap.UplinkNASTransport{AMFUENGAPID: 999, RANUENGAPID: 1, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1))})

	ind := expect(t, gnb, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || ind.Cause.Value != ngap.CauseRadioNetworkUnknownLocalUENGAPID {
		t.Fatalf("cause = %v", ind.Cause)
	}

	amf.send(t, &ngap.DownlinkNASTransport{AMFUENGAPID: 5, RANUENGAPID: 12345, NASPDU: nas})

	ind = expect(t, amf, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || ind.Cause.Value != ngap.CauseRadioNetworkUnknownLocalUENGAPID {
		t.Fatalf("cause = %v", ind.Cause)
	}

	ue := attach(t, gnb, amf, 3, 30)

	gnb.send(t, &ngap.UplinkNASTransport{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: 4, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1))})

	ind = expect(t, gnb, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || ind.Cause.Value != ngap.CauseRadioNetworkInconsistentRemoteUEID {
		t.Fatalf("cause = %v", ind.Cause)
	}

	reset := expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].RANUENGAPID != ue.amfRANID {
		t.Fatalf("NG Reset = %+v", reset)
	}

	h.eventually(t, "the UE context removed", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestErrorIndicationRelayed(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.13")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.13"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	cause := ngap.Cause{Group: ngap.CauseGroupProtocol, Value: ngap.CauseProtocolSemanticError}
	gnb.send(t, &ngap.ErrorIndication{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID, Cause: &cause})

	ind := expect(t, amf, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if *ind.AMFUENGAPID != ue.amfID || *ind.RANUENGAPID != ue.amfRANID || ind.Cause.Value != cause.Value {
		t.Fatalf("Error Indication = %+v", ind)
	}

	amf.send(t, &ngap.ErrorIndication{AMFUENGAPID: &ue.amfID, RANUENGAPID: &ue.amfRANID, Cause: &causeUnknownLocal})

	ind = expect(t, gnb, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if *ind.AMFUENGAPID != ue.ranAMFID || *ind.RANUENGAPID != ue.ranID {
		t.Fatalf("Error Indication = %+v", ind)
	}

	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
}

var causeUnknownLocal = ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkUnknownLocalUENGAPID}

func TestRadioLossResetsItsUEsTowardTheAMF(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.14")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.14"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	_ = gnb.conn.Abort()

	reset := expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if reset.ResetType.All || len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].AMFUENGAPID != ue.amfID ||
		*reset.ResetType.Part[0].RANUENGAPID != ue.amfRANID || reset.Cause.Group != ngap.CauseGroupTransport {
		t.Fatalf("NG Reset = %+v", reset)
	}

	h.eventually(t, "no UE", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestAMFLossResetsItsUEsTowardTheRadios(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.15")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.15"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	_ = amf.conn.Abort()

	reset := expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if reset.ResetType.All || len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].AMFUENGAPID != ue.ranAMFID ||
		*reset.ResetType.Part[0].RANUENGAPID != ue.ranID {
		t.Fatalf("NG Reset = %+v", reset)
	}

	h.eventually(t, "no UE", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestResetTranslation(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.16")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.16"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue1 := attach(t, gnb, amf, 1, 10)
	ue2 := attach(t, gnb, amf, 2, 20)

	cause := ngap.Cause{Group: ngap.CauseGroupMisc, Value: ngap.CauseMiscOMIntervention}
	amf.send(t, &ngap.NGReset{Cause: &cause, ResetType: ngap.ResetType{Part: ngap.UEAssociatedLogicalNGConnectionList{
		{AMFUENGAPID: &ue1.amfID}, {RANUENGAPID: ngap.Ptr(ngap.RANUENGAPID(4242))},
	}}})

	ack := expect(t, amf, "successful", ngap.ProcNGReset, ngap.ParseNGResetAcknowledge)
	if len(ack.ConnectionList) != 2 {
		t.Fatalf("NG Reset Acknowledge = %+v", ack)
	}

	reset := expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].AMFUENGAPID != ue1.ranAMFID ||
		*reset.ResetType.Part[0].RANUENGAPID != ue1.ranID || reset.Cause.Value != cause.Value {
		t.Fatalf("NG Reset = %+v", reset)
	}

	gnb.send(t, &ngap.NGResetAcknowledge{ConnectionList: reset.ResetType.Part})

	gnb.send(t, &ngap.NGReset{Cause: &cause, ResetType: ngap.ResetType{All: true}})
	expect(t, gnb, "successful", ngap.ProcNGReset, ngap.ParseNGResetAcknowledge)

	reset = expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(reset.ResetType.Part) != 1 || *reset.ResetType.Part[0].AMFUENGAPID != ue2.amfID ||
		*reset.ResetType.Part[0].RANUENGAPID != ue2.amfRANID {
		t.Fatalf("NG Reset = %+v", reset)
	}

	h.eventually(t, "no UE", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestInitialUEMessageForAnUnknownPLMN(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.17")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.17"))
	gnb, amf := upAndRunning(t, h, amfs)

	gnb.send(t, initialUE(t, 5, "00199"))

	cmd := expect(t, gnb, "initiating", ngap.ProcUEContextRelease, ngap.ParseUEContextReleaseCommand)
	if !cmd.UENGAPIDs.Pair || cmd.UENGAPIDs.RANUENGAPID != 5 || cmd.Cause.Value != ngap.CauseMiscUnknownPLMNOrSNPN {
		t.Fatalf("UE Context Release Command = %+v", cmd)
	}

	gnb.send(t, &ngap.UEContextReleaseComplete{AMFUENGAPID: &cmd.UENGAPIDs.AMFUENGAPID, RANUENGAPID: new(ngap.RANUENGAPID(5))})

	amf.quiet(t, 300*time.Millisecond)
	gnb.quiet(t, 100*time.Millisecond)
}

func TestNewAMFUENGAPID(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.18")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.18"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	b, err := (&ngap.UEContextModificationRequest{AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := ngap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	value, err := per.Marshal(ngap.AMFUENGAPID(11), per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Insert(p.Len(), ngap.RawIE{ID: ngap.IDNewAMFUENGAPID, Criticality: ngap.CriticalityReject, Value: value}); err != nil {
		t.Fatal(err)
	}

	if b, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	amf.sendRaw(t, b)

	_, raw := expectRaw(t, gnb, "initiating", ngap.ProcUEContextModification)
	if id, _, err := raw.NewAMFUENGAPID(); err != nil || id != ue.ranAMFID {
		t.Fatalf("New AMF UE NGAP ID toward the radio = %d, %v, want %d", id, err, ue.ranAMFID)
	}

	gnb.send(t, &ngap.UplinkNASTransport{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: 1, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1))})

	_, raw = expectRaw(t, amf, "initiating", ngap.ProcUplinkNASTransport)
	if a, _ := ueIDs(t, raw); a != 11 {
		t.Fatalf("AMF UE NGAP ID = %d, want the new one", a)
	}

	amf.send(t, &ngap.DownlinkNASTransport{AMFUENGAPID: 11, RANUENGAPID: ue.amfRANID, NASPDU: nas})
	expectRaw(t, gnb, "initiating", ngap.ProcDownlinkNASTransport)

	if got := h.gw.UEs(); len(got) != 1 || *got[0].AMFUENGAPID != 11 {
		t.Fatalf("UEs = %+v", got)
	}
}
