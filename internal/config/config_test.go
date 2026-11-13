package config

import (
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mocn.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestLoad(t *testing.T) {
	cfg, err := Load(writeConfig(t, "logging:\n  level: debug\ndb:\n  path: /var/lib/mocn/mocn.db\napi:\n  address: 127.0.0.1\n  port: 8080\ns1:\n  address: 10.0.0.1\n  port: 36413\nng:\n  address: 10.0.0.2\n  port: 38413\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Logging.Level != slog.LevelDebug {
		t.Errorf("Logging.Level = %v, want debug", cfg.Logging.Level)
	}

	if cfg.DB.Path != "/var/lib/mocn/mocn.db" {
		t.Errorf("DB.Path = %q, want /var/lib/mocn/mocn.db", cfg.DB.Path)
	}

	if want := netip.MustParseAddr("127.0.0.1"); cfg.API.Address != want {
		t.Errorf("API.Address = %v, want %v", cfg.API.Address, want)
	}

	if got := cfg.API.ListenAddress(); got != "127.0.0.1:8080" {
		t.Errorf("API.ListenAddress() = %q, want 127.0.0.1:8080", got)
	}

	if want := (Endpoint{Address: netip.MustParseAddr("10.0.0.1"), Port: 36413}); cfg.S1 != want {
		t.Errorf("S1 = %+v, want %+v", cfg.S1, want)
	}

	if want := (Endpoint{Address: netip.MustParseAddr("10.0.0.2"), Port: 38413}); cfg.NG != want {
		t.Errorf("NG = %+v, want %+v", cfg.NG, want)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, "db:\n  path: mocn.db\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Logging.Level != slog.LevelInfo {
		t.Errorf("Logging.Level = %v, want info", cfg.Logging.Level)
	}

	if got := cfg.API.ListenAddress(); got != "0.0.0.0:5030" {
		t.Errorf("API.ListenAddress() = %q, want 0.0.0.0:5030", got)
	}

	if cfg.API.TLS != nil {
		t.Errorf("API.TLS = %+v, want nil", cfg.API.TLS)
	}

	if want := (Endpoint{Address: netip.IPv4Unspecified(), Port: 36412}); cfg.S1 != want {
		t.Errorf("S1 = %+v, want %+v", cfg.S1, want)
	}

	if want := (Endpoint{Address: netip.IPv4Unspecified(), Port: 38412}); cfg.NG != want {
		t.Errorf("NG = %+v, want %+v", cfg.NG, want)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := map[string]struct {
		content string
		want    string
	}{
		"unknown key":     {content: "logging:\n  level: info\nunknown: 1\n", want: "parse config"},
		"invalid level":   {content: "logging:\n  level: loud\n", want: "parse config"},
		"not yaml":        {content: "logging: [\n", want: "parse config"},
		"invalid address": {content: "api:\n  address: nowhere\n", want: "parse config"},
		"missing db path": {content: "logging:\n  level: info\n", want: "db.path is required"},
		"port too high":   {content: "db:\n  path: mocn.db\napi:\n  port: 70000\n", want: "api.port"},
		"negative port":   {content: "db:\n  path: mocn.db\napi:\n  port: -1\n", want: "api.port"},
		"s1 port":         {content: "db:\n  path: mocn.db\ns1:\n  port: 65536\n", want: "s1.port"},
		"s1 address":      {content: "db:\n  path: mocn.db\ns1:\n  address: 36412\n", want: "parse config"},
		"s1 zone":         {content: "db:\n  path: mocn.db\ns1:\n  address: fe80::1%eth0\n", want: "s1.address"},
		"ng port":         {content: "db:\n  path: mocn.db\nng:\n  port: 65536\n", want: "ng.port"},
		"ng zone":         {content: "db:\n  path: mocn.db\nng:\n  address: fe80::1%eth0\n", want: "ng.address"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadAPITLS(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	for _, f := range []string{cert, key} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := Load(writeConfig(t, "db:\n  path: mocn.db\napi:\n  tls:\n    cert: "+cert+"\n    key: "+key+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := (&TLS{Cert: cert, Key: key}); cfg.API.TLS == nil || *cfg.API.TLS != *want {
		t.Errorf("API.TLS = %+v, want %+v", cfg.API.TLS, want)
	}

	if _, err := cfg.API.TLS.Load(); err == nil || !strings.Contains(err.Error(), "load the API certificate") {
		t.Errorf("TLS.Load error = %v, want a certificate error", err)
	}

	tests := map[string]struct {
		content string
		want    string
	}{
		"missing key":  {content: "db:\n  path: mocn.db\napi:\n  tls:\n    cert: " + cert + "\n", want: "api.tls.cert and api.tls.key must both be set"},
		"missing cert": {content: "db:\n  path: mocn.db\napi:\n  tls:\n    key: " + key + "\n", want: "api.tls.cert and api.tls.key must both be set"},
		"absent file":  {content: "db:\n  path: mocn.db\napi:\n  tls:\n    cert: " + cert + "\n    key: " + filepath.Join(dir, "none.pem") + "\n", want: "api.tls.key"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadTelemetry(t *testing.T) {
	const valid = "db:\n  path: mocn.db\n"

	cfg, err := Load(writeConfig(t, valid+"telemetry:\n  enabled: true\n  otlp_endpoint: collector:4317\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := (Telemetry{Enabled: true, OTLPEndpoint: "collector:4317"}); cfg.Telemetry != want {
		t.Fatalf("telemetry = %+v, want %+v", cfg.Telemetry, want)
	}

	if cfg, err = Load(writeConfig(t, valid)); err != nil || cfg.Telemetry != (Telemetry{}) {
		t.Fatalf("telemetry = %+v, %v, want off", cfg.Telemetry, err)
	}

	if _, err := Load(writeConfig(t, valid+"telemetry:\n  enabled: true\n")); err == nil ||
		!strings.Contains(err.Error(), "telemetry.otlp_endpoint is required") {
		t.Fatalf("Load without telemetry.otlp_endpoint = %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("Load error = %v, want read config", err)
	}
}

func TestExampleConfig(t *testing.T) {
	if _, err := Load(filepath.Join("..", "..", "mocn.yaml")); err != nil {
		t.Fatalf("Load mocn.yaml: %v", err)
	}
}
