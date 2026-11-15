package config

import (
	"go/build"
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
	cfg, err := Load(writeConfig(t, "logging:\n  level: debug\ndb:\n  path: /var/lib/mocn/mocn.db\napi:\n  address: 127.0.0.1\n  port: 8080\nran:\n  signalling:\n    address: 10.0.0.1\n    s1_port: 36413\n    ng_port: 38413\n"))
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

	if want := (Signalling{Address: netip.MustParseAddr("10.0.0.1"), S1Port: 36413, NGPort: 38413}); cfg.RAN.Signalling != want {
		t.Errorf("RAN.Signalling = %+v, want %+v", cfg.RAN.Signalling, want)
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

	if want := (Signalling{Address: netip.IPv4Unspecified(), S1Port: 36412, NGPort: 38412}); cfg.RAN.Signalling != want {
		t.Errorf("RAN.Signalling = %+v, want %+v", cfg.RAN.Signalling, want)
	}

	if cfg.RAN.UserPlane != (UserPlane{}) {
		t.Errorf("RAN.UserPlane = %+v, want disabled", cfg.RAN.UserPlane)
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
		"s1 port":         {content: "db:\n  path: mocn.db\nran:\n  signalling:\n    s1_port: 65536\n", want: "ran.signalling.s1_port"},
		"ng port":         {content: "db:\n  path: mocn.db\nran:\n  signalling:\n    ng_port: 65536\n", want: "ran.signalling.ng_port"},
		"signalling zone": {content: "db:\n  path: mocn.db\nran:\n  signalling:\n    address: fe80::1%eth0\n", want: "ran.signalling.address"},
		"old s1 key":      {content: "db:\n  path: mocn.db\ns1:\n  port: 36412\n", want: "parse config"},
		"user plane without interface": {
			content: "db:\n  path: mocn.db\nran:\n  user_plane:\n    enabled: true\n",
			want:    "ran.user_plane.name or ran.user_plane.address is required",
		},
		"user plane name and address": {
			content: "db:\n  path: mocn.db\nran:\n  user_plane:\n    enabled: true\n    name: eth1\n    address: 10.0.0.1\n",
			want:    "not both",
		},
		"user plane loopback": {
			content: "db:\n  path: mocn.db\nran:\n  user_plane:\n    enabled: true\n    address: 127.0.0.1\n",
			want:    "ran.user_plane.address",
		},
		"user plane interface name": {
			content: "db:\n  path: mocn.db\nran:\n  user_plane:\n    enabled: true\n    name: a/b\n",
			want:    "ran.user_plane.name",
		},
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

func TestNoGatewayImports(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, imp := range pkg.Imports {
		if imp == "github.com/ellanetworks/mocn/internal/s1" || imp == "github.com/ellanetworks/mocn/internal/ng" {
			t.Fatalf("config imports %s", imp)
		}
	}
}

func fakeHost(t *testing.T) {
	t.Helper()

	orig := hostAddresses
	hostAddresses = func() (map[string][]netip.Addr, error) {
		return map[string][]netip.Addr{
			"eth1": {netip.MustParseAddr("fe80::1"), netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("10.0.0.1")},
			"eth2": {netip.MustParseAddr("fe80::2"), netip.MustParseAddr("2001:db8::2")},
			"eth3": {netip.MustParseAddr("fe80::3")},
		}, nil
	}

	t.Cleanup(func() { hostAddresses = orig })
}

func TestLoadUserPlane(t *testing.T) {
	fakeHost(t)

	const base = "db:\n  path: mocn.db\nran:\n  user_plane:\n    enabled: true\n"

	for name, tc := range map[string]struct {
		content string
		want    UserPlane
	}{
		"name prefers IPv4":   {base + "    name: eth1\n", UserPlane{Enabled: true, Name: "eth1", Address: netip.MustParseAddr("10.0.0.1")}},
		"name with IPv6 only": {base + "    name: eth2\n", UserPlane{Enabled: true, Name: "eth2", Address: netip.MustParseAddr("2001:db8::2")}},
		"address":             {base + "    address: 2001:db8::1\n", UserPlane{Enabled: true, Name: "eth1", Address: netip.MustParseAddr("2001:db8::1")}},
		"disabled":            {"db:\n  path: mocn.db\nran:\n  user_plane:\n    name: eth9\n", UserPlane{Name: "eth9"}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.content))
			if err != nil {
				t.Fatal(err)
			}

			if cfg.RAN.UserPlane != tc.want {
				t.Fatalf("ran.user_plane = %+v, want %+v", cfg.RAN.UserPlane, tc.want)
			}
		})
	}

	for name, tc := range map[string]struct{ content, want string }{
		"unknown interface":   {base + "    name: eth9\n", "interface eth9 does not exist"},
		"no global address":   {base + "    name: eth3\n", "interface eth3 has no unicast address"},
		"address not on host": {base + "    address: 10.0.0.9\n", "no interface on the host has the address 10.0.0.9"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.content)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}
