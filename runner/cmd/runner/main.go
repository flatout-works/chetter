package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/controller"
)

var _gitHash = "unknown"

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "runner.yaml", "path to runner configuration")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	if cfg.Server.URL == "" {
		slog.Error("server.url is required for ConnectRPC mode")
		os.Exit(1)
	}

	runner, err := controller.NewRunner(cfg)
	if err != nil {
		slog.Error("runner init", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down...")
		cancel()
	}()

	slog.Info("runner starting", "build", _gitHash)
	if err := runner.Start(ctx); err != nil {
		slog.Error("runner start", "error", err)
		os.Exit(1)
	}
}
