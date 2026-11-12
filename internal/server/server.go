package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ellanetworks/mocn/internal/api"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
)

const (
	shutdownTimeout   = 5 * time.Second
	readHeaderTimeout = 10 * time.Second
	CheckDatabase     = "database"
)

type Config struct {
	Listener   net.Listener
	DBPath     string
	Frontend   fs.FS
	BcryptCost int
	Logger     *slog.Logger
}

func Run(ctx context.Context, cfg Config) (err error) {
	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		_ = cfg.Listener.Close()
		return err
	}

	defer func() {
		if cerr := database.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close database: %w", cerr))
		}
	}()

	secret, err := database.JWTSecret(ctx)
	if err != nil {
		_ = cfg.Listener.Close()
		return fmt.Errorf("read the JWT secret: %w", err)
	}

	requests := api.NewRequestMetrics()

	srv := &http.Server{
		Handler: api.NewHandler(api.Config{
			Frontend:   cfg.Frontend,
			Ready:      readiness(database),
			Metrics:    newRegistry(database, requests),
			Requests:   requests,
			Auth:       database,
			JWTSecret:  func() []byte { return secret },
			BcryptCost: cfg.BcryptCost,
			Now:        time.Now,
			Logger:     cfg.Logger,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	served := make(chan error, 1)

	go func() {
		served <- srv.Serve(cfg.Listener)
	}()

	v := version.Get()

	cfg.Logger.InfoContext(ctx, "mocn started", "version", v.Version, "revision", v.Revision,
		"api", cfg.Listener.Addr().String(), "db", cfg.DBPath)

	var failed error

	select {
	case <-ctx.Done():
	case err := <-served:
		failed = fmt.Errorf("serve api: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		cfg.Logger.ErrorContext(shutdownCtx, "api shutdown", "error", err)
	}

	if failed != nil {
		return failed
	}

	cfg.Logger.InfoContext(shutdownCtx, "mocn stopped")

	return nil
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

func newRegistry(database *db.DB, requests *api.RequestMetrics) *prometheus.Registry {
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

	return registry
}
