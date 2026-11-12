package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func TestRoles(t *testing.T) {
	const (
		admin = 1 << iota
		readOnly
		networkManager
		all = admin | readOnly | networkManager
	)

	want := map[string]int{
		"GET /api/v1/users/me":                         all,
		"PUT /api/v1/users/me/password":                all,
		"GET /api/v1/users/me/api-tokens":              all,
		"POST /api/v1/users/me/api-tokens":             all,
		"DELETE /api/v1/users/me/api-tokens/{id}":      all,
		"GET /api/v1/operators":                        all,
		"POST /api/v1/operators":                       admin | networkManager,
		"GET /api/v1/operators/{name}":                 all,
		"PUT /api/v1/operators/{name}":                 admin | networkManager,
		"DELETE /api/v1/operators/{name}":              admin | networkManager,
		"GET /api/v1/users":                            admin,
		"POST /api/v1/users":                           admin,
		"GET /api/v1/users/{email}":                    admin,
		"PUT /api/v1/users/{email}":                    admin,
		"DELETE /api/v1/users/{email}":                 admin,
		"PUT /api/v1/users/{email}/password":           admin,
		"DELETE /api/v1/users/{email}/password":        admin,
		"GET /api/v1/users/{email}/api-tokens":         admin,
		"POST /api/v1/users/{email}/api-tokens":        admin,
		"DELETE /api/v1/users/{email}/api-tokens/{id}": admin,
	}

	store := openAuthStore(t)
	cfg := Config{
		Auth: store, JWTSecret: func() []byte { return []byte("0123456789abcdef0123456789abcdef") }, BcryptCost: bcrypt.MinCost,
		Now: time.Now, Logger: slog.New(slog.DiscardHandler),
	}
	a := newAuth(cfg)
	mux := http.NewServeMux()
	protected := 0

	for _, rt := range routes(cfg, a) {
		if rt.perm == public {
			continue
		}

		protected++

		mux.Handle(rt.pattern, a.authenticate(a.authorize(rt.perm, http.HandlerFunc(func(w http.ResponseWriter,
			_ *http.Request,
		) {
			w.WriteHeader(http.StatusNoContent)
		}))))
	}

	if len(want) != protected {
		t.Fatalf("the test covers %d routes, but %d need a token", len(want), protected)
	}

	bits := map[db.Role]int{db.RoleAdmin: admin, db.RoleReadOnly: readOnly, db.RoleNetworkManager: networkManager}
	tokens := map[db.Role]string{}

	for role := range bits {
		id, err := store.CreateUser(t.Context(), db.NewUser{
			Email: strings.Repeat("x", int(role)) + "@example.com",
			Role:  role, PasswordHash: "unused",
		}, MaxNumUsers)
		if err != nil {
			t.Fatal(err)
		}

		u, err := store.GetUserByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}

		sessionID, err := store.CreateSession(t.Context(), db.NewSession{
			UserID: id, TokenHash: []byte{byte(role)}, Method: db.SessionPassword,
			CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}, MaxSessionsPerUser)
		if err != nil {
			t.Fatal(err)
		}

		if tokens[role], err = a.accessToken(u, sessionID); err != nil {
			t.Fatal(err)
		}
	}

	path := strings.NewReplacer("{id}", "1", "{email}", "nobody@example.com", "{name}", "nobody")

	for route, allowedRoles := range want {
		method, pattern, _ := strings.Cut(route, " ")

		for role, token := range tokens {
			req := httptest.NewRequestWithContext(t.Context(), method, path.Replace(pattern), nil)
			req.Header.Set("Authorization", "Bearer "+token)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			allowed := allowedRoles&bits[role] != 0
			if wantCode := map[bool]int{true: http.StatusNoContent, false: http.StatusForbidden}[allowed]; rec.Code != wantCode {
				t.Errorf("%s as role %d = %d, want %d", route, role, rec.Code, wantCode)
			}
		}
	}
}
