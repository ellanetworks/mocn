package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/api"
	"github.com/ellanetworks/mocn/internal/config"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/tracing"
	"github.com/ellanetworks/mocn/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"
)

const (
	shutdownTimeout        = 5 * time.Second
	tracingShutdownTimeout = 5 * time.Second
	readHeaderTimeout      = 10 * time.Second
	CheckDatabase          = "database"
)

type Config struct {
	Listener       net.Listener
	TLS            *tls.Config
	S1Listener     *sctp.Listener
	NGListener     *sctp.Listener
	DBPath         string
	Frontend       fs.FS
	BcryptCost     int
	Telemetry      config.Telemetry
	UserPlane      config.UserPlane
	TracerProvider trace.TracerProvider
	Logger         *slog.Logger
}

func Run(ctx context.Context, cfg Config) (err error) {
	owned := false

	defer func() {
		if !owned {
			closeListeners(cfg)
		}
	}()

	if c := cfg.BcryptCost; c != 0 && (c < bcrypt.MinCost || c > bcrypt.MaxCost) {
		return fmt.Errorf("bcrypt cost %d is out of range [%d, %d]", c, bcrypt.MinCost, bcrypt.MaxCost)
	}

	tp, shutdownTracing, err := newTracerProvider(ctx, cfg)
	if err != nil {
		return err
	}

	defer func() {
		tracingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tracingShutdownTimeout)
		defer cancel()

		if err := shutdownTracing(tracingCtx); err != nil {
			cfg.Logger.WarnContext(tracingCtx, "failed to export the last spans", slog.Any("error", err))
		}
	}()

	database, err := db.Open(ctx, cfg.DBPath, db.WithTracerProvider(tp))
	if err != nil {
		return err
	}

	defer func() {
		if cerr := database.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close database: %w", cerr))
		}
	}()

	secret, err := database.JWTSecret(ctx)
	if err != nil {
		return fmt.Errorf("read the JWT secret: %w", err)
	}

	requests := api.NewRequestMetrics()

	m := metrics.New()

	plane := gtpu.New(gtpu.Config{
		RANAddress: cfg.UserPlane.Address, RANInterface: cfg.UserPlane.Name, Enabled: cfg.UserPlane.Enabled,
		Logger: cfg.Logger,
	})

	if err := plane.Start(ctx); err != nil {
		return err
	}

	userPlane := planeReconciler{plane: plane, db: database}
	if err := userPlane.Reconcile(ctx); err != nil {
		cfg.Logger.ErrorContext(ctx, "failed to apply the user-plane settings of the operators", slog.Any("error", err))
	}

	s1Gateway := s1.New(s1.Config{
		Listener: cfg.S1Listener, Operators: database.ListOperators, Metrics: m, TracerProvider: tp, Logger: cfg.Logger,
		Plane: plane,
	})

	ngGateway := ng.New(ng.Config{
		Listener: cfg.NGListener, Operators: database.ListOperators, Metrics: m, TracerProvider: tp, Logger: cfg.Logger,
		Plane: plane,
	})

	gatewayCtx, stopGateways := context.WithCancel(ctx)
	defer stopGateways()

	var gateways sync.WaitGroup

	gateways.Go(func() { s1Gateway.Run(gatewayCtx) })
	gateways.Go(func() { ngGateway.Run(gatewayCtx) })

	defer func() {
		stopGateways()
		gateways.Wait()
	}()

	srv := &http.Server{
		Handler: api.NewHandler(api.Config{
			Frontend:       cfg.Frontend,
			Ready:          readiness(database),
			Metrics:        newRegistry(database, requests, append(m.Collectors(s1Gateway, ngGateway), plane.Collectors()...)),
			Requests:       requests,
			Auth:           database,
			Operators:      reconciling{DB: database, gateways: []reconciler{userPlane, s1Gateway, ngGateway}, logger: cfg.Logger},
			UserPlane:      cfg.UserPlane.Enabled,
			Plane:          plane,
			S1:             s1Gateway,
			NG:             ngGateway,
			SecureCookie:   cfg.TLS != nil,
			JWTSecret:      func() []byte { return secret },
			BcryptCost:     cfg.BcryptCost,
			TracerProvider: tp,
			Now:            time.Now,
			Logger:         cfg.Logger,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		TLSConfig:         cfg.TLS,
	}

	served := make(chan error, 1)

	go func() {
		if cfg.TLS != nil {
			served <- srv.ServeTLS(cfg.Listener, "", "")
			return
		}

		served <- srv.Serve(cfg.Listener)
	}()

	owned = true

	v := version.Get()

	s1Address, ngAddress := "", ""
	if cfg.S1Listener != nil {
		s1Address = cfg.S1Listener.Addr().String()
	}

	if cfg.NGListener != nil {
		ngAddress = cfg.NGListener.Addr().String()
	}

	cfg.Logger.InfoContext(ctx, "mocn started", slog.String("version", v.Version), slog.String("revision", v.Revision),
		slog.String("api", cfg.Listener.Addr().String()), slog.Bool("tls", cfg.TLS != nil), slog.String("s1", s1Address),
		slog.String("ng", ngAddress), slog.String("db", cfg.DBPath))

	var failed error

	select {
	case <-ctx.Done():
	case err := <-served:
		failed = fmt.Errorf("serve api: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		cfg.Logger.ErrorContext(shutdownCtx, "api shutdown", slog.Any("error", err))

		if err := srv.Close(); err != nil {
			cfg.Logger.ErrorContext(shutdownCtx, "api close", slog.Any("error", err))
		}
	}

	if failed != nil {
		return failed
	}

	cfg.Logger.InfoContext(shutdownCtx, "mocn stopped")

	return nil
}

func closeListeners(cfg Config) {
	if cfg.Listener != nil {
		_ = cfg.Listener.Close()
	}

	if cfg.S1Listener != nil {
		_ = cfg.S1Listener.Close()
	}

	if cfg.NGListener != nil {
		_ = cfg.NGListener.Close()
	}
}

func newTracerProvider(ctx context.Context, cfg Config) (trace.TracerProvider, func(context.Context) error, error) {
	none := func(context.Context) error { return nil }

	switch {
	case cfg.TracerProvider != nil:
		return cfg.TracerProvider, none, nil
	case !cfg.Telemetry.Enabled:
		return nil, none, nil
	}

	tp, err := tracing.NewProvider(ctx, cfg.Telemetry.OTLPEndpoint, version.Get().Version, cfg.Logger)
	if err != nil {
		return nil, nil, fmt.Errorf("start tracing: %w", err)
	}

	return tp, tp.Shutdown, nil
}

func readiness(database *db.DB) func(ctx context.Context) []api.Check {
	return func(ctx context.Context) []api.Check {
		check := api.Check{Name: CheckDatabase, OK: true}

		if err := database.Ping(ctx); err != nil {
			check = api.Check{Name: CheckDatabase, Detail: err.Error()}
		}

		return []api.Check{check}
	}
}

func newRegistry(database *db.DB, requests *api.RequestMetrics, gateways []prometheus.Collector) *prometheus.Registry {
	v := version.Get()

	build := prometheus.Labels{"version": v.Version}
	if v.Revision != "" {
		build["revision"] = v.Revision
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		versioncollector.NewCollector("ellamocn", versioncollector.WithExtraConstLabels(build)),
	)
	registry.MustRegister(database.Collectors()...)
	registry.MustRegister(requests.Collectors()...)
	registry.MustRegister(gateways...)

	return registry
}

type planeReconciler struct {
	plane *gtpu.Plane
	db    *db.DB
}

func (r planeReconciler) Reconcile(ctx context.Context) error {
	ops, err := r.db.ListOperators(ctx)
	if err != nil {
		return err
	}

	out := make([]gtpu.Operator, 0, len(ops))

	for _, o := range ops {
		out = append(out, gtpu.Operator{ID: o.ID, Name: o.Name, Aggregated: o.UserPlane.Aggregation && r.plane.Enabled()})
	}

	return r.plane.SetOperators(out)
}

type reconciler interface {
	Reconcile(ctx context.Context) error
}

type reconciling struct {
	*db.DB
	gateways []reconciler
	logger   *slog.Logger
}

func (r reconciling) reconcile(ctx context.Context) {
	for _, g := range r.gateways {
		if err := g.Reconcile(context.WithoutCancel(ctx)); err != nil {
			r.logger.ErrorContext(ctx, "failed to apply operator changes", slog.Any("error", err))
		}
	}
}

func (r reconciling) CreateOperator(ctx context.Context, o db.Operator) (int64, error) {
	id, err := r.DB.CreateOperator(ctx, o)
	if err == nil {
		r.reconcile(ctx)
	}

	return id, err
}

func (r reconciling) UpdateOperator(ctx context.Context, o db.Operator) error {
	err := r.DB.UpdateOperator(ctx, o)
	if err == nil {
		r.reconcile(ctx)
	}

	return err
}

func (r reconciling) DeleteOperator(ctx context.Context, id int64) error {
	err := r.DB.DeleteOperator(ctx, id)
	if err == nil {
		r.reconcile(ctx)
	}

	return err
}
