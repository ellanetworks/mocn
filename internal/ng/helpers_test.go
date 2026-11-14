package ng_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/sctptest"
)

var (
	plmnA = db.PLMN{MCC: "001", MNC: "01"}
	plmnB = db.PLMN{MCC: "002", MNC: "002"}
	nas   = ngap.NASPDU{0x7e, 0x00, 0x41, 0x79, 0x00, 0x0d, 0x01, 0x00, 0xf1, 0x10, 0xca, 0xfe, 0xba, 0xbe}
	slice = ngap.SliceSupportItem{SNSSAI: ngap.SNSSAI{SST: 1, SD: &ngap.SD{0x01, 0x02, 0x03}}}
)

type (
	frame     = sctptest.Frame[ngap.PDU]
	peer      = sctptest.Peer[ngap.PDU]
	fakeAMF   = sctptest.Core[ngap.PDU]
	operators = sctptest.Operators
)

var codec = sctptest.Codec[ngap.PDU]{Protocol: "NGAP", PPID: ng.PPID, Unmarshal: ngap.Unmarshal, Name: procedureName}

func expect[T any](t *testing.T, p *peer, kind string, proc ngap.ProcedureCode, parse func([]byte) (*T, error)) *T {
	t.Helper()

	pdu := p.Next(t)

	var (
		value []byte
		code  ngap.ProcedureCode
		got   string
	)

	switch m := pdu.(type) {
	case *ngap.InitiatingMessage:
		value, code, got = m.Value, m.ProcedureCode, "initiating"
	case *ngap.SuccessfulOutcome:
		value, code, got = m.Value, m.ProcedureCode, "successful"
	case *ngap.UnsuccessfulOutcome:
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

func expectRaw(t *testing.T, p *peer, kind string, proc ngap.ProcedureCode) (frame, *ngap.RawPDU) {
	t.Helper()

	f := p.NextFrame(t)

	raw, err := ngap.ParseRawPDU(f.Raw)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[ngap.TriggeringMessage]string{
		ngap.TriggeringInitiatingMessage: "initiating", ngap.TriggeringSuccessfulOutcome: "successful",
		ngap.TriggeringUnsuccessfulOutcome: "unsuccessful",
	}

	if kinds[raw.Trigger()] != kind || raw.ProcedureCode() != proc {
		t.Fatalf("got %s, want %s %s", procedureName(f.PDU), kind, proc)
	}

	return f, raw
}

func withAMFUENGAPID2(t *testing.T, got frame, want ngap.AMFUENGAPID) frame {
	t.Helper()

	p, err := ngap.ParseRawPDU(got.Raw)
	if err != nil {
		t.Fatal(err)
	}

	i := p.Index(ngap.IDAMFUENGAPID2)
	if i != p.Len()-1 {
		t.Fatalf("AMF UE NGAP ID 2 at %d of %d IEs, want last", i, p.Len())
	}

	var id ngap.AMFUENGAPID
	if ok, err := p.Get(ngap.IDAMFUENGAPID2, &id); err != nil || !ok || id != want {
		t.Fatalf("AMF UE NGAP ID 2 = %d (%v, %v), want %d", id, ok, err, want)
	}

	if ie, _ := p.IE(i); ie.Criticality != ngap.CriticalityIgnore {
		t.Fatalf("AMF UE NGAP ID 2 criticality %s, want ignore", ie.Criticality)
	}

	if err := p.Remove(i); err != nil {
		t.Fatal(err)
	}

	if got.Raw, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	return got
}

func assertRelayed(t *testing.T, sent interface{ Marshal() ([]byte, error) }, got frame) {
	t.Helper()

	b, err := sent.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	in, err := ngap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ngap.ParseRawPDU(got.Raw)
	if err != nil {
		t.Fatal(err)
	}

	if got.Stream != 1 {
		t.Fatalf("UE-associated message on stream %d, want 1", got.Stream)
	}

	if in.Trigger() != out.Trigger() || in.ProcedureCode() != out.ProcedureCode() || in.Criticality() != out.Criticality() {
		t.Fatalf("relayed %s, sent %s", out.ProcedureCode(), in.ProcedureCode())
	}

	a, z := in.IEs(), out.IEs()
	if len(a) != len(z) {
		t.Fatalf("relayed %d IEs, sent %d", len(z), len(a))
	}

	rewritten := []ngap.ProtocolIEID{ngap.IDAMFUENGAPID, ngap.IDRANUENGAPID, ngap.IDUENGAPIDs}

	for i := range a {
		if a[i].ID != z[i].ID || a[i].Criticality != z[i].Criticality {
			t.Fatalf("IE %d: relayed %s, sent %s", i, z[i].ID, a[i].ID)
		}

		if contains(rewritten, a[i].ID) {
			continue
		}

		if !bytes.Equal(a[i].Value, z[i].Value) {
			t.Fatalf("IE %s changed: %x, sent %x", a[i].ID, z[i].Value, a[i].Value)
		}
	}
}

func contains(ids []ngap.ProtocolIEID, id ngap.ProtocolIEID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}

	return false
}

func ueIDs(t *testing.T, p *ngap.RawPDU) (ngap.AMFUENGAPID, ngap.RANUENGAPID) {
	t.Helper()

	amf, _, err := p.AMFUENGAPID()
	if err != nil {
		t.Fatal(err)
	}

	ran, _, err := p.RANUENGAPID()
	if err != nil {
		t.Fatal(err)
	}

	return amf, ran
}

func skipWithoutSCTP(t *testing.T) {
	t.Helper()

	sctptest.SkipWithoutSCTP(t)
}

func listenAMF(t *testing.T, ip string) *fakeAMF {
	t.Helper()

	return sctptest.ListenCore(t, codec, ip, 0)
}

func listenAMFAt(t *testing.T, ip string, port int) *fakeAMF {
	t.Helper()

	return sctptest.ListenCore(t, codec, ip, port)
}

type harness struct {
	*sctptest.Gateway
	gw      *ng.Gateway
	metrics *metrics.Metrics
}

func startGateway(t *testing.T, amfPort int, ops ...db.Operator) *harness {
	t.Helper()

	return startGatewayWith(t, ng.Config{CorePort: amfPort}, ops...)
}

func startGatewayWith(t *testing.T, cfg ng.Config, ops ...db.Operator) *harness {
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

	gw := ng.New(cfg)

	return &harness{Gateway: sctptest.Start(t, ln, src, gw.Run), gw: gw, metrics: cfg.Metrics}
}

func (h *harness) connectGNB(t *testing.T) *peer {
	t.Helper()

	return sctptest.Dial(t, codec, h.Addr)
}

func (h *harness) connectGNBFrom(t *testing.T, ip string) *peer {
	t.Helper()

	return sctptest.DialFrom(t, codec, ip, h.Addr)
}

func (h *harness) eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	sctptest.Eventually(t, what, ok)
}

func plmnID(t *testing.T, mcc, mnc string) ngap.PLMNIdentity {
	t.Helper()

	return sctptest.PLMNID[ngap.PLMNIdentity](t, mcc, mnc)
}

func procedureName(pdu ngap.PDU) string {
	switch m := pdu.(type) {
	case *ngap.InitiatingMessage:
		return "initiating " + m.ProcedureCode.String()
	case *ngap.SuccessfulOutcome:
		return "successful " + m.ProcedureCode.String()
	case *ngap.UnsuccessfulOutcome:
		return "unsuccessful " + m.ProcedureCode.String()
	}

	return "unknown"
}

func operatorA(amfs ...string) db.Operator {
	o := db.Operator{
		ID: 1, Name: "A", PLMNs: []db.PLMN{plmnA},
		GlobalGNBID: &db.GlobalGNBID{PLMN: plmnA, ID: 0xabcde, Length: 22},
	}

	for _, m := range amfs {
		o.AMFs = append(o.AMFs, db.CoreNode{TNLAddresses: []string{m}})
	}

	return o
}

func operatorB(amfs ...string) db.Operator {
	o := db.Operator{
		ID: 2, Name: "B", PLMNs: []db.PLMN{plmnB},
		GlobalGNBID: &db.GlobalGNBID{PLMN: plmnB, ID: 7, Length: 32},
	}

	for _, m := range amfs {
		o.AMFs = append(o.AMFs, db.CoreNode{TNLAddresses: []string{m}})
	}

	return o
}

func ta(t *testing.T, tac uint32, plmns ...string) ngap.SupportedTAItem {
	t.Helper()

	item := ngap.SupportedTAItem{TAC: ngap.TAC(tac)}
	for _, p := range plmns {
		item.BroadcastPLMNList = append(item.BroadcastPLMNList, ngap.BroadcastPLMNItem{
			PLMNIdentity: plmnID(t, p[:3], p[3:]), TAISliceSupportList: ngap.SliceSupportList{slice},
		})
	}

	return item
}

func setupRequest(t *testing.T, gnbID uint32, name string, drx ngap.PagingDRX, tas ...ngap.SupportedTAItem) *ngap.NGSetupRequest {
	t.Helper()

	return &ngap.NGSetupRequest{
		GlobalRANNodeID: ngap.GlobalRANNodeID{Kind: ngap.RANNodeIDGNB, PLMNIdentity: plmnID(t, "001", "01"), Value: gnbID, Bits: 24},
		RANNodeName:     &name, SupportedTAList: tas, DefaultPagingDRX: &drx,
	}
}

func guami(t *testing.T, plmn string, region uint8, set uint16, pointer uint8) ngap.GUAMI {
	t.Helper()

	return ngap.GUAMI{
		PLMNIdentity: plmnID(t, plmn[:3], plmn[3:]), AMFRegionID: ngap.AMFRegionID(region), AMFSetID: ngap.AMFSetID(set),
		AMFPointer: ngap.AMFPointer(pointer),
	}
}

func setupResponse(t *testing.T, name string, capacity uint8, plmn string, guamis ...ngap.GUAMI) *ngap.NGSetupResponse {
	t.Helper()

	resp := &ngap.NGSetupResponse{
		AMFName: name, RelativeAMFCapacity: &capacity,
		PLMNSupportList: ngap.PLMNSupportList{{PLMNIdentity: plmnID(t, plmn[:3], plmn[3:]), SliceSupportList: ngap.SliceSupportList{slice}}},
	}

	for _, g := range guamis {
		resp.ServedGUAMIList = append(resp.ServedGUAMIList, ngap.ServedGUAMIItem{GUAMI: g})
	}

	return resp
}

func setupAMF(t *testing.T, amfs *fakeAMF, resp *ngap.NGSetupResponse) (*peer, *ngap.NGSetupRequest) {
	t.Helper()

	amf := amfs.Accept(t)
	req := expect(t, amf, "initiating", ngap.ProcNGSetup, ngap.ParseNGSetupRequest)
	amf.Send(t, resp)

	return amf, req
}

func upAndRunning(t *testing.T, h *harness, amfs *fakeAMF) (*peer, *peer) {
	t.Helper()

	gnb := h.connectGNB(t)
	gnb.Send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	amf, _ := setupAMF(t, amfs, setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0)))

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	return gnb, amf
}
