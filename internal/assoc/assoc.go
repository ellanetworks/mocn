package assoc

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"time"

	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"go.opentelemetry.io/otel/trace"
)

// TS 36.413 clause 9.1.8.4
// TS 38.413 clause 9.2.6.1
const NodeName = "Ella MOCN"

type State string

const (
	Disconnected State = "disconnected"
	Connected    State = "connected"
	Up           State = "up"
	SetupFailed  State = "setup_failed"
)

var States = []State{Disconnected, Connected, Up, SetupFailed}

type RadioState string

const (
	RadioConnected RadioState = "connected"
	RadioUp        RadioState = "up"
)

var RadioStates = []RadioState{RadioConnected, RadioUp}

type Operator struct {
	ID          int64
	Name        string
	PLMNs       []db.PLMN
	PWS         bool
	GlobalENBID *db.GlobalENBID
	GlobalGNBID *db.GlobalGNBID
}

type TAI struct {
	PLMN db.PLMN
	TAC  uint32
}

type Setup interface {
	Ref() string
	Broadcasts(plmns []db.PLMN) bool
	Serves(tai TAI) bool
}

type Equaler[T any] interface {
	Equal(other T) bool
}

type RadioConfig[T any] interface {
	Equaler[T]
	Empty() bool
	Pushed() T
}

type Names struct {
	Interface   string
	Server      string
	Protocol    string
	Tracer      string
	PPID        uint32
	CorePort    int
	CoreStreams uint16
	Core        string
	CoreAttr    string
	Setup       string
	Update      string
	RadioUpdate string
	NoCore      string
	CoreFailure string
	Kill        string
}

type Config struct {
	Listener        *sctp.Listener
	Operators       func(ctx context.Context) ([]db.Operator, error)
	Metrics         *metrics.Metrics
	TracerProvider  trace.TracerProvider
	Logger          *slog.Logger
	CorePort        int
	SetupWait       time.Duration
	ResponseTimeout time.Duration
	WarningWait     time.Duration
	RetryInterval   time.Duration
	ReconnectMin    time.Duration
	ReconnectMax    time.Duration
	Now             func() time.Time
	IntN            func(n int) int
}

const (
	defaultSetupWait       = 4 * time.Second
	defaultResponseTimeout = 10 * time.Second
	defaultWarningWait     = 3 * time.Second
	defaultRetryInterval   = 10 * time.Second
	defaultReconnectMin    = time.Second
	defaultReconnectMax    = 30 * time.Second
	serverShutdownTimeout  = 5 * time.Second
	clientShutdownTimeout  = 5 * time.Second
	dialTimeout            = 10 * time.Second
)

func (cfg Config) withDefaults(names Names) Config {
	if cfg.CorePort == 0 {
		cfg.CorePort = names.CorePort
	}

	if cfg.IntN == nil {
		cfg.IntN = rand.IntN
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.Metrics == nil {
		cfg.Metrics = metrics.New()
	}

	for _, d := range []struct {
		v   *time.Duration
		def time.Duration
	}{
		{&cfg.SetupWait, defaultSetupWait},
		{&cfg.ResponseTimeout, defaultResponseTimeout},
		{&cfg.WarningWait, defaultWarningWait},
		{&cfg.RetryInterval, defaultRetryInterval},
		{&cfg.ReconnectMin, defaultReconnectMin},
		{&cfg.ReconnectMax, defaultReconnectMax},
	} {
		if *d.v <= 0 {
			*d.v = d.def
		}
	}

	return cfg
}

func addresses(a any) []string {
	addr, ok := a.(*sctp.SCTPAddr)
	if !ok || addr == nil {
		return nil
	}

	out := make([]string, 0, len(addr.IPAddrs))

	for _, ip := range addr.IPAddrs {
		if a, ok := netip.AddrFromSlice(ip.IP); ok {
			out = append(out, a.Unmap().String())
		}
	}

	return out
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func GetIE[T any, P interface {
	*T
	per.Unmarshaler
}, ID any](get func(ID, per.Unmarshaler) (bool, error), id ID,
) (*T, error) {
	var v T

	ok, err := get(id, P(&v))
	if err != nil || !ok {
		return nil, err
	}

	return &v, nil
}
