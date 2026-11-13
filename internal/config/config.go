package config

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	defaultAPIPort = 5030
	defaultS1Port  = 36412
	defaultNGPort  = 38412
)

type Config struct {
	Logging   Logging   `yaml:"logging"`
	DB        DB        `yaml:"db"`
	API       API       `yaml:"api"`
	S1        Endpoint  `yaml:"s1"`
	NG        Endpoint  `yaml:"ng"`
	Telemetry Telemetry `yaml:"telemetry"`
}

type Telemetry struct {
	Enabled      bool   `yaml:"enabled"`
	OTLPEndpoint string `yaml:"otlp_endpoint"`
}

type Logging struct {
	Level slog.Level `yaml:"level"`
}

type DB struct {
	Path string `yaml:"path"`
}

type API struct {
	Address netip.Addr `yaml:"address"`
	Port    int        `yaml:"port"`
	TLS     *TLS       `yaml:"tls"`
}

type TLS struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

func (t *TLS) Load() (*tls.Config, error) {
	if t == nil {
		return nil, nil //nolint:nilnil
	}

	cert, err := tls.LoadX509KeyPair(t.Cert, t.Key)
	if err != nil {
		return nil, fmt.Errorf("load the API certificate: %w", err)
	}

	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, nil
}

func (t *TLS) validate(prefix string) error {
	if t == nil {
		return nil
	}

	if t.Cert == "" || t.Key == "" {
		return fmt.Errorf("%s.tls.cert and %s.tls.key must both be set", prefix, prefix)
	}

	for name, path := range map[string]string{prefix + ".tls.cert": t.Cert, prefix + ".tls.key": t.Key} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

func (a API) ListenAddress() string {
	return netip.AddrPortFrom(a.Address, uint16(a.Port)).String()
}

type Endpoint struct {
	Address netip.Addr `yaml:"address"`
	Port    int        `yaml:"port"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if !cfg.API.Address.IsValid() {
		cfg.API.Address = netip.IPv4Unspecified()
	}

	if cfg.API.Port == 0 {
		cfg.API.Port = defaultAPIPort
	}

	if !cfg.S1.Address.IsValid() {
		cfg.S1.Address = netip.IPv4Unspecified()
	}

	if cfg.S1.Port == 0 {
		cfg.S1.Port = defaultS1Port
	}

	if !cfg.NG.Address.IsValid() {
		cfg.NG.Address = netip.IPv4Unspecified()
	}

	if cfg.NG.Port == 0 {
		cfg.NG.Port = defaultNGPort
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) validate() error {
	switch {
	case c.DB.Path == "":
		return errors.New("db.path is required")
	case c.API.Port < 1 || c.API.Port > 65535:
		return errors.New("api.port must be between 1 and 65535")
	case c.S1.Port < 1 || c.S1.Port > 65535:
		return errors.New("s1.port must be between 1 and 65535")
	case c.S1.Address.Zone() != "":
		return errors.New("s1.address must not have a zone")
	case c.NG.Port < 1 || c.NG.Port > 65535:
		return errors.New("ng.port must be between 1 and 65535")
	case c.NG.Address.Zone() != "":
		return errors.New("ng.address must not have a zone")
	case c.Telemetry.Enabled && c.Telemetry.OTLPEndpoint == "":
		return errors.New("telemetry.otlp_endpoint is required when telemetry.enabled is true")
	}

	return c.API.TLS.validate("api")
}
