package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Frontend       fs.FS
	Ready          func(ctx context.Context) []Check
	Metrics        prometheus.Gatherer
	Requests       *RequestMetrics
	Auth           authStore
	Operators      operatorStore
	UserPlane      bool
	Plane          PlaneView
	S1             S1View
	NG             NGView
	JWTSecret      func() []byte
	SecureCookie   bool
	BcryptCost     int
	TracerProvider trace.TracerProvider
	Now            func() time.Time
	Logger         *slog.Logger
}

type PlaneView interface {
	Status(operatorID int64) (gtpu.Status, bool)
}

type S1View interface {
	MMEStatus(operatorID int64, tnlAddresses []string) (s1.MMEStatus, bool)
	RANNodes() []s1.RANNode
	UEs() []s1.UE
}

type NGView interface {
	AMFStatus(operatorID int64, tnlAddresses []string) (ng.AMFStatus, bool)
	RANNodes() []ng.RANNode
	UEs() []ng.UE
}

func NewHandler(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	a := newAuth(cfg)
	mux := http.NewServeMux()

	for _, rt := range routes(cfg, a) {
		h := rt.handler
		if rt.perm != public {
			h = a.authenticate(a.authorize(rt.perm, h))
		}

		mux.Handle(rt.pattern, cfg.Requests.instrument(rt.pattern, h))
	}

	if cfg.Frontend != nil {
		mux.Handle("GET /", Frontend(cfg.Frontend))
	}

	return traced(cfg, securityHeaders(cfg, http.NewCrossOriginProtection().Handler(mux)))
}

var untraced = map[string]bool{"/api/v1/livez": true, "/api/v1/readyz": true, "/api/v1/metrics": true}

func traced(cfg Config, next http.Handler) http.Handler {
	if cfg.TracerProvider == nil {
		return next
	}

	route := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)

		if i := strings.IndexByte(r.Pattern, '/'); i >= 0 {
			trace.SpanFromContext(r.Context()).SetAttributes(semconv.HTTPRoute(r.Pattern[i:]))
		}
	})

	return otelhttp.NewHandler(route, "",
		otelhttp.WithTracerProvider(cfg.TracerProvider),
		otelhttp.WithPropagators(tracing.Propagator),
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return strings.HasPrefix(r.URL.Path, "/api/") && !untraced[r.URL.Path] && !strings.HasPrefix(r.URL.Path, "/api/v1/pprof/")
		}),
	)
}

func securityHeaders(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		if cfg.SecureCookie {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}

		next.ServeHTTP(w, r)
	})
}

type route struct {
	pattern string
	perm    permission
	handler http.Handler
}

func routes(cfg Config, a *auth) []route {
	return []route{
		{"GET /api/v1/livez", public, livez()},
		{"GET /api/v1/readyz", public, readyz(cfg)},
		{"GET /api/v1/status", public, getStatus(cfg)},
		{"GET /api/v1/openapi.yaml", public, openAPISpec()},
		{"GET /api/v1/metrics", public, getMetrics(cfg)},
		{"POST /api/v1/init", public, initialize(a)},
		{"POST /api/v1/auth/login", public, login(a)},
		{"POST /api/v1/auth/refresh", public, refresh(a)},
		{"POST /api/v1/auth/logout", public, logout(a)},

		{"GET /api/v1/users/me", permSelf, getMyUser(a)},
		{"PUT /api/v1/users/me/password", permSelf, a.needsLogin(updateMyPassword(a))},
		{"GET /api/v1/users/me/api-tokens", permSelf, listAPITokens(a, myself(cfg))},
		{"POST /api/v1/users/me/api-tokens", permSelf, a.needsLogin(createAPIToken(a, myself(cfg)))},
		{"DELETE /api/v1/users/me/api-tokens/{id}", permSelf, deleteAPIToken(a, myself(cfg))},

		{"GET /api/v1/operators", permReadOperators, listOperators(cfg)},
		{"POST /api/v1/operators", permUpdateOperators, createOperator(a)},
		{"GET /api/v1/operators/{name}", permReadOperators, getOperator(cfg)},
		{"PUT /api/v1/operators/{name}", permUpdateOperators, updateOperator(a)},
		{"DELETE /api/v1/operators/{name}", permUpdateOperators, deleteOperator(a)},

		{"GET /api/v1/radios", permReadOperators, listRadios(cfg)},
		{"GET /api/v1/radios/{ref}", permReadOperators, getRadio(cfg)},
		{"GET /api/v1/ue-connections", permReadOperators, listUEConnections(cfg)},

		{"GET /api/v1/users", permManageUsers, a.needsLogin(listUsers(a))},
		{"POST /api/v1/users", permManageUsers, a.needsLogin(createUser(a))},
		{"GET /api/v1/users/{email}", permManageUsers, a.needsLogin(getUser(a))},
		{"PUT /api/v1/users/{email}", permManageUsers, a.needsLogin(updateUser(a))},
		{"DELETE /api/v1/users/{email}", permManageUsers, a.needsLogin(deleteUser(a))},
		{"PUT /api/v1/users/{email}/password", permManageUsers, a.needsLogin(updateUserPassword(a))},
		{"DELETE /api/v1/users/{email}/password", permManageUsers, a.needsLogin(deleteUserPassword(a))},
		{"GET /api/v1/users/{email}/api-tokens", permManageUsers, a.needsLogin(listAPITokens(a, userOfPath(cfg)))},
		{"POST /api/v1/users/{email}/api-tokens", permManageUsers, a.needsLogin(createAPIToken(a, userOfPath(cfg)))},
		{"DELETE /api/v1/users/{email}/api-tokens/{id}", permManageUsers, a.needsLogin(deleteAPIToken(a, userOfPath(cfg)))},

		{"GET /api/v1/pprof/{$}", permReadProfiles, http.HandlerFunc(pprof.Index)},
		{"GET /api/v1/pprof/allocs", permReadProfiles, pprof.Handler("allocs")},
		{"GET /api/v1/pprof/goroutine", permReadProfiles, pprof.Handler("goroutine")},
		{"GET /api/v1/pprof/goroutineleak", permReadProfiles, pprof.Handler("goroutineleak")},
		{"GET /api/v1/pprof/heap", permReadProfiles, pprof.Handler("heap")},
		{"GET /api/v1/pprof/threadcreate", permReadProfiles, pprof.Handler("threadcreate")},
		{"GET /api/v1/pprof/cmdline", permReadProfiles, http.HandlerFunc(pprof.Cmdline)},
		{"GET /api/v1/pprof/profile", permReadProfiles, http.HandlerFunc(pprof.Profile)},
		{"GET /api/v1/pprof/symbol", permReadProfiles, http.HandlerFunc(pprof.Symbol)},
		{"POST /api/v1/pprof/symbol", permReadProfiles, http.HandlerFunc(pprof.Symbol)},
		{"GET /api/v1/pprof/trace", permReadProfiles, http.HandlerFunc(pprof.Trace)},
	}
}
