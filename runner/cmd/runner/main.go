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
		slog.Info("shutting down — draining in-flight tasks")
		// Mark the runner draining and send a final "draining" heartbeat so the
		// server stops assigning new tasks and can reassign in-flight tasks
		// sooner. Then cancel the run context so claim loops stop and the
		// startConnectRPC shutdown path runs waitDrain. See issue #97.
		runner.BeginGracefulShutdown()
		cancel()
	}()

	slog.Info("runner starting", "build", _gitHash)
	if err := runner.Start(ctx); err != nil {
		slog.Error("runner start", "error", err)
		os.Exit(1)
	}
	// A forced termination (drain deadline expired with tasks still running)
	// exits non-zero so orchestrators (Kubernetes, CI) can distinguish a clean
	// drain from one that had to kill in-flight work. See issue #97.
	if runner.ForcedExit() {
		slog.Info("runner stopped after force-cancelling in-flight tasks")
		os.Exit(1)
	}
}
