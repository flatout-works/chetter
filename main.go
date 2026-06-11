package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/flatout-works/devfleet/internal/bus"
	"github.com/flatout-works/devfleet/internal/config"
	"github.com/flatout-works/devfleet/internal/service"
	"github.com/flatout-works/devfleet/internal/store"
	"github.com/flatout-works/devfleet/internal/webhook"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpServerName    = "devfleet"
	mcpServerVersion = "v0.1.0"
	initTimeout      = 30 * time.Second
	shutdownTimeout  = 15 * time.Second
	readHeaderTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("devfleet exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	if err := st.Ping(initCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	if err := st.ApplySchema(initCtx); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}

	nc, err := bus.Connect(cfg)
	if err != nil {
		return fmt.Errorf("connect bus: %w", err)
	}
	defer nc.Close()

	svc := service.New(cfg, st, nc)
	if err := svc.Start(ctx); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	defer svc.Stop()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion}, nil)
	service.RegisterTools(mcpServer, svc)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
		Logger:    slog.Default(),
	})

	whHandler := buildWebhookHandler(cfg, svc)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/mcp", authMiddleware(cfg.MCPAuthToken, mcpHandler))
	if whHandler != nil {
		mux.Handle("/webhook/github", whHandler)
		slog.Info("github webhook handler registered", "path", "/webhook/github")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("http shutdown", "error", err)
		}
	}()

	slog.Info("devfleet MCP server listening", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	return nil
}

func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if token != "" {
			auth := req.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

// buildWebhookHandler constructs the GitHub webhook handler. Returns nil if
// the GitHub App is not configured (in which case the route is not
// registered).
func buildWebhookHandler(cfg config.Config, svc *service.Service) http.Handler {
	if !cfg.GitHubConfigured() {
		slog.Info("github webhook not configured (missing GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_B64, GITHUB_INSTALLATION_ID, or GITHUB_WEBHOOK_SECRET)")
		return nil
	}
	gh, err := webhook.NewClient(cfg.GitHubAppID, cfg.GitHubInstallationID, cfg.GitHubAppPrivateKeyB64)
	if err != nil {
		slog.Error("github webhook: create client", "err", err)
		return nil
	}
	submitter := webhook.NewServiceSubmitter(&serviceSubmitterAdapter{svc: svc})
	return webhook.NewHandler(webhook.HandlerConfig{
		Disabled:           cfg.GitHubWebhookDisabled,
		WebhookSecret:      cfg.GitHubWebhookSecret,
		ReviewerAgent:      "pr-reviewer",
		ReviewerProviderID: "opencode-go",
		ReviewerModelID:    "minimax-m3",
		ReviewerTimeoutSec: 3600,
		AllowedRepos:       cfg.GitHubReviewAllowedRepos,
	}, gh, submitter)
}

// serviceSubmitterAdapter adapts service.Service to webhook.TaskSubmitterService.
type serviceSubmitterAdapter struct {
	svc *service.Service
}

// SubmitTask converts the webhook-side SubmitTaskRequest to the service-side
// format and calls service.SubmitTask. The TaskRecord return value is ignored.
func (a *serviceSubmitterAdapter) SubmitTask(ctx context.Context, req webhook.SubmitTaskRequest) (any, error) {
	return a.svc.SubmitTask(ctx, service.SubmitTaskRequest{
		Prompt:     req.Prompt,
		GitURL:     req.GitURL,
		GitRef:     req.GitRef,
		AgentImage: req.AgentImage,
		Agent:      req.Agent,
		ProviderID: req.ProviderID,
		ModelID:    req.ModelID,
		VariantID:  req.VariantID,
		Skills:     req.Skills,
		Env:        req.Env,
		TimeoutSec: req.TimeoutSec,
	})
}
