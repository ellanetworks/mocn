package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Config struct {
	Frontend     fs.FS
	Ready        func(ctx context.Context) []Check
	Metrics      prometheus.Gatherer
	Requests     *RequestMetrics
	Auth         AuthStore
	Operators    OperatorStore
	S1           S1View
	JWTSecret    func() []byte
	SecureCookie bool
	BcryptCost   int
	Now          func() time.Time
	Logger       *slog.Logger
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

	return securityHeaders(cfg, http.NewCrossOriginProtection().Handler(mux))
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
	perm    Permission
	handler http.Handler
}

func routes(cfg Config, a *auth) []route {
	return []route{
		{"GET /api/v1/livez", public, Livez()},
		{"GET /api/v1/readyz", public, Readyz(cfg)},
		{"GET /api/v1/status", public, GetStatus(cfg)},
		{"GET /api/v1/openapi.yaml", public, OpenAPISpec()},
		{"GET /api/v1/metrics", public, GetMetrics(cfg)},
		{"POST /api/v1/init", public, Initialize(a)},
		{"POST /api/v1/auth/login", public, Login(a)},
		{"POST /api/v1/auth/refresh", public, Refresh(a)},
		{"POST /api/v1/auth/logout", public, Logout(a)},

		{"GET /api/v1/users/me", PermSelf, GetMyUser(a)},
		{"PUT /api/v1/users/me/password", PermSelf, a.needsLogin(UpdateMyPassword(a))},
		{"GET /api/v1/users/me/api-tokens", PermSelf, ListAPITokens(a, myself(cfg))},
		{"POST /api/v1/users/me/api-tokens", PermSelf, a.needsLogin(CreateAPIToken(a, myself(cfg)))},
		{"DELETE /api/v1/users/me/api-tokens/{id}", PermSelf, DeleteAPIToken(a, myself(cfg))},

		{"GET /api/v1/operators", PermReadOperators, ListOperators(cfg)},
		{"POST /api/v1/operators", PermUpdateOperators, CreateOperator(a)},
		{"GET /api/v1/operators/{name}", PermReadOperators, GetOperator(cfg)},
		{"PUT /api/v1/operators/{name}", PermUpdateOperators, UpdateOperator(a)},
		{"DELETE /api/v1/operators/{name}", PermUpdateOperators, DeleteOperator(a)},

		{"GET /api/v1/radios", PermReadOperators, ListRadios(cfg)},
		{"GET /api/v1/radios/{ref}", PermReadOperators, GetRadio(cfg)},
		{"GET /api/v1/ue-connections", PermReadOperators, ListUEConnections(cfg)},

		{"GET /api/v1/users", PermManageUsers, a.needsLogin(ListUsers(a))},
		{"POST /api/v1/users", PermManageUsers, a.needsLogin(CreateUser(a))},
		{"GET /api/v1/users/{email}", PermManageUsers, a.needsLogin(GetUser(a))},
		{"PUT /api/v1/users/{email}", PermManageUsers, a.needsLogin(UpdateUser(a))},
		{"DELETE /api/v1/users/{email}", PermManageUsers, a.needsLogin(DeleteUser(a))},
		{"PUT /api/v1/users/{email}/password", PermManageUsers, a.needsLogin(UpdateUserPassword(a))},
		{"DELETE /api/v1/users/{email}/password", PermManageUsers, a.needsLogin(DeleteUserPassword(a))},
		{"GET /api/v1/users/{email}/api-tokens", PermManageUsers, a.needsLogin(ListAPITokens(a, userOfPath(cfg)))},
		{"POST /api/v1/users/{email}/api-tokens", PermManageUsers, a.needsLogin(CreateAPIToken(a, userOfPath(cfg)))},
		{"DELETE /api/v1/users/{email}/api-tokens/{id}", PermManageUsers, a.needsLogin(DeleteAPIToken(a, userOfPath(cfg)))},
	}
}
