package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ellanetworks/mocn/internal/api"
	"github.com/ellanetworks/mocn/internal/logging/logtest"
	"github.com/ellanetworks/mocn/internal/server"
	"golang.org/x/crypto/bcrypt"
)

type running struct {
	base string
	rec  *logtest.Recorder
	stop func() error
}

func start(t *testing.T, frontend fstest.MapFS) *running {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	rec := &logtest.Recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- server.Run(ctx, server.Config{
			Listener:   ln,
			DBPath:     filepath.Join(t.TempDir(), "mocn.db"),
			Frontend:   frontend,
			BcryptCost: bcrypt.MinCost,
			Logger:     rec.Logger(),
		})
	}()

	stopped := false
	r := &running{base: "http://" + ln.Addr().String(), rec: rec}
	r.stop = func() error {
		if stopped {
			return nil
		}

		stopped = true

		cancel()

		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after cancel")
			return nil
		}
	}

	t.Cleanup(func() { _ = r.stop() })

	return r
}

func (r *running) call(t *testing.T, client *http.Client, method, path, token string, body any) (int, json.RawMessage) {
	t.Helper()

	var reader io.Reader

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}

		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, r.base+path, reader)
	if err != nil {
		t.Fatal(err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) == 0 {
		return res.StatusCode, nil
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
	}

	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%s %s: invalid JSON %q", method, path, raw)
	}

	return res.StatusCode, resp.Result
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()

	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}

	return v
}

func TestUsersOverHTTP(t *testing.T) {
	r := start(t, fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Jar: jar}

	if code, result := r.call(t, client, http.MethodGet, "/api/v1/status", "", nil); code != http.StatusOK ||
		decode[api.Status](t, result).Initialized {
		t.Fatalf("status before init = %d %s", code, result)
	}

	admin := api.InitializeParams{Email: "admin@example.com", Password: "correct horse"}

	code, result := r.call(t, client, http.MethodPost, "/api/v1/init", "", admin)
	if code != http.StatusCreated || decode[api.LoginResponse](t, result).Token == "" {
		t.Fatalf("init = %d %s", code, result)
	}

	code, result = r.call(t, client, http.MethodPost, "/api/v1/auth/login", "", api.LoginParams(admin))
	if code != http.StatusOK {
		t.Fatalf("login = %d %s", code, result)
	}

	token := decode[api.LoginResponse](t, result).Token

	if code, result := r.call(t, client, http.MethodPost, "/api/v1/users", token, api.CreateUserParams{
		Email: "ro@example.com", Password: "correct horse", RoleID: 2,
	}); code != http.StatusCreated {
		t.Fatalf("create user = %d %s", code, result)
	}

	code, result = r.call(t, client, http.MethodGet, "/api/v1/users", token, nil)
	if code != http.StatusOK {
		t.Fatalf("list users = %d %s", code, result)
	}

	want := []api.User{
		{Email: "admin@example.com", RoleID: 1, PasswordSet: true},
		{Email: "ro@example.com", RoleID: 2, PasswordSet: true},
	}

	users := decode[api.ListUsersResponse](t, result)
	if users.TotalCount != 2 || len(users.Items) != 2 || users.Items[0] != want[0] || users.Items[1] != want[1] {
		t.Fatalf("users = %+v, want %+v", users, want)
	}

	operator := api.Operator{
		Name:        "Operator A",
		PLMNs:       []api.PLMN{{MCC: "001", MNC: "01"}},
		GlobalENBID: &api.GlobalENBID{PLMN: api.PLMN{MCC: "001", MNC: "01"}, Type: "macro", ID: 703710},
		MMEs:        []api.MME{{TNLAddresses: []string{"192.0.2.1", "192.0.2.2"}}, {TNLAddresses: []string{"192.0.2.3"}}},
		AMFs:        []api.CoreNode{},
	}

	if code, result := r.call(t, client, http.MethodPost, "/api/v1/operators", token, operator); code != http.StatusCreated {
		t.Fatalf("create operator = %d %s", code, result)
	}

	code, result = r.call(t, client, http.MethodGet, "/api/v1/operators", token, nil)

	ops := decode[api.ListOperatorsResponse](t, result).Items
	if code != http.StatusOK || len(ops) != 1 {
		t.Fatalf("list operators = %d %s", code, result)
	}

	for i, m := range ops[0].MMEs {
		if m.Status == nil || m.Status.State == "" {
			t.Fatalf("MME %d status = %+v, want a runtime status", i, m.Status)
		}

		ops[0].MMEs[i].Status = nil
	}

	if !reflect.DeepEqual(ops[0], operator) {
		t.Fatalf("list operators = %+v, want %+v", ops[0], operator)
	}

	code, result = r.call(t, client, http.MethodGet, "/api/v1/radios", token, nil)
	if nodes := decode[api.ListRadiosResponse](t, result); code != http.StatusOK || nodes.Items == nil ||
		len(nodes.Items) != 0 {
		t.Fatalf("list radios = %d %s", code, result)
	}

	if code, result := r.call(t, client, http.MethodDelete, "/api/v1/operators/Operator%20A", token, nil); code != http.StatusOK {
		t.Fatalf("delete operator = %d %s", code, result)
	}

	code, result = r.call(t, client, http.MethodGet, "/api/v1/operators", token, nil)
	if ops := decode[api.ListOperatorsResponse](t, result).Items; code != http.StatusOK || len(ops) != 0 {
		t.Fatalf("list operators after delete = %d %s", code, result)
	}

	if code, _ := r.call(t, client, http.MethodPost, "/api/v1/auth/refresh", "", nil); code != http.StatusOK {
		t.Fatalf("refresh before logout = %d", code)
	}

	if code, _ := r.call(t, client, http.MethodPost, "/api/v1/auth/logout", "", nil); code != http.StatusNoContent {
		t.Fatalf("logout = %d", code)
	}

	if code, _ := r.call(t, client, http.MethodGet, "/api/v1/users", token, nil); code != http.StatusUnauthorized {
		t.Fatalf("list users after logout = %d, want %d", code, http.StatusUnauthorized)
	}

	if code, _ := r.call(t, client, http.MethodPost, "/api/v1/auth/refresh", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout = %d, want %d", code, http.StatusUnauthorized)
	}

	if code, result := r.call(t, client, http.MethodGet, "/api/v1/readyz", "", nil); code != http.StatusOK ||
		!decode[api.Readiness](t, result).Ready {
		t.Fatalf("readyz = %d %s", code, result)
	}

	if err := r.stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := r.rec.Messages(t, "mocn stopped"); len(got) != 1 {
		t.Errorf("stopped logs = %d, want 1", len(got))
	}
}

func TestRunServesFrontendAndStops(t *testing.T) {
	r := start(t, fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, r.base+"/", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK || string(body) != "<html></html>" {
		t.Fatalf("GET / = %d %q, want 200 <html></html>", resp.StatusCode, body)
	}

	if err := r.stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := r.rec.Messages(t, "mocn started"); len(got) != 1 {
		t.Errorf("started logs = %d, want 1", len(got))
	}

	if got := r.rec.Messages(t, "mocn stopped"); len(got) != 1 {
		t.Errorf("stopped logs = %d, want 1", len(got))
	}
}

func TestRunFailsWhenListenerCloses(t *testing.T) {
	var rec logtest.Recorder

	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	if err := server.Run(context.Background(), server.Config{
		Listener: ln, DBPath: filepath.Join(t.TempDir(), "mocn.db"), Frontend: fstest.MapFS{}, Logger: rec.Logger(),
	}); err == nil {
		t.Fatal("Run returned nil, want a serve error")
	}
}

func TestRunFailsWithoutDatabase(t *testing.T) {
	var rec logtest.Recorder

	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	if err := server.Run(context.Background(), server.Config{
		Listener: ln, DBPath: filepath.Join(t.TempDir(), "missing", "mocn.db"), Frontend: fstest.MapFS{},
		Logger: rec.Logger(),
	}); err == nil {
		t.Fatal("Run returned nil, want a database error")
	}
}
