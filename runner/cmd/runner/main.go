// runner is the agent runner daemon. It listens for task requests
// on NATS, provisions isolated workspaces, spawns agents inside Kata
// Containers (or directly in local mode), and publishes results.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/flatout-works/devfleet/runner/internal/config"
	"github.com/flatout-works/devfleet/runner/internal/controller"
	"github.com/flatout-works/devfleet/runner/internal/nats"
	"github.com/flatout-works/devfleet/runner/internal/nats/embedded"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "runner.yaml", "path to runner configuration")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	if cfg.EmbeddedNATS {
		natsServer, err := embedded.Start()
		if err != nil {
			slog.Error("embedded nats", "error", err)
			os.Exit(1)
		}
		defer natsServer.Close()
		cfg.NATS.URL = natsServer.ClientURL()
		portFile := filepath.Join(filepath.Dir(configPath), ".nats-port")
		if err := os.WriteFile(portFile, []byte(strconv.Itoa(natsServer.Port)), 0644); err != nil {
			slog.Warn("write nats port file", "error", err)
		}
		defer os.Remove(portFile)
	}

	nc, err := nats.Connect(cfg.NATS.URL)
	if err != nil {
		slog.Error("nats connect", "error", err)
		os.Exit(1)
	}
	defer nc.Conn.Close()

	runner, err := controller.NewRunner(cfg, nc)
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

	slog.Info("runner starting")
	if err := runner.Start(ctx); err != nil {
		slog.Error("runner start", "error", err)
		os.Exit(1)
	}
}
