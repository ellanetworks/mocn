package ng_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/ng"
)

const waitFor = 5 * time.Second

var (
	plmnA = db.PLMN{MCC: "001", MNC: "01"}
	plmnB = db.PLMN{MCC: "002", MNC: "002"}
	nas   = ngap.NASPDU{0x7e, 0x00, 0x41, 0x79, 0x00, 0x0d, 0x01, 0x00, 0xf1, 0x10, 0xca, 0xfe, 0xba, 0xbe}
	slice = ngap.SliceSupportItem{SNSSAI: ngap.SNSSAI{SST: 1, SD: &ngap.SD{0x01, 0x02, 0x03}}}
)

type frame struct {
	pdu    ngap.PDU
	raw    []byte
	stream uint16
}

type peer struct {
	conn *sctp.SCTPConn
	msgs chan frame
	done chan struct{}
}

func newPeer(t *testing.T, conn *sctp.SCTPConn) *peer {
	t.Helper()

	p := &peer{conn: conn, msgs: make(chan frame, 16), done: make(chan struct{})}

	go func() {
		defer close(p.done)

		buf := make([]byte, sctp.MaxMessageSize)

		for {
			n, info, err := conn.ReadMsg(buf)
			if err != nil {
				return
			}

			if info == nil || sctp.PPIDWireOrder(info.PPID) != ng.PPID {
				continue
			}

			raw := append([]byte{}, buf[:n]...)

			pdu, err := ngap.Unmarshal(raw)
			if err != nil {
				continue
			}

			p.msgs <- frame{pdu: pdu, raw: raw, stream: info.Stream}
		}
	}()

	t.Cleanup(func() { _ = conn.Abort() })

	return p
}

func (p *peer) send(t *testing.T, msg interface{ Marshal() ([]byte, error) }) {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p.sendRaw(t, b)
}

func (p *peer) sendRaw(t *testing.T, b []byte) {
	t.Helper()

	if _, err := p.conn.WriteMsg(b, &sctp.SndRcvInfo{PPID: sctp.PPIDWireOrder(ng.PPID)}); err != nil {
		t.Fatal(err)
	}
}

func (p *peer) next(t *testing.T) ngap.PDU {
	t.Helper()

	return p.nextFrame(t).pdu
}

func (p *peer) nextFrame(t *testing.T) frame {
	t.Helper()

	select {
	case f := <-p.msgs:
		return f
	case <-time.After(waitFor):
		t.Fatal("no NGAP message")
		return frame{}
	}
}

func (p *peer) quiet(t *testing.T, d time.Duration) {
	t.Helper()

	select {
	case f := <-p.msgs:
		t.Fatalf("unexpected NGAP message %s", procedureName(f.pdu))
	case <-time.After(d):
	}
}

func expect[T any](t *testing.T, p *peer, kind string, proc ngap.ProcedureCode, parse func([]byte) (*T, error)) *T {
	t.Helper()

	pdu := p.next(t)

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

	f := p.nextFrame(t)

	raw, err := ngap.ParseRawPDU(f.raw)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[ngap.TriggeringMessage]string{
		ngap.TriggeringInitiatingMessage: "initiating", ngap.TriggeringSuccessfulOutcome: "successful",
		ngap.TriggeringUnsuccessfulOutcome: "unsuccessful",
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

	in, err := ngap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ngap.ParseRawPDU(got.raw)
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

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}})
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("SCTP unavailable: %v", err)
		}

		t.Skipf("SCTP unavailable (sudo modprobe sctp): %v", err)
	}

	_ = ln.Close()
}

type fakeAMF struct {
	ln    *sctp.Listener
	conns chan *peer
}

func listenAMF(t *testing.T, ip string) *fakeAMF {
	t.Helper()

	return listenAMFAt(t, ip, 0)
}

func listenAMFAt(t *testing.T, ip string, port int) *fakeAMF {
	t.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.ParseIP(ip)}}, Port: port})
	if err != nil {
		t.Fatal(err)
	}

	m := &fakeAMF{ln: ln, conns: make(chan *peer, 4)}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			if err := conn.PrepareAccepted(slog.New(slog.DiscardHandler)); err != nil {
				_ = conn.Abort()
				continue
			}

			m.conns <- newPeer(t, conn)
		}
	}()

	t.Cleanup(func() { _ = ln.Close() })

	return m
}

func (m *fakeAMF) port() int {
	return m.ln.Addr().(*sctp.SCTPAddr).Port
}

func (m *fakeAMF) accept(t *testing.T) *peer {
	t.Helper()

	select {
	case p := <-m.conns:
		return p
	case <-time.After(waitFor):
		t.Fatal("no association from the gateway")
		return nil
	}
}

type operators struct {
	mu  sync.Mutex
	ops []db.Operator
}

func (o *operators) set(ops ...db.Operator) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.ops = ops
}

func (o *operators) list(context.Context) ([]db.Operator, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]db.Operator{}, o.ops...), nil
}

type harness struct {
	gw   *ng.Gateway
	stop func()
	addr *sctp.SCTPAddr
	ops  *operators
}

func startGateway(t *testing.T, amfPort int, ops ...db.Operator) *harness {
	t.Helper()

	return startGatewayWith(t, ng.Config{AMFPort: amfPort}, ops...)
}

func startGatewayWith(t *testing.T, cfg ng.Config, ops ...db.Operator) *harness {
	t.Helper()

	var lc sctp.ListenConfig

	ln, err := lc.Listen(t.Context(), &sctp.SCTPAddr{IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}})
	if err != nil {
		t.Fatal(err)
	}

	src := &operators{}
	src.set(ops...)

	cfg.Listener, cfg.SetupWait, cfg.ResponseTimeout, cfg.RetryInterval = ln, time.Second, 2*time.Second, 500*time.Millisecond
	cfg.ReconnectMin, cfg.ReconnectMax = 100*time.Millisecond, 200*time.Millisecond

	if cfg.Logger == nil {
		cfg.Logger = testLogger()
	}

	if cfg.Operators == nil {
		cfg.Operators = src.list
	}

	gw := ng.New(cfg)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- gw.Run(ctx) }()

	stop := func() {
		cancel()

		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}

	var once sync.Once

	t.Cleanup(func() { once.Do(stop) })

	return &harness{gw: gw, stop: func() { once.Do(stop) }, addr: &sctp.SCTPAddr{
		IPAddrs: []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}},
		Port:    ln.Addr().(*sctp.SCTPAddr).Port,
	}, ops: src}
}

func (h *harness) connectGNB(t *testing.T) *peer {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitFor)
	defer cancel()

	conn, err := sctp.Dial(ctx, "sctp", nil, h.addr, sctp.InitMsg{NumOstreams: 2, MaxInstreams: 2})
	if err != nil {
		t.Fatal(err)
	}

	return newPeer(t, conn)
}

func (h *harness) eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(waitFor)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func plmnID(t *testing.T, mcc, mnc string) ngap.PLMNIdentity {
	t.Helper()

	d := []byte(mcc + mnc)
	for i := range d {
		d[i] -= '0'
	}

	id := ngap.PLMNIdentity{d[1]<<4 | d[0], 0xf0 | d[2], d[4]<<4 | d[3]}
	if len(mnc) == 3 {
		id[1] = d[5]<<4 | d[2]
	}

	return id
}

func testLogger() *slog.Logger {
	if os.Getenv("MOCN_TEST_LOG") == "" {
		return slog.New(slog.DiscardHandler)
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
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

	amf := amfs.accept(t)
	req := expect(t, amf, "initiating", ngap.ProcNGSetup, ngap.ParseNGSetupRequest)
	amf.send(t, resp)

	return amf, req
}

func upAndRunning(t *testing.T, h *harness, amfs *fakeAMF) (*peer, *peer) {
	t.Helper()

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101")))

	amf, _ := setupAMF(t, amfs, setupResponse(t, "amf1", 255, "00101", guami(t, "00101", 2, 1, 0)))

	expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	return gnb, amf
}
