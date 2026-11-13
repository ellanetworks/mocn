package db

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func openTracedDB(t *testing.T) (*DB, *tracetest.SpanRecorder) {
	t.Helper()

	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "mocn.db"), WithTracerProvider(tp))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	return d, rec
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

func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	i := slices.IndexFunc(spans, func(s sdktrace.ReadOnlySpan) bool { return s.Name() == name })
	if i < 0 {
		names := make([]string, 0, len(spans))
		for _, s := range spans {
			names = append(names, s.Name())
		}

		t.Fatalf("no span %q in %q", name, names)
	}

	return spans[i]
}

func assertStatementSpan(t *testing.T, s sdktrace.ReadOnlySpan, operation, table string) {
	t.Helper()

	if s.SpanKind() != trace.SpanKindClient || s.Status().Code == codes.Error {
		t.Fatalf("span %s kind %v status %v", s.Name(), s.SpanKind(), s.Status())
	}

	want := map[string]string{"db.system.name": "sqlite", "db.operation.name": operation, "db.collection.name": table}
	for key, v := range want {
		if got := spanAttr(s, key); got != v {
			t.Errorf("span %s %s = %q, want %q", s.Name(), key, got, v)
		}
	}

	for _, a := range s.Attributes() {
		if strings.HasPrefix(string(a.Key), "db.query.") {
			t.Errorf("span %s carries %s", s.Name(), a.Key)
		}
	}
}

func TestStatementSpans(t *testing.T) {
	d, rec := openTracedDB(t)
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test")

	ctx, write := tracer.Start(context.Background(), "write")

	if _, err := d.CreateUser(ctx, NewUser{Email: "secret@example.com", Role: RoleAdmin, PasswordHash: "hash"}, 10); err != nil {
		t.Fatal(err)
	}

	write.End()

	var names []string

	for _, s := range childrenOf(rec, write) {
		names = append(names, s.Name())
	}

	if !slices.Equal(names, []string{"SELECT users", "INSERT users"}) {
		t.Fatalf("spans of a write = %q, want SELECT users and INSERT users only", names)
	}

	insert := findSpan(t, childrenOf(rec, write), "INSERT users")
	assertStatementSpan(t, insert, "INSERT", "users")

	if insert.SpanContext().TraceID() != write.SpanContext().TraceID() {
		t.Fatal("INSERT users is not in the caller's trace")
	}

	for _, s := range rec.Ended() {
		for _, a := range s.Attributes() {
			if strings.Contains(a.Value.String(), "secret@example.com") || strings.Contains(a.Value.String(), "hash") {
				t.Fatalf("span %s carries a parameter: %s=%s", s.Name(), a.Key, a.Value.String())
			}
		}
	}

	ctx, read := tracer.Start(context.Background(), "read")

	if _, err := d.GetUser(ctx, "secret@example.com"); err != nil {
		t.Fatal(err)
	}

	read.End()

	reads := childrenOf(rec, read)
	if len(reads) != 1 {
		t.Fatalf("%d spans for one SELECT", len(reads))
	}

	assertStatementSpan(t, reads[0], "SELECT", "users")

	if reads[0].Name() != "SELECT users" {
		t.Fatalf("span name %q", reads[0].Name())
	}

	ctx, missing := tracer.Start(context.Background(), "missing")

	if _, err := d.GetUser(ctx, "nobody@example.com"); err == nil {
		t.Fatal("found a missing user")
	}

	missing.End()

	assertStatementSpan(t, findSpan(t, childrenOf(rec, missing), "SELECT users"), "SELECT", "users")
}

func TestFailedStatementSpan(t *testing.T) {
	d, rec := openTracedDB(t)

	if _, err := d.writer.ExecContext(context.Background(), `DROP TABLE api_tokens`); err != nil {
		t.Fatal(err)
	}

	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test")
	ctx, parent := tracer.Start(context.Background(), "count")

	if _, err := d.CountAPITokens(ctx, 1); err == nil {
		t.Fatal("counted API tokens without the table")
	}

	parent.End()

	s := findSpan(t, childrenOf(rec, parent), "SELECT api_tokens")
	if s.Status().Code != codes.Error || spanAttr(s, "error.type") != "1" || spanAttr(s, "db.response.status_code") != "1" {
		t.Fatalf("status %v error.type %q db.response.status_code %q", s.Status(), spanAttr(s, "error.type"),
			spanAttr(s, "db.response.status_code"))
	}
}

func TestNoStatementSpansWithoutTracerProvider(t *testing.T) {
	d := openTestDB(t)

	if d.tracer != nil {
		t.Fatal("tracer without a tracer provider")
	}

	if _, err := d.CountUsers(context.Background()); err != nil {
		t.Fatal(err)
	}
}
