package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func openTracedDB(t *testing.T) (*DB, *tracetest.SpanRecorder, trace.Tracer) {
	t.Helper()

	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "mocn.db"), WithTracerProvider(tp))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	return d, rec, tp.Tracer("test")
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) string {
	set := attribute.NewSet(s.Attributes()...)
	v, _ := set.Value(attribute.Key(key))

	return v.String()
}

func childrenOf(rec *tracetest.SpanRecorder, parent trace.Span) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan

	for _, s := range rec.Ended() {
		if s.Parent().SpanID() == parent.SpanContext().SpanID() {
			out = append(out, s)
		}
	}

	return out
}

func onlyChild(t *testing.T, rec *tracetest.SpanRecorder, parent trace.Span) sdktrace.ReadOnlySpan {
	t.Helper()

	spans := childrenOf(rec, parent)
	if len(spans) != 1 {
		t.Fatalf("%d spans under %v, want 1", len(spans), parent)
	}

	return spans[0]
}

func TestMethodSpans(t *testing.T) {
	d, rec, tracer := openTracedDB(t)

	ctx, write := tracer.Start(t.Context(), "write")

	if _, err := d.CreateUser(ctx, NewUser{Email: "secret@example.com", Role: RoleAdmin, PasswordHash: "hash"}, 10); err != nil {
		t.Fatal(err)
	}

	write.End()

	ctx, read := tracer.Start(t.Context(), "read")

	if _, err := d.GetUser(ctx, "secret@example.com"); err != nil {
		t.Fatal(err)
	}

	read.End()

	ctx, missing := tracer.Start(t.Context(), "missing")

	if _, err := d.GetUser(ctx, "nobody@example.com"); err == nil {
		t.Fatal("found a missing user")
	}

	missing.End()

	for _, c := range []struct {
		parent     trace.Span
		name, pool string
	}{
		{write, "db.CreateUser", poolWrite},
		{read, "db.GetUser", poolRead},
		{missing, "db.GetUser", poolRead},
	} {
		s := onlyChild(t, rec, c.parent)

		if s.Name() != c.name || s.SpanKind() != trace.SpanKindInternal || s.Status().Code != codes.Unset {
			t.Errorf("span %s %s %v, want %s internal unset", s.Name(), s.SpanKind(), s.Status(), c.name)
		}

		if got := spanAttr(s, string(poolKey)); got != c.pool {
			t.Errorf("span %s pool %q, want %q", s.Name(), got, c.pool)
		}

		if s.SpanContext().TraceID() != c.parent.SpanContext().TraceID() {
			t.Errorf("span %s is not in the caller's trace", s.Name())
		}
	}

	for _, s := range rec.Ended() {
		for _, a := range s.Attributes() {
			if strings.HasPrefix(string(a.Key), "db.") || strings.Contains(a.Value.String(), "secret@example.com") ||
				strings.Contains(a.Value.String(), "hash") {
				t.Fatalf("span %s carries %s=%s", s.Name(), a.Key, a.Value.String())
			}
		}
	}
}

func TestFailedMethodSpan(t *testing.T) {
	d, rec, tracer := openTracedDB(t)

	if _, err := d.writer.ExecContext(t.Context(), `DROP TABLE api_tokens`); err != nil {
		t.Fatal(err)
	}

	ctx, parent := tracer.Start(t.Context(), "count")

	if _, err := d.CountAPITokens(ctx, 1); err == nil {
		t.Fatal("counted API tokens without the table")
	}

	parent.End()

	s := onlyChild(t, rec, parent)
	if s.Name() != "db.CountAPITokens" || s.Status().Code != codes.Error || s.Status().Description != "" ||
		spanAttr(s, "error.type") != "1" {
		t.Fatalf("span %s status %v error.type %q", s.Name(), s.Status(), spanAttr(s, "error.type"))
	}
}

func TestNoSpanOutsideATrace(t *testing.T) {
	d, rec, _ := openTracedDB(t)

	if _, err := d.CountUsers(t.Context()); err != nil {
		t.Fatal(err)
	}

	if n := len(rec.Ended()); n != 0 {
		t.Fatalf("a call outside a trace started %d spans", n)
	}
}

func TestNoSpanWithoutTracerProvider(t *testing.T) {
	if _, ok := openTestDB(t).tracer.(noop.Tracer); !ok {
		t.Fatal("a tracer without a tracer provider")
	}
}

func TestDurationExemplar(t *testing.T) {
	d, _, tracer := openTracedDB(t)

	ctx, span := tracer.Start(t.Context(), "count")
	defer span.End()

	if _, err := d.CountUsers(ctx); err != nil {
		t.Fatal(err)
	}

	var m dto.Metric
	if err := d.metrics.duration.WithLabelValues(poolRead).(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}

	exemplars := m.GetHistogram().GetExemplars()
	for _, b := range m.GetHistogram().GetBucket() {
		if e := b.GetExemplar(); e != nil {
			exemplars = append(exemplars, e)
		}
	}

	for _, e := range exemplars {
		for _, l := range e.GetLabel() {
			if l.GetName() == "trace_id" && l.GetValue() == span.SpanContext().TraceID().String() {
				return
			}
		}
	}

	t.Fatal("no exemplar with the caller's trace ID")
}
