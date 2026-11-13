package ng_test

import (
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func attr(s sdktrace.ReadOnlySpan, key string) attribute.Value {
	set := attribute.NewSet(s.Attributes()...)
	v, _ := set.Value(attribute.Key(key))

	return v
}

func waitSpan(t *testing.T, rec *tracetest.SpanRecorder, name, side, messageType string) sdktrace.ReadOnlySpan {
	t.Helper()

	deadline := time.Now().Add(waitFor)

	for {
		for _, s := range rec.Ended() {
			if s.Name() == name && attr(s, "ellamocn.side").AsString() == side &&
				attr(s, "ngap.message_type").AsString() == messageType {
				return s
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf("no %s span on side %s for %s", name, side, messageType)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestNGAPSpans(t *testing.T) {
	skipWithoutSCTP(t)

	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec), sdktrace.WithIDGenerator(tracing.IDGenerator()))

	amfs := listenAMF(t, "127.0.1.40")
	h := startGatewayWith(t, ng.Config{AMFPort: amfs.port(), TracerProvider: tp}, operatorA("127.0.1.40"))
	gnb, amf := upAndRunning(t, h, amfs)

	for _, s := range []struct {
		name, side, message string
		kind                trace.SpanKind
	}{
		{"ngap/receive", "radio", "NGSetupRequest", trace.SpanKindServer},
		{"ngap/send", "radio", "NGSetupResponse", trace.SpanKindClient},
		{"ngap/send", "core", "NGSetupRequest", trace.SpanKindClient},
		{"ngap/receive", "core", "NGSetupResponse", trace.SpanKindServer},
	} {
		if got := waitSpan(t, rec, s.name, s.side, s.message); got.SpanKind() != s.kind {
			t.Fatalf("%s %s kind = %v", s.name, s.message, got.SpanKind())
		}
	}

	ue := attach(t, gnb, amf, 7, 1000)

	init := waitSpan(t, rec, "ngap/receive", "radio", "InitialUEMessage")
	if init.SpanKind() != trace.SpanKindConsumer || attr(init, "network.protocol.name").AsString() != "ngap" ||
		attr(init, "ellamocn.operator").AsString() != "A" || attr(init, "ngap.ran_ue_ngap_id").AsInt64() != 7 {
		t.Fatalf("receive span attributes = %v", init.Attributes())
	}

	sent := waitSpan(t, rec, "ngap/send", "core", "InitialUEMessage")
	if sent.Parent().SpanID() != init.SpanContext().SpanID() ||
		attr(sent, "ngap.ran_ue_ngap_id").AsInt64() != int64(ue.amfRANID) {
		t.Fatalf("send span = %v", sent.Attributes())
	}

	dl := waitSpan(t, rec, "ngap/send", "radio", "DownlinkNASTransport")
	if attr(dl, "ngap.amf_ue_ngap_id").AsInt64() != int64(ue.ranAMFID) || attr(dl, "ellamocn.operator").AsString() != "A" {
		t.Fatalf("send span = %v", dl.Attributes())
	}

	b, err := contextSetup(t, ue).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := ngap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	value, err := per.Marshal(ngap.TraceActivation{
		NGRANTraceID: ngap.NGRANTraceID{1, 2, 3, 4, 5, 6, 0, 1}, InterfacesToTrace: 0x80,
		TraceCollectionEntityIPAddress: ngap.TransportLayerAddress{10, 0, 0, 1},
	}, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Insert(p.Len(), ngap.RawIE{ID: ngap.IDTraceActivation, Criticality: ngap.CriticalityIgnore, Value: value}); err != nil {
		t.Fatal(err)
	}

	if b, err = p.Marshal(); err != nil {
		t.Fatal(err)
	}

	amf.sendRaw(t, b)
	expectRaw(t, gnb, "initiating", ngap.ProcInitialContextSetup)

	gnb.send(t, &ngap.InitialContextSetupResponse{AMFUENGAPID: &ue.ranAMFID, RANUENGAPID: new(ngap.RANUENGAPID(7))})
	expectRaw(t, amf, "successful", ngap.ProcInitialContextSetup)

	resp := waitSpan(t, rec, "ngap/receive", "radio", "InitialContextSetupResponse")
	if want := (tracing.RANTraceID{1, 2, 3, 4, 5, 6, 0, 1}).OTelTraceID(); resp.SpanContext().TraceID() != want {
		t.Fatalf("trace ID = %s, want %s", resp.SpanContext().TraceID(), want)
	}
}
