package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/ellanetworks/mocn/internal/config"
	"github.com/ellanetworks/mocn/internal/logging"
	"github.com/ellanetworks/mocn/internal/server"
	"github.com/ellanetworks/mocn/ui"
)

func main() {
	configPath := flag.String("config", "mocn.yaml", "path to the configuration file")

	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logging.New(os.Stdout, slog.LevelInfo).Error("mocn stopped", "error", err)
		os.Exit(1)
	}

	logger := logging.New(os.Stdout, cfg.Logging.Level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := start(ctx, cfg, logger); err != nil {
		logger.ErrorContext(ctx, "mocn stopped", "error", err)
		os.Exit(1)
	}
}

func start(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", cfg.API.ListenAddress())
	if err != nil {
		return fmt.Errorf("listen api: %w", err)
	}

	return server.Run(ctx, server.Config{Listener: ln, DBPath: cfg.DB.Path, Frontend: ui.FS(), Logger: logger})
}
