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
	cfg, err := Load(writeConfig(t, "logging:\n  level: debug\ndb:\n  path: /var/lib/mocn/mocn.db\napi:\n  address: 127.0.0.1\n  port: 8080\n"))
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
