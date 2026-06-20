package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/flatout-works/chetter/gen/proto/runner/v1/runnerv1connect"
	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/service"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/webhook"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpServerName     = "chetter"
	mcpServerVersion  = "v0.1.0"
	initTimeout       = 30 * time.Second
	shutdownTimeout   = 15 * time.Second
	readHeaderTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("chetter exited", "error", err)
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

	svc := service.New(cfg, st)
	if cfg.GitHubAppConfigured() {
		gh, err := webhook.NewClient(cfg.GitHubAppID, cfg.GitHubInstallationID, cfg.GitHubAppPrivateKeyB64)
		if err != nil {
			return fmt.Errorf("configure github app client: %w", err)
		}
		svc.SetGitHubClient(gh)
	}
	runnerSvc := service.NewRunnerRPCService(repository.New(st.DB()), st.DB())
	svc.SetRunnerRPC(runnerSvc)
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
	mux.Handle("/mcp", authMiddleware(cfg.MCPAuthToken, st.DB(), mcpHandler))
	runnerPath, runnerHandler := runnerv1connect.NewRunnerServiceHandler(runnerSvc)
	mux.Handle(runnerPath, runnerRPCAuthMiddleware(cfg.RunnerRPCToken, runnerHandler))
	mux.Handle("/api/v1/", authMiddleware(cfg.MCPAuthToken, st.DB(), svc.TokenAPIHandler()))
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

	slog.Info("chetter MCP server listening", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	return nil
}

func authMiddleware(adminToken string, db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authHeader := req.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		provided := strings.TrimPrefix(authHeader, "Bearer ")
		if adminToken != "" && provided == adminToken {
			next.ServeHTTP(w, req.WithContext(
				auth.WithScope(req.Context(), auth.Scope{Admin: true}),
			))
			return
		}
		if db != nil {
			scope := lookupTokenScope(req.Context(), db, provided)
			if scope.TeamID != "" {
				next.ServeHTTP(w, req.WithContext(
					auth.WithScope(req.Context(), scope),
				))
				return
			}
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func lookupTokenScope(ctx context.Context, db *sql.DB, rawToken string) auth.Scope {
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])
	repo := repository.New(db)
	row, err := repo.GetTokenByHash(ctx, tokenHash)
	if err != nil {
		return auth.Scope{}
	}
	return auth.Scope{TeamID: row.TeamID}
}

// runnerRPCAuthMiddleware validates only the dedicated runner RPC token.
// Regular team-scoped API tokens and the admin MCP token are rejected.
func runnerRPCAuthMiddleware(runnerToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authHeader := req.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		provided := strings.TrimPrefix(authHeader, "Bearer ")
		if runnerToken == "" || provided != runnerToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// buildWebhookHandler constructs the GitHub webhook handler. Returns nil if
// the GitHub App is not configured (in which case the route is not
// registered).
func buildWebhookHandler(cfg config.Config, svc *service.Service) http.Handler {
	if !cfg.GitHubConfigured() {
		slog.Info("github webhook not configured (missing GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_B64, GITHUB_INSTALLATION_ID, or GITHUB_WEBHOOK_SECRET); skipping /webhook/github route")
		return nil
	}
	gh, err := webhook.NewClient(cfg.GitHubAppID, cfg.GitHubInstallationID, cfg.GitHubAppPrivateKeyB64)
	if err != nil {
		slog.Error("github webhook: create client", "err", err)
		return nil
	}
	submitter := webhook.NewServiceSubmitter(&serviceSubmitterAdapter{svc: svc})
	resumer := &sessionResumerAdapter{svc: svc}
	return webhook.NewHandler(webhook.HandlerConfig{
		Disabled:      cfg.GitHubWebhookDisabled,
		WebhookSecret: cfg.GitHubWebhookSecret,
	}, gh, submitter, svc, &auditLoggerAdapter{svc: svc}, &artifactRecorderAdapter{svc: svc}, resumer)
}

type auditLoggerAdapter struct{ svc *service.Service }

func (a *auditLoggerAdapter) LogAuditEvent(ctx context.Context, params webhook.AuditEventParams) error {
	return a.svc.LogAuditEvent(ctx, service.AuditEventParams{
		EventType:        params.EventType,
		SourceType:       params.SourceType,
		SourceID:         params.SourceID,
		TargetType:       params.TargetType,
		TargetID:         params.TargetID,
		Repo:             params.Repo,
		GitHubEvent:      params.GitHubEvent,
		GitHubAction:     params.GitHubAction,
		GitHubDeliveryID: params.GitHubDeliveryID,
		ParentEventID:    params.ParentEventID,
		Detail:           params.Detail,
		Payload:          params.Payload,
	})
}

type artifactRecorderAdapter struct{ svc *service.Service }

func (a *artifactRecorderAdapter) RecordArtifact(ctx context.Context, params webhook.RecordArtifactParams) error {
	return a.svc.RecordArtifact(ctx, service.RecordArtifactParams{
		TaskID:          params.TaskID,
		AgentSessionID:  params.AgentSessionID,
		SessionRunID:    params.SessionRunID,
		ArtifactType:    params.ArtifactType,
		Repo:            params.Repo,
		Number:          params.Number,
		URL:             params.URL,
		Ref:             params.Ref,
		SHA:             params.SHA,
		DiscoverySource: params.DiscoverySource,
	})
}

type sessionResumerAdapter struct{ svc *service.Service }

func (a *sessionResumerAdapter) ResumeSessionForPR(ctx context.Context, repo string, prNumber int) error {
	return a.svc.ResumeSessionForPR(ctx, repo, prNumber)
}

// serviceSubmitterAdapter adapts service.Service to webhook.TaskSubmitterService.
type serviceSubmitterAdapter struct {
	svc *service.Service
}

// SubmitTask converts the webhook-side SubmitTaskRequest to the service-side
// format and calls service.SubmitTask. The TaskRecord return value is ignored.
func (a *serviceSubmitterAdapter) SubmitTask(ctx context.Context, req webhook.SubmitTaskRequest) (any, error) {
	return a.svc.SubmitTask(ctx, service.SubmitTaskRequest{
		Prompt:      req.Prompt,
		GitURL:      req.GitURL,
		GitRef:      req.GitRef,
		AgentImage:  req.AgentImage,
		Agent:       req.Agent,
		ProviderID:  req.ProviderID,
		ModelID:     req.ModelID,
		VariantID:   req.VariantID,
		Skills:      req.Skills,
		Env:         req.Env,
		TimeoutSec:  req.TimeoutSec,
		TriggerName: req.TriggerName,
		TriggerType: req.TriggerType,
	})
}
