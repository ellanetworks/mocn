package s1_test

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type spans struct {
	rec *tracetest.SpanRecorder
}

func tracedGateway(t *testing.T, mmeIP string) (*harness, *fakeMME, spans) {
	t.Helper()

	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec), sdktrace.WithIDGenerator(tracing.IDGenerator()))

	mmeListener := listenMME(t, mmeIP)
	h := startGatewayWith(t, s1.Config{MMEPort: mmeListener.port(), TracerProvider: tp}, operatorA(mmeIP))

	return h, mmeListener, spans{rec: rec}
}

func attr(s sdktrace.ReadOnlySpan, key string) attribute.Value {
	set := attribute.NewSet(s.Attributes()...)
	v, _ := set.Value(attribute.Key(key))

	return v
}

func (sp spans) wait(t *testing.T, name, side, messageType string, match func(sdktrace.ReadOnlySpan) bool) sdktrace.ReadOnlySpan {
	t.Helper()

	deadline := time.Now().Add(waitFor)

	for {
		for _, s := range sp.rec.Ended() {
			if s.Name() == name && attr(s, "ellamocn.side").AsString() == side &&
				attr(s, "s1ap.message_type").AsString() == messageType && (match == nil || match(s)) {
				return s
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf("no %s span on side %s for %s", name, side, messageType)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func (sp spans) child(t *testing.T, parent sdktrace.ReadOnlySpan, side, messageType string) sdktrace.ReadOnlySpan {
	t.Helper()

	s := sp.wait(t, "s1ap/send", side, messageType, func(s sdktrace.ReadOnlySpan) bool {
		return s.Parent().SpanID() == parent.SpanContext().SpanID()
	})

	if s.SpanContext().TraceID() != parent.SpanContext().TraceID() {
		t.Fatalf("send span in trace %s, receive span in %s", s.SpanContext().TraceID(), parent.SpanContext().TraceID())
	}

	return s
}

func assertKind(t *testing.T, s sdktrace.ReadOnlySpan, want trace.SpanKind) {
	t.Helper()

	if s.SpanKind() != want {
		t.Fatalf("%s %s kind = %v, want %v", s.Name(), attr(s, "s1ap.message_type").AsString(), s.SpanKind(), want)
	}
}

func assertError(t *testing.T, s sdktrace.ReadOnlySpan, errType string) {
	t.Helper()

	if s.Status().Code != codes.Error || attr(s, "error.type").AsString() != errType {
		t.Fatalf("%s %s status %v error.type %q, want error %q", s.Name(), attr(s, "s1ap.message_type").AsString(),
			s.Status(), attr(s, "error.type").AsString(), errType)
	}
}

func assertInt(t *testing.T, s sdktrace.ReadOnlySpan, key string, want int64) {
	t.Helper()

	if got := attr(s, key); got.Type() != attribute.INT64 || got.AsInt64() != want {
		t.Fatalf("%s %s %s = %v, want %d", s.Name(), attr(s, "s1ap.message_type").AsString(), key, got.String(), want)
	}
}

func TestS1APSpans(t *testing.T) {
	skipWithoutSCTP(t)

	h, mmeListener, sp := tracedGateway(t, "127.0.0.34")
	enb, mme := upAndRunning(t, h, mmeListener)

	setup := sp.wait(t, "s1ap/receive", "radio", "S1SetupRequest", nil)
	assertKind(t, setup, trace.SpanKindServer)
	assertKind(t, sp.child(t, setup, "radio", "S1SetupResponse"), trace.SpanKindClient)
	assertKind(t, sp.wait(t, "s1ap/send", "core", "S1SetupRequest", nil), trace.SpanKindClient)
	assertKind(t, sp.wait(t, "s1ap/receive", "core", "S1SetupResponse", nil), trace.SpanKindServer)

	ue := attach(t, enb, mme, 7, 1000)

	init := sp.wait(t, "s1ap/receive", "radio", "InitialUEMessage", nil)
	assertKind(t, init, trace.SpanKindConsumer)

	if init.Parent().IsValid() {
		t.Fatalf("receive span has parent %v", init.Parent())
	}

	b, err := initialUE(t, 7).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]string{
		"network.protocol.name": "s1ap", "network.transport": "sctp", "ellamocn.side": "radio", "ellamocn.operator": "A",
	} {
		if got := attr(init, key).AsString(); got != want {
			t.Errorf("receive %s = %q, want %q", key, got, want)
		}
	}

	if !strings.HasPrefix(attr(init, "network.peer.address").AsString(), "127.0.0.1") ||
		!strings.HasPrefix(attr(init, "network.local.address").AsString(), "127.0.0.1:") {
		t.Errorf("receive addresses %q %q", attr(init, "network.peer.address").AsString(),
			attr(init, "network.local.address").AsString())
	}

	assertInt(t, init, "s1ap.message_size", int64(len(b)))
	assertInt(t, init, "s1ap.enb_ue_s1ap_id", 7)

	relayed := sp.child(t, init, "core", "InitialUEMessage")
	assertKind(t, relayed, trace.SpanKindProducer)
	assertInt(t, relayed, "s1ap.enb_ue_s1ap_id", int64(ue.mmeENBID))

	if got := attr(relayed, "ellamocn.operator").AsString(); got != "A" {
		t.Errorf("send operator = %q", got)
	}

	if !strings.HasPrefix(attr(relayed, "network.peer.address").AsString(), "127.0.0.34:") {
		t.Errorf("send peer address = %q", attr(relayed, "network.peer.address").AsString())
	}

	dl := sp.wait(t, "s1ap/receive", "core", "DownlinkNASTransport", nil)
	assertKind(t, dl, trace.SpanKindConsumer)
	assertInt(t, dl, "s1ap.mme_ue_s1ap_id", 1000)
	assertInt(t, dl, "s1ap.enb_ue_s1ap_id", int64(ue.mmeENBID))

	down := sp.child(t, dl, "radio", "DownlinkNASTransport")
	assertInt(t, down, "s1ap.mme_ue_s1ap_id", int64(ue.ranMMEID))
	assertInt(t, down, "s1ap.enb_ue_s1ap_id", 7)

	if got := attr(down, "ellamocn.operator").AsString(); got != "A" {
		t.Errorf("send to radio operator = %q", got)
	}

	mme.send(t, contextSetup(ue, true))
	expectRaw(t, enb, "initiating", s1ap.ProcInitialContextSetup)

	ics := sp.wait(t, "s1ap/receive", "core", "InitialContextSetupRequest", nil)
	assertKind(t, ics, trace.SpanKindServer)
	assertKind(t, sp.child(t, ics, "radio", "InitialContextSetupRequest"), trace.SpanKindClient)

	resp := &s1ap.InitialContextSetupResponse{
		MMEUES1APID: s1ap.Ptr(ue.ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(7)),
		ERABSetup: []s1ap.ERABSetupItemCtxtSURes{{ERABID: 5, TransportLayerAddress: s1ap.TransportLayerAddress{10, 90, 1, 21}, GTPTEID: 1}},
	}
	enb.send(t, resp)
	expectRaw(t, mme, "successful", s1ap.ProcInitialContextSetup)

	icsResp := sp.wait(t, "s1ap/receive", "radio", "InitialContextSetupResponse", nil)
	assertKind(t, icsResp, trace.SpanKindServer)
	assertKind(t, sp.child(t, icsResp, "core", "InitialContextSetupResponse"), trace.SpanKindClient)

	assertNoSecrets(t, sp)
}

func assertNoSecrets(t *testing.T, sp spans) {
	t.Helper()

	key := securityKey()
	secrets := []string{hex.EncodeToString(key[:]), string(key[:]), hex.EncodeToString(nas), string(nas)}

	for _, s := range sp.rec.Ended() {
		values := []string{s.Name(), s.Status().Description}

		for _, a := range s.Attributes() {
			values = append(values, a.Value.String())
		}

		for _, e := range s.Events() {
			values = append(values, e.Name)

			for _, a := range e.Attributes {
				values = append(values, a.Value.String())
			}
		}

		for _, v := range values {
			for _, secret := range secrets {
				if strings.Contains(v, secret) {
					t.Fatalf("span %s carries a Security Key or NAS-PDU: %q", s.Name(), v)
				}
			}
		}
	}
}

func TestS1APSpanErrors(t *testing.T) {
	skipWithoutSCTP(t)

	h, mmeListener, sp := tracedGateway(t, "127.0.0.35")
	enb, mme := upAndRunning(t, h, mmeListener)

	enb.send(t, rawPDU{&s1ap.InitiatingMessage{
		ProcedureCode: s1ap.ProcUplinkNASTransport, Criticality: s1ap.CriticalityIgnore, Value: []byte{0xff},
	}})
	assertError(t, sp.wait(t, "s1ap/receive", "radio", "UplinkNASTransport", nil), "invalid")

	unknown := initialUE(t, 3)
	unknown.TAI.PLMNIdentity = plmnID(t, "009", "09")
	enb.send(t, unknown)
	expectReleaseCommand(t, enb, 3, s1ap.CauseGroupMisc, s1ap.CauseMiscUnknownPLMN)
	assertError(t, sp.wait(t, "s1ap/receive", "radio", "InitialUEMessage", nil), "unknown_plmn")

	enb.send(t, uplinkNAS(t, 4242, 9))
	expectRaw(t, enb, "initiating", s1ap.ProcErrorIndication)
	assertError(t, sp.wait(t, "s1ap/receive", "radio", "UplinkNASTransport", func(s sdktrace.ReadOnlySpan) bool {
		return attr(s, "s1ap.mme_ue_s1ap_id").AsInt64() == 4242
	}), "unknown_ue")

	mme.send(t, paging(t, tai(t, "00101", 9)))
	assertError(t, sp.wait(t, "s1ap/receive", "core", "Paging", nil), "no_radio")

	h.gw.CloseMMESends()
	enb.send(t, initialUE(t, 4))
	expectReleaseCommand(t, enb, 4, s1ap.CauseGroupTransport, s1ap.CauseTransportResourceUnavailable)

	recv := sp.wait(t, "s1ap/receive", "radio", "InitialUEMessage", func(s sdktrace.ReadOnlySpan) bool {
		return attr(s, "s1ap.enb_ue_s1ap_id").AsInt64() == 4
	})
	assertError(t, recv, "error")
	assertError(t, sp.child(t, recv, "core", "InitialUEMessage"), "association_closed")
}

func traceActivation(id s1ap.EUTRANTraceID) *s1ap.TraceActivation {
	return &s1ap.TraceActivation{
		EUTRANTraceID: id, InterfacesToTrace: s1ap.InterfaceToTraceS1MME, TraceDepth: s1ap.TraceDepthMinimum,
		TraceCollectionEntityIPAddress: s1ap.TransportLayerAddress{10, 0, 0, 9},
	}
}

func withTraceActivation(t *testing.T, msg interface{ Marshal() ([]byte, error) }, id s1ap.EUTRANTraceID) rawBytes {
	t.Helper()

	b, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := s1ap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	v, err := per.Marshal(traceActivation(id), per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Insert(p.Len(), s1ap.RawIE{ID: s1ap.IDTraceActivation, Criticality: s1ap.CriticalityIgnore, Value: v}); err != nil {
		t.Fatal(err)
	}

	out, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func deactivateTrace(t *testing.T, mmeID s1ap.MMEUES1APID, enbID s1ap.ENBUES1APID, id s1ap.EUTRANTraceID) rawBytes {
	t.Helper()

	b, err := (&s1ap.TraceStart{MMEUES1APID: mmeID, ENBUES1APID: enbID, TraceActivation: traceActivation(id)}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	pdu, err := s1ap.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}

	im := pdu.(*s1ap.InitiatingMessage)
	im.ProcedureCode = s1ap.ProcDeactivateTrace

	if b, err = s1ap.Marshal(im); err != nil {
		t.Fatal(err)
	}

	p, err := s1ap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	v, err := per.Marshal(&id, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	i := p.Index(s1ap.IDTraceActivation)
	if err := p.Remove(i); err != nil {
		t.Fatal(err)
	}

	if err := p.Insert(i, s1ap.RawIE{ID: s1ap.IDEUTRANTraceID, Criticality: s1ap.CriticalityIgnore, Value: v}); err != nil {
		t.Fatal(err)
	}

	if b, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	return b
}

type rawBytes []byte

func (r rawBytes) Marshal() ([]byte, error) { return r, nil }

func ranTraceID(id s1ap.EUTRANTraceID) trace.TraceID {
	return tracing.RANTraceID(id).OTelTraceID()
}

func TestRANTraceCorrelation(t *testing.T) {
	skipWithoutSCTP(t)

	h, mmeListener, sp := tracedGateway(t, "127.0.0.36")
	enb, mme := upAndRunning(t, h, mmeListener)
	ue := attach(t, enb, mme, 7, 1000)

	first := s1ap.EUTRANTraceID{0x00, 0xf1, 0x10, 0x00, 0x00, 0x01, 0x00, 0x01}
	ics := withTraceActivation(t, contextSetup(ue, true), first)
	mme.send(t, ics)

	f, raw := expectRaw(t, enb, "initiating", s1ap.ProcInitialContextSetup)
	assertRelayed(t, ics, f)

	var got s1ap.TraceActivation
	if ok, err := raw.Get(s1ap.IDTraceActivation, &got); err != nil || !ok || got.EUTRANTraceID != first {
		t.Fatalf("relayed Trace Activation = %+v, %v, %v", got, ok, err)
	}

	recv := sp.wait(t, "s1ap/receive", "core", "InitialContextSetupRequest", nil)
	if recv.SpanContext().TraceID() != ranTraceID(first) {
		t.Fatalf("Initial Context Setup Request trace ID = %s, want %s", recv.SpanContext().TraceID(), ranTraceID(first))
	}

	sp.child(t, recv, "radio", "InitialContextSetupRequest")

	enb.send(t, uplinkNAS(t, ue.ranMMEID, 7))
	expectRaw(t, mme, "initiating", s1ap.ProcUplinkNASTransport)

	ul := sp.wait(t, "s1ap/receive", "radio", "UplinkNASTransport", nil)
	if ul.SpanContext().TraceID() != ranTraceID(first) {
		t.Fatalf("Uplink NAS Transport trace ID = %s, want %s", ul.SpanContext().TraceID(), ranTraceID(first))
	}

	sp.child(t, ul, "core", "UplinkNASTransport")

	mme.send(t, deactivateTrace(t, ue.mmeUEID, ue.mmeENBID, first))
	expectRaw(t, enb, "initiating", s1ap.ProcDeactivateTrace)

	deact := sp.wait(t, "s1ap/receive", "core", "DeactivateTrace", nil)
	if deact.SpanContext().TraceID() != ranTraceID(first) {
		t.Fatalf("Deactivate Trace trace ID = %s, want %s", deact.SpanContext().TraceID(), ranTraceID(first))
	}

	enb.send(t, uplinkNAS(t, ue.ranMMEID, 7))
	expectRaw(t, mme, "initiating", s1ap.ProcUplinkNASTransport)

	after := sp.wait(t, "s1ap/receive", "radio", "UplinkNASTransport", func(s sdktrace.ReadOnlySpan) bool {
		return s.SpanContext().SpanID() != ul.SpanContext().SpanID()
	})
	if tracing.IsRANTraceID(after.SpanContext().TraceID()) {
		t.Fatalf("trace ID %s after Deactivate Trace", after.SpanContext().TraceID())
	}

	second := s1ap.EUTRANTraceID{0x00, 0xf1, 0x10, 0x00, 0x00, 0x02, 0x00, 0x07}
	start := &s1ap.TraceStart{MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID, TraceActivation: traceActivation(second)}
	mme.send(t, start)

	f, _ = expectRaw(t, enb, "initiating", s1ap.ProcTraceStart)
	assertRelayed(t, start, f)

	ts := sp.wait(t, "s1ap/receive", "core", "TraceStart", nil)
	assertKind(t, ts, trace.SpanKindConsumer)
	assertKind(t, sp.child(t, ts, "radio", "TraceStart"), trace.SpanKindProducer)

	if ts.SpanContext().TraceID() != ranTraceID(second) {
		t.Fatalf("Trace Start trace ID = %s, want %s", ts.SpanContext().TraceID(), ranTraceID(second))
	}

	mme.send(t, &s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{MMEUES1APID: ue.mmeUEID, ENBUES1APID: ue.mmeENBID, Pair: true},
		Cause:     &s1ap.Cause{Group: s1ap.CauseGroupNAS, Value: 0},
	})
	expectRaw(t, enb, "initiating", s1ap.ProcUEContextRelease)
	enb.send(t, &s1ap.UEContextReleaseComplete{MMEUES1APID: s1ap.Ptr(ue.ranMMEID), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(7))})
	expectRaw(t, mme, "successful", s1ap.ProcUEContextRelease)

	complete := sp.wait(t, "s1ap/receive", "radio", "UEContextReleaseComplete", nil)
	if complete.SpanContext().TraceID() != ranTraceID(second) {
		t.Fatalf("UE Context Release Complete trace ID = %s, want %s", complete.SpanContext().TraceID(), ranTraceID(second))
	}

	enb.send(t, initialUE(t, 7))
	expectRaw(t, mme, "initiating", s1ap.ProcInitialUEMessage)

	again := sp.wait(t, "s1ap/receive", "radio", "InitialUEMessage", func(s sdktrace.ReadOnlySpan) bool {
		return s.StartTime().After(complete.StartTime())
	})
	if tracing.IsRANTraceID(again.SpanContext().TraceID()) {
		t.Fatalf("trace ID %s after release", again.SpanContext().TraceID())
	}

	enb.send(t, uplinkNAS(t, ue.ranMMEID, 7))
	expectRaw(t, enb, "initiating", s1ap.ProcErrorIndication)

	stale := sp.wait(t, "s1ap/receive", "radio", "UplinkNASTransport", func(s sdktrace.ReadOnlySpan) bool {
		return s.StartTime().After(complete.StartTime())
	})
	if tracing.IsRANTraceID(stale.SpanContext().TraceID()) {
		t.Fatalf("trace ID %s for a released UE", stale.SpanContext().TraceID())
	}

	assertNoSecrets(t, sp)
}
