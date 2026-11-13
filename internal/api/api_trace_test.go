package api_test

import (
	"net/http"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/ellanetworks/mocn/internal/api"
	"github.com/ellanetworks/mocn/internal/db"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestAPISpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	cfg := testConfig(t, openStore(t, db.WithTracerProvider(tp)))
	cfg.Frontend = fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	cfg.TracerProvider = tp
	h := api.NewHandler(cfg)
	token := initialize(t, h)

	for _, path := range []string{"/", "/api/v1/livez", "/api/v1/pprof/heap"} {
		serve(h, request(http.MethodGet, path, "", ""))
	}

	req := request(http.MethodGet, "/api/v1/users/"+testAdmin, "", token)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")

	if res := serve(h, req); res.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/users/%s = %d %s", testAdmin, res.Code, res.Body)
	}

	var spans, dbSpans []sdktrace.ReadOnlySpan

	for _, s := range rec.Ended() {
		if s.SpanKind() == trace.SpanKindServer {
			spans = append(spans, s)
		} else {
			dbSpans = append(dbSpans, s)
		}
	}

	if len(spans) != 2 || spans[0].Name() != "POST /api/v1/init" {
		names := make([]string, 0, len(spans))
		for _, s := range spans {
			names = append(names, s.Name())
		}

		t.Fatalf("spans %q, want POST /api/v1/init and the user's", names)
	}

	s := spans[1]
	if s.Name() != "GET /api/v1/users/{email}" || s.SpanKind() != trace.SpanKindServer || s.Parent().IsValid() {
		t.Fatalf("span %q kind %v parent %v, want a server span without a parent", s.Name(), s.SpanKind(), s.Parent())
	}

	if links := s.Links(); len(links) != 1 || links[0].SpanContext.TraceID().String() != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("links = %+v, want one to the caller's trace context", links)
	}

	attrs := attribute.NewSet(s.Attributes()...)
	if route, _ := attrs.Value("http.route"); route.AsString() != "/api/v1/users/{email}" {
		t.Errorf("http.route = %q", route.AsString())
	}

	var children []string

	for _, d := range dbSpans {
		if d.Parent().SpanID() == s.SpanContext().SpanID() {
			children = append(children, d.Name())
		}
	}

	if !slices.Contains(children, "db.GetUser") {
		t.Errorf("database spans of the request = %q, want db.GetUser", children)
	}
}
