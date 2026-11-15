package ng_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/assoc"
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

	return ngap.UserLocationInformation{NR: &ngap.UserLocationInformationNR{
		NRCGI: ngap.NRCGI{PLMNIdentity: id, NRCellIdentity: ngap.NRCellIdentity(cell)}, TAI: ngap.TAI{PLMNIdentity: id, TAC: 1},
	}}
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

	gnb.Send(t, init)

	f, raw := expectRaw(t, amf, "initiating", ngap.ProcInitialUEMessage)
	assertRelayed(t, init, f)

	_, amfRANID := ueIDs(t, raw)

	dl := &ngap.DownlinkNASTransport{AMFUENGAPID: amfID, RANUENGAPID: amfRANID, NASPDU: nas}
	amf.Send(t, dl)

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
		UESecurityCapabilities: ngap.UESecurityCapabilities{NREncryptionAlgorithms: ngap.SecurityAlgorithmsFrom(0xe000), NRIntegrityProtectionAlgorithms: ngap.SecurityAlgorithmsFrom(0xe000)},
		SecurityKey:            securityKey(),
		NASPDU:                 &nas,
	}
}

func TestUELifecycleThroughRelay(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	amfs := listenAMF(t, "127.0.1.10")
	h := startGatewayWith(t, ng.Config{CorePort: amfs.Port(), Logger: logger}, operatorA("127.0.1.10"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 7, 1<<39+5)

	ues := h.gw.UEs()
	if len(ues) != 1 {
		t.Fatalf("UEs = %+v, want one", ues)
	}

	got := ues[0]
	if got.RadioRANUENGAPID == nil || *got.RadioRANUENGAPID != 7 || got.RadioAMFUENGAPID != uint64(ue.ranAMFID) ||
		got.AMFRANUENGAPID != uint32(ue.amfRANID) || got.AMFUENGAPID == nil || *got.AMFUENGAPID != 1<<39+5 ||
		got.OperatorName != "A" || got.PLMN != plmnA || got.RadioGlobalGNBID.ID != 1 ||
		len(got.AMFTNLAddresses) != 1 || got.AMFTNLAddresses[0] != "127.0.1.10" ||
		got.TAI == nil || got.TAI.TAC != 1 || got.NRCGI == nil || got.NRCGI.CellID != 0x19b01 || got.Since.IsZero() {
		t.Fatalf("UE = %+v", got)
	}

	ul := &ngap.UplinkNASTransport{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: 7, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 0x19b02))}
	gnb.Send(t, ul)

	f, raw := expectRaw(t, amf, "initiating", ngap.ProcUplinkNASTransport)
	assertRelayed(t, ul, f)

	if a, r := ueIDs(t, raw); a != ue.amfID || r != ue.amfRANID {
		t.Fatalf("Uplink NAS Transport IDs = %d/%d, want %d/%d", a, r, ue.amfID, ue.amfRANID)
	}

	if ues := h.gw.UEs(); ues[0].NRCGI.CellID != 0x19b02 {
		t.Fatalf("last NR CGI = %+v", ues[0].NRCGI)
	}

	ics := contextSetup(t, ue)
	amf.Send(t, ics)

	f, raw = expectRaw(t, gnb, "initiating", ngap.ProcInitialContextSetup)
	assertRelayed(t, ics, withAMFUENGAPID2(t, f, ue.amfID))

	if a, r := ueIDs(t, raw); a != ue.ranAMFID || r != 7 {
		t.Fatalf("Initial Context Setup Request IDs = %d/%d", a, r)
	}

	gnb.Send(t, &ngap.InitialContextSetupResponse{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: new(ngap.RANUENGAPID(7))})
	expectRaw(t, amf, "successful", ngap.ProcInitialContextSetup)

	cmd := &ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{UENGAPIDPair: &ngap.UENGAPIDPair{AMFUENGAPID: ue.amfID, RANUENGAPID: ue.amfRANID}},
		Cause:     new(ngap.CauseNASNormalRelease.Cause()),
	}
	amf.Send(t, cmd)

	f, raw = expectRaw(t, gnb, "initiating", ngap.ProcUEContextRelease)
	assertRelayed(t, cmd, f)

	pair, _, err := raw.UENGAPIDs()
	if err != nil || pair.UENGAPIDPair == nil || pair.UENGAPIDPair.AMFUENGAPID != ue.ranAMFID || pair.UENGAPIDPair.RANUENGAPID != 7 {
		t.Fatalf("UE NGAP IDs = %+v, %v", pair, err)
	}

	complete := &ngap.UEContextReleaseComplete{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: new(ngap.RANUENGAPID(7))}
	gnb.Send(t, complete)

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
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.11")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.11"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 9, 77)

	amf.Send(t, &ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{AMFUENGAPID: new(ngap.AMFUENGAPID(77))},
		Cause:     new(ngap.CauseNASNormalRelease.Cause()),
	})

	_, raw := expectRaw(t, gnb, "initiating", ngap.ProcUEContextRelease)

	ids, _, err := raw.UENGAPIDs()
	if err != nil || ids.AMFUENGAPID == nil || *ids.AMFUENGAPID != ue.ranAMFID {
		t.Fatalf("UE NGAP IDs = %+v, %v", ids, err)
	}
}

func TestUnknownUEIDs(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.12")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.12"))
	gnb, amf := upAndRunning(t, h, amfs)

	gnb.Send(t, &ngap.UplinkNASTransport{AMFUENGAPID: 999, RANUENGAPID: 1, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1))})

	ind := expect(t, gnb, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || !ind.Cause.Equal(ngap.CauseRadioNetworkUnknownLocalUENGAPID.Cause()) {
		t.Fatalf("cause = %v", ind.Cause)
	}

	amf.Send(t, &ngap.DownlinkNASTransport{AMFUENGAPID: 5, RANUENGAPID: 12345, NASPDU: nas})

	ind = expect(t, amf, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || !ind.Cause.Equal(ngap.CauseRadioNetworkUnknownLocalUENGAPID.Cause()) {
		t.Fatalf("cause = %v", ind.Cause)
	}

	ue := attach(t, gnb, amf, 3, 30)

	gnb.Send(t, &ngap.UplinkNASTransport{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: 4, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1))})

	ind = expect(t, gnb, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if ind.Cause == nil || !ind.Cause.Equal(ngap.CauseRadioNetworkInconsistentRemoteUEID.Cause()) {
		t.Fatalf("cause = %v", ind.Cause)
	}

	reset := expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(assoc.Items(reset.ResetType.PartOfNGInterface)) != 1 || *assoc.Items(reset.ResetType.PartOfNGInterface)[0].RANUENGAPID != ue.amfRANID {
		t.Fatalf("NG Reset = %+v", reset)
	}

	h.eventually(t, "the UE context removed", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestErrorIndicationRelayed(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.13")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.13"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	cause := ngap.CauseProtocolSemanticError.Cause()
	gnb.Send(t, &ngap.ErrorIndication{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: &ue.ranID, Cause: &cause})

	ind := expect(t, amf, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if *ind.AMFUENGAPID != ue.amfID || *ind.RANUENGAPID != ue.amfRANID || !ind.Cause.Equal(cause) {
		t.Fatalf("Error Indication = %+v", ind)
	}

	amf.Send(t, &ngap.ErrorIndication{AMFUENGAPID: &ue.amfID, RANUENGAPID: &ue.amfRANID, Cause: &causeUnknownLocal})

	ind = expect(t, gnb, "initiating", ngap.ProcErrorIndication, ngap.ParseErrorIndication)
	if *ind.AMFUENGAPID != ue.ranAMFID || *ind.RANUENGAPID != ue.ranID {
		t.Fatalf("Error Indication = %+v", ind)
	}

	h.eventually(t, "the UE context released", func() bool { return len(h.gw.UEs()) == 0 })
}

var causeUnknownLocal = ngap.CauseRadioNetworkUnknownLocalUENGAPID.Cause()

func TestRadioLossResetsItsUEsTowardTheAMF(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.14")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.14"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	_ = gnb.Conn.Abort()

	reset := expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if reset.ResetType.NGInterface != nil || len(assoc.Items(reset.ResetType.PartOfNGInterface)) != 1 || *assoc.Items(reset.ResetType.PartOfNGInterface)[0].AMFUENGAPID != ue.amfID ||
		*assoc.Items(reset.ResetType.PartOfNGInterface)[0].RANUENGAPID != ue.amfRANID || reset.Cause.Transport == nil {
		t.Fatalf("NG Reset = %+v", reset)
	}

	h.eventually(t, "no UE", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestAMFLossKeepsTheUEsUntilTheirNextMessage(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.15")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.15"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue := attach(t, gnb, amf, 1, 10)

	gnb.Send(t, initialUE(t, 2, "00101"))
	expectRaw(t, amf, "initiating", ngap.ProcInitialUEMessage)

	_ = amf.Conn.Abort()

	expectReset(t, gnb, connection(nil, 2))
	gnb.Quiet(t, 200*time.Millisecond)

	if ues := h.gw.UEs(); len(ues) != 1 || *ues[0].RadioRANUENGAPID != 1 {
		t.Fatalf("UEs = %+v, want the connected one", ues)
	}

	gnb.Send(t, uplinkNAS(t, ue.ranAMFID, ue.ranID))
	expectReset(t, gnb, connection(&ue.ranAMFID, ue.ranID))

	h.eventually(t, "no UE", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestResetTranslation(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.16")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.16"))
	gnb, amf := upAndRunning(t, h, amfs)

	ue1 := attach(t, gnb, amf, 1, 10)
	ue2 := attach(t, gnb, amf, 2, 20)

	cause := ngap.CauseMiscOMIntervention.Cause()
	amf.Send(t, &ngap.NGReset{Cause: &cause, ResetType: ngap.ResetType{PartOfNGInterface: &ngap.UEAssociatedLogicalNGConnectionList{
		{AMFUENGAPID: &ue1.amfID}, {RANUENGAPID: ngap.Ptr(ngap.RANUENGAPID(4242))},
	}}})

	ack := expect(t, amf, "successful", ngap.ProcNGReset, ngap.ParseNGResetAcknowledge)
	if len(ack.ConnectionList) != 2 {
		t.Fatalf("NG Reset Acknowledge = %+v", ack)
	}

	reset := expect(t, gnb, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(assoc.Items(reset.ResetType.PartOfNGInterface)) != 1 || *assoc.Items(reset.ResetType.PartOfNGInterface)[0].AMFUENGAPID != ue1.ranAMFID ||
		*assoc.Items(reset.ResetType.PartOfNGInterface)[0].RANUENGAPID != ue1.ranID || !reset.Cause.Equal(cause) {
		t.Fatalf("NG Reset = %+v", reset)
	}

	gnb.Send(t, &ngap.NGResetAcknowledge{ConnectionList: assoc.Items(reset.ResetType.PartOfNGInterface)})

	gnb.Send(t, &ngap.NGReset{Cause: &cause, ResetType: ngap.ResetType{NGInterface: new(ngap.ResetAllResetAll)}})
	expect(t, gnb, "successful", ngap.ProcNGReset, ngap.ParseNGResetAcknowledge)

	reset = expect(t, amf, "initiating", ngap.ProcNGReset, ngap.ParseNGReset)
	if len(assoc.Items(reset.ResetType.PartOfNGInterface)) != 1 || *assoc.Items(reset.ResetType.PartOfNGInterface)[0].AMFUENGAPID != ue2.amfID ||
		*assoc.Items(reset.ResetType.PartOfNGInterface)[0].RANUENGAPID != ue2.amfRANID {
		t.Fatalf("NG Reset = %+v", reset)
	}

	h.eventually(t, "no UE", func() bool { return len(h.gw.UEs()) == 0 })
}

func TestInitialUEMessageForAnUnknownPLMN(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.17")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.17"))
	gnb, amf := upAndRunning(t, h, amfs)

	gnb.Send(t, initialUE(t, 5, "00199"))

	cmd := expect(t, gnb, "initiating", ngap.ProcUEContextRelease, ngap.ParseUEContextReleaseCommand)
	if cmd.UENGAPIDs.UENGAPIDPair == nil || cmd.UENGAPIDs.UENGAPIDPair.RANUENGAPID != 5 || !cmd.Cause.Equal(ngap.CauseMiscUnknownPLMNOrSNPN.Cause()) {
		t.Fatalf("UE Context Release Command = %+v", cmd)
	}

	gnb.Send(t, &ngap.UEContextReleaseComplete{AMFUENGAPID: &cmd.UENGAPIDs.UENGAPIDPair.AMFUENGAPID, RANUENGAPID: new(ngap.RANUENGAPID(5))})

	amf.Quiet(t, 300*time.Millisecond)
	gnb.Quiet(t, 100*time.Millisecond)
}

func TestNewAMFUENGAPID(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.18")
	h := startGateway(t, amfs.Port(), operatorA("127.0.1.18"))
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

	amf.SendRaw(t, b)

	_, raw := expectRaw(t, gnb, "initiating", ngap.ProcUEContextModification)
	if id, _, err := raw.NewAMFUENGAPID(); err != nil || id != ue.ranAMFID {
		t.Fatalf("New AMF UE NGAP ID toward the radio = %d, %v, want %d", id, err, ue.ranAMFID)
	}

	gnb.Send(t, &ngap.UplinkNASTransport{AMFUENGAPID: ue.ranAMFID, RANUENGAPID: 1, NASPDU: nas, UserLocationInformation: ngap.Ptr(uli(t, "00101", 1))})

	_, raw = expectRaw(t, amf, "initiating", ngap.ProcUplinkNASTransport)
	if a, _ := ueIDs(t, raw); a != 11 {
		t.Fatalf("AMF UE NGAP ID = %d, want the new one", a)
	}

	amf.Send(t, &ngap.DownlinkNASTransport{AMFUENGAPID: 11, RANUENGAPID: ue.amfRANID, NASPDU: nas})
	expectRaw(t, gnb, "initiating", ngap.ProcDownlinkNASTransport)

	if got := h.gw.UEs(); len(got) != 1 || *got[0].AMFUENGAPID != 11 {
		t.Fatalf("UEs = %+v", got)
	}
}

func TestAMFThatStopsReadingDuringABurstKeepsItsAssociation(t *testing.T) {
	t.Parallel()

	skipWithoutSCTP(t)

	const (
		radios   = 8
		perRadio = 500
	)

	amfs := listenAMF(t, "127.0.1.120")
	h := startGatewayWith(t, ng.Config{CorePort: amfs.Port(), ResponseTimeout: time.Minute}, operatorA("127.0.1.120"))
	gnb, amf := upAndRunning(t, h, amfs)
	gnbs := []*peer{gnb}

	for i := 2; i <= radios; i++ {
		n := h.connectGNB(t)
		n.Send(t, setupRequest(t, uint32(i), fmt.Sprintf("gnb%d", i), ngap.PagingDRXv128, ta(t, 1, "00101")))
		expect(t, n, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
		gnbs = append(gnbs, n)
	}

	msgs := make([][]byte, perRadio)
	for i := range msgs {
		b, err := initialUE(t, ngap.RANUENGAPID(i+1), "00101").Marshal()
		if err != nil {
			t.Fatal(err)
		}

		msgs[i] = b
	}

	sent := make(chan error, radios)

	for _, n := range gnbs {
		go func() {
			info := &sctp.SndRcvInfo{Stream: 1, PPID: sctp.PPIDWireOrder(ng.PPID)}

			for _, b := range msgs {
				if _, err := n.Conn.WriteMsgWait(t.Context(), b, info); err != nil {
					sent <- err
					return
				}
			}

			sent <- nil
		}()
	}

	time.Sleep(200 * time.Millisecond)

	for range radios * perRadio {
		expectRaw(t, amf, "initiating", ngap.ProcInitialUEMessage)
	}

	for range radios {
		if err := <-sent; err != nil {
			t.Fatalf("burst from a radio: %v", err)
		}
	}

	amf.Quiet(t, 100*time.Millisecond)

	for _, n := range gnbs {
		n.Quiet(t, 100*time.Millisecond)
	}

	if ues := h.gw.UEs(); len(ues) != radios*perRadio {
		t.Fatalf("UEs = %d, want %d", len(ues), radios*perRadio)
	}
}
