package config

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"

	"github.com/ellanetworks/mocn/internal/ports"
	"gopkg.in/yaml.v3"
)

const defaultAPIPort = 5030

type Config struct {
	Logging   Logging   `yaml:"logging"`
	DB        DB        `yaml:"db"`
	API       API       `yaml:"api"`
	RAN       RAN       `yaml:"ran"`
	Telemetry Telemetry `yaml:"telemetry"`
}

type RAN struct {
	Signalling Signalling `yaml:"signalling"`
	UserPlane  UserPlane  `yaml:"user_plane"`
}

type Signalling struct {
	Address netip.Addr `yaml:"address"`
	S1Port  int        `yaml:"s1_port"`
	NGPort  int        `yaml:"ng_port"`
}

type UserPlane struct {
	Enabled bool       `yaml:"enabled"`
	Name    string     `yaml:"name"`
	Address netip.Addr `yaml:"address"`
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

	for _, f := range []struct{ name, path string }{{prefix + ".tls.cert", t.Cert}, {prefix + ".tls.key", t.Key}} {
		if _, err := os.Stat(f.path); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}

	return nil
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

	sig := &cfg.RAN.Signalling
	if !sig.Address.IsValid() {
		sig.Address = netip.IPv4Unspecified()
	}

	if sig.S1Port == 0 {
		sig.S1Port = ports.S1
	}

	if sig.NGPort == 0 {
		sig.NGPort = ports.NG
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	if err := cfg.RAN.UserPlane.resolve(); err != nil {
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
	case c.RAN.Signalling.S1Port < 1 || c.RAN.Signalling.S1Port > 65535:
		return errors.New("ran.signalling.s1_port must be between 1 and 65535")
	case c.RAN.Signalling.NGPort < 1 || c.RAN.Signalling.NGPort > 65535:
		return errors.New("ran.signalling.ng_port must be between 1 and 65535")
	case c.RAN.Signalling.Address.Zone() != "":
		return errors.New("ran.signalling.address must not have a zone")
	case c.Telemetry.Enabled && c.Telemetry.OTLPEndpoint == "":
		return errors.New("telemetry.otlp_endpoint is required when telemetry.enabled is true")
	}

	if err := c.RAN.UserPlane.validate(); err != nil {
		return err
	}

	return c.API.TLS.validate("api")
}

const maxInterfaceName = 15

func (u UserPlane) validate() error {
	a := u.Address.Unmap()

	switch {
	case u.Name != "" && u.Address.IsValid():
		return errors.New("ran.user_plane: set either name or address, not both")
	case u.Enabled && u.Name == "" && !u.Address.IsValid():
		return errors.New("ran.user_plane.name or ran.user_plane.address is required when ran.user_plane.enabled is true")
	case u.Name != "" && (len(u.Name) > maxInterfaceName || strings.ContainsAny(u.Name, "/: \t\n")):
		return fmt.Errorf("ran.user_plane.name: %q is not an interface name", u.Name)
	case u.Address.IsValid() && (u.Address.Zone() != "" || a.IsUnspecified() || a.IsMulticast() || a.IsLoopback()):
		return fmt.Errorf("ran.user_plane.address: %s is not a unicast address", u.Address)
	}

	return nil
}

var hostAddresses = func() (map[string][]netip.Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	out := map[string][]netip.Addr{}

	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			return nil, err
		}

		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out[ifc.Name] = append(out[ifc.Name], p.Addr().Unmap())
			}
		}
	}

	return out, nil
}

func (u *UserPlane) resolve() error {
	if !u.Enabled {
		return nil
	}

	host, err := hostAddresses()
	if err != nil {
		return fmt.Errorf("ran.user_plane: list the host interfaces: %w", err)
	}

	if u.Name != "" {
		addrs, ok := host[u.Name]
		if !ok {
			return fmt.Errorf("ran.user_plane.name: interface %s does not exist on the host", u.Name)
		}

		for _, v4 := range []bool{true, false} {
			for _, a := range addrs {
				if a.Is4() == v4 && a.IsGlobalUnicast() {
					u.Address = a
					return nil
				}
			}
		}

		return fmt.Errorf("ran.user_plane.name: interface %s has no unicast address", u.Name)
	}

	u.Address = u.Address.Unmap()

	for name, addrs := range host {
		if slices.Contains(addrs, u.Address) {
			u.Name = name
			return nil
		}
	}

	return fmt.Errorf("ran.user_plane.address: no interface on the host has the address %s", u.Address)
}
