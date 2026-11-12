package config

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"

	"gopkg.in/yaml.v3"
)

const defaultAPIPort = 5030

type Config struct {
	Logging Logging `yaml:"logging"`
	DB      DB      `yaml:"db"`
	API     API     `yaml:"api"`
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
}

func (a API) ListenAddress() string {
	return netip.AddrPortFrom(a.Address, uint16(a.Port)).String()
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
	}

	return nil
}
