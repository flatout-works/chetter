package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/flatout-works/chetter/gen/proto/runner/v1/runnerv1connect"
	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/metrics"
	"github.com/flatout-works/chetter/internal/service"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/webapi"
	"github.com/flatout-works/chetter/internal/webhook"
	"github.com/flatout-works/chetter/internal/webui"
	"github.com/flatout-works/chetter/pkg/definitions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	startupPingTimeout  = 60 * time.Second
	startupRetryBackoff = 2 * time.Second
)

var (
	// serverVersion is overridden at build time via
	// -X 'main.serverVersion=...' (see Makefile VERSION).
	serverVersion = "dev"
	_gitHash      = "unknown"
	// startedAt marks process start, used for /api/server-info uptime.
	startedAt = time.Now()
)

const (
	mcpServerName     = "chetter"
	mcpServerVersion  = "v0.1.0"
	initTimeout       = 30 * time.Second
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

	shutdownTimeout := envDuration("CHETTER_SHUTDOWN_TIMEOUT", 15*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Register the second-signal watcher only after the first signal cancels
	// ctx. This prevents the first signal from being mistaken for the force
	// exit signal. See issue #99 criterion 2.
	forceExitCh := make(chan os.Signal, 1)
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		signal.Notify(forceExitCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(forceExitCh)
		select {
		case <-forceExitCh:
			slog.Warn("graceful shutdown: second signal received, force-exiting")
			os.Exit(1)
		case <-shutdownDone:
		}
	}()
	defer close(shutdownDone)

	st, err := store.Open(cfg.DatabaseDSN, store.ParseDialect(cfg.DBDialect))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	// Ping the database with retry on transient errors for up to
	// startupPingTimeout. This lets the server ride through brief TiDB
	// leader transfers or PostgreSQL restart without manual recovery.
	// Non-transient errors (bad credentials, missing database) fail fast.
	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	if err := pingWithRetry(ctx, st); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	if err := st.ApplySchema(initCtx); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	schemaReady := true

	// Surface the session/global time zone captured and verified by the
	// preflight inside store.Open (issue #316) so server-info reports
	// exactly what was verified at startup. PostgreSQL carries its observed
	// TimeZone setting; it is exempt from the UTC gate (TIMESTAMPTZ is
	// zone-independent).
	dbTZSession, dbTZGlobal := st.VerifiedSessionTimeZone()
	dbTZUTC := store.IsUTCTimeZone(dbTZSession, dbTZGlobal)

	var defs *definitions.Manager
	if cfg.DefinitionsRepo != "" {
		defs = definitions.New(cfg.DefinitionsRepo, cfg.DefinitionsBranch, "")
	}

	svc := service.New(cfg, st)
	var githubManager *webhook.Manager
	if defs != nil {
		svc.SetDefinitions(defs)
		if _, err := svc.SyncDefinitions(ctx); err != nil {
			slog.Warn("definitions sync failed (continuing with active DB or built-in catalog)", "err", err)
		}
	}
	if cfg.GitHubAppConfigured() {
		githubManager, err = newGitHubManager(cfg)
		if err != nil {
			return fmt.Errorf("configure github app manager: %w", err)
		}
		svc.SetGitHubManager(githubManager)
	}
	eventBus := webapi.NewEventBus()
	repo := data.New(st.DB(), st.Dialect())
	runnerSvc := service.NewRunnerRPCService(repo, st.DB(), st.Dialect()).WithEventBus(eventBus).WithEventCallbacks(svc).WithGitHubActions(svc).WithSecurityAuditLogger(svc)
	svc.SetRunnerRPC(runnerSvc)
	if err := svc.Start(ctx); err != nil {
		return fmt.Errorf("start service: %w", err)
	}

	// One fleet-cursor poller per server replica: publishes task activity
	// committed by other replicas into the local event bus so fleet streams
	// stay current in multi-replica deployments. Stops with the service.
	webapi.StartFleetCursorPoller(svc.ReaperStopCh(), svc, eventBus)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion}, nil)
	service.RegisterTools(mcpServer, svc)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       slog.Default(),
	})

	whHandler := buildWebhookHandler(cfg, svc, githubManager, service.NewWebhookDeliveryStoreAdapter(st.DB(), st.Dialect(), svc.LogAuditEvent))

	// Start the webhook delivery retry worker. It retries failed deliveries
	// with exponential backoff (1s/5s/15s) and dead-letters them after 3
	// attempts. The worker runs until the service's reaperStop channel is
	// closed (on shutdown). See issue #102.
	var deliveryWorker *service.WebhookDeliveryWorker
	if whHandler != nil {
		deliveryWorker = service.NewWebhookDeliveryWorker(st.DB(), st.Dialect(), whHandler, svc.ReaperStopCh())
		deliveryWorker.Start()
		slog.Info("webhook delivery retry worker started")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/metrics", metrics.Handler(st.DB(), st.Dialect()))

	// readyz reports ready only when the schema is applied, the database is
	// reachable, and (for TiDB/MySQL) the database session time zone is
	// still UTC (issue #316). A drifted session would otherwise silently
	// skew fleet presence and reaper age math. PostgreSQL is exempt: its
	// TIMESTAMPTZ arithmetic is zone-independent.
	readyzHandler := func(w http.ResponseWriter, _ *http.Request) {
		if !schemaReady {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: schema not applied\n"))
			return
		}
		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := st.Ping(pingCtx); err != nil {
			slog.Warn("readiness check: database ping failed", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: database unreachable\n"))
			return
		}
		if !st.IsPostgres() {
			if _, _, err := st.VerifyUTCSession(pingCtx); err != nil {
				slog.Warn("readiness check: database session time zone is not UTC", "error", err)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("not ready: database session time zone is not UTC\n"))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}
	mux.HandleFunc("/readyz", readyzHandler)
	mux.Handle("/mcp", authMiddleware(cfg.MCPAuthToken, st.DB(), mcpHandler))
	runnerPath, runnerHandler := runnerv1connect.NewRunnerServiceHandler(runnerSvc)
	mux.Handle(runnerPath, runnerRPCAuthMiddleware(cfg.RunnerRPCToken, runnerHandler))
	if whHandler != nil {
		mux.Handle("/webhook/github", whHandler)
		slog.Info("github webhook handler registered", "path", "/webhook/github")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// Trigger MCP/runner server shutdown when the signal context is
	// cancelled. This causes server.ListenAndServe below to return
	// (http.ErrServerClosed) so the explicit shutdown sequence can proceed.
	go func() {
		<-ctx.Done()
		slog.Info("graceful shutdown: signal received, stopping MCP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("graceful shutdown: MCP server drain incomplete", "error", err)
		}
		cancel()
	}()

	// Web API + UI server
	webMux := http.NewServeMux()
	webHandlers := webapi.NewHandlers(svc, eventBus)

	// OIDC web UI SSO (issue #94). Provider discovery runs at startup so a
	// misconfigured or unreachable IdP fails fast.
	var oidcAuth *auth.OIDCAuth
	if cfg.OIDCConfigured() {
		oidcAuth, err = auth.NewOIDCAuth(ctx, cfg.OIDCConfig())
		if err != nil {
			return fmt.Errorf("configure oidc: %w", err)
		}
		slog.Info("oidc web auth configured", "issuer", cfg.OIDCIssuerURL, "redirect_url", cfg.OIDCRedirectURL)
	}
	webapi.RegisterHandlers(webMux, webHandlers, cfg.MCPAuthToken, st.DB(), oidcAuth, repo)
	webMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	webMux.HandleFunc("/readyz", readyzHandler)
	webMux.HandleFunc("GET /api/server-info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		lastReap := svc.LastReapAt()
		lastReapField := "null"
		if !lastReap.IsZero() {
			lastReapField = fmt.Sprintf("%q", lastReap.Format(time.RFC3339Nano))
		}
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"serverVersion":%q,"gitHash":%q,"uptimeSeconds":%d,"startedAt":%q,"quotaExhausted":%t,"lastReapAt":%s,"oidcEnabled":%t,"dbSessionTimeZone":%q,"dbGlobalTimeZone":%q,"dbTimeZoneUTC":%t}`,
			serverVersion, _gitHash, int64(time.Since(startedAt).Seconds()), startedAt.UTC().Format(time.RFC3339),
			svc.QuotaExhausted(), lastReapField, oidcAuth != nil,
			dbTZSession, dbTZGlobal, dbTZUTC,
		)))
	})
	webMux.Handle("/", webui.Handler())

	webServer := &http.Server{
		Addr:              cfg.WebAddr,
		Handler:           webMux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	webListener, err := net.Listen("tcp", cfg.WebAddr)
	if err != nil {
		return fmt.Errorf("listen web api: %w", err)
	}

	slog.Info("chetter MCP server listening", "addr", cfg.HTTPAddr)
	slog.Info("chetter web API listening", "addr", cfg.WebAddr)
	go func() {
		if err := webServer.Serve(webListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("web server error", "error", err)
		}
	}()

	// Block until SIGTERM/SIGINT or a fatal serve error.
	serveErr := server.ListenAndServe()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", serveErr)
	}

	// ── Graceful shutdown sequence (issue #99) ───────────────────────────
	// The signal context (ctx) was cancelled by the first SIGTERM/SIGINT.
	// server.Shutdown was triggered by the context-cancellation goroutine
	// above and has already returned (ListenAndServe returned). Now we
	// perform the remaining steps in order, with a deadline and logging at
	// each stage so operators can observe progress.

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	// Step 1: Stop accepting new web API connections.
	slog.Info("graceful shutdown: stopping web server")
	if err := webServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("graceful shutdown: web server drain incomplete", "error", err)
	}

	// Step 2: Drain in-flight webhook processing goroutines before closing
	// the database. The HTTP handler goroutines have already returned
	// (they respond 200 immediately then spawn a tracked goroutine), so
	// server.Shutdown only waited for the handler itself — not the async
	// event processing. See issue #57.
	if whHandler != nil {
		slog.Info("graceful shutdown: draining webhook handler")
		if err := whHandler.Shutdown(shutdownCtx); err != nil {
			slog.Warn("graceful shutdown: webhook handler drain incomplete", "error", err)
		}
	}

	// Step 3: Stop the reaper, cron, and definitions sync loop. The reaper
	// uses the shutdown context for its DB operations, so in-flight cycles
	// abort early. See issue #99 criterion 4.
	slog.Info("graceful shutdown: stopping service (reaper, cron, sync)")
	svc.Stop()
	eventBus.CloseAll()
	if deliveryWorker != nil {
		slog.Info("graceful shutdown: waiting for webhook delivery worker")
		if err := deliveryWorker.Shutdown(shutdownCtx); err != nil {
			slog.Warn("graceful shutdown: webhook delivery worker did not stop", "error", err)
		}
	}

	// Step 4: Close the database connection pool.
	slog.Info("graceful shutdown: closing database")
	if err := st.Close(); err != nil {
		slog.Warn("graceful shutdown: database close error", "error", err)
	}

	slog.Info("graceful shutdown: complete")
	return nil
}

// pingWithRetry pings the database with exponential backoff for transient
// errors (connection refused, leader change, etc.) for up to
// startupPingTimeout. Non-transient errors are returned immediately.
func pingWithRetry(ctx context.Context, st *store.Store) error {
	deadline := time.Now().Add(startupPingTimeout)
	var lastErr error
	backoff := startupRetryBackoff
	for attempt := 0; ; attempt++ {
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("database startup ping failed after %v: %w", startupPingTimeout, lastErr)
			}
			return fmt.Errorf("database startup ping timed out after %v", startupPingTimeout)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := st.Ping(pingCtx)
		cancel()
		if err == nil {
			if attempt > 0 {
				slog.Info("database startup ping recovered after transient errors",
					"attempt", attempt)
			}
			return nil
		}

		if !store.IsTransientError(err) {
			return fmt.Errorf("database startup ping failed (non-transient): %w", err)
		}

		lastErr = err
		slog.Warn("database startup ping transient error, retrying",
			"attempt", attempt+1,
			"backoff", backoff,
			"deadline", deadline.Format(time.RFC3339),
			"error", err,
		)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		// Cap exponential growth at 15 seconds.
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func authMiddleware(adminToken string, db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authHeader := req.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		provided := strings.TrimPrefix(authHeader, "Bearer ")
		scope, ok := auth.ResolveToken(req.Context(), adminToken, db, provided)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req.WithContext(
			auth.WithScope(req.Context(), scope),
		))
	})
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
// registered). When deliveryStore is non-nil, deliveries are persisted for
// idempotency, retry, and status tracking. See issue #102.
func buildWebhookHandler(cfg config.Config, svc *service.Service, githubManager *webhook.Manager, deliveryStore webhook.DeliveryStore) *webhook.Handler {
	if !cfg.GitHubWebhookConfigured() {
		slog.Info("github webhook not configured (missing GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_B64, or GITHUB_WEBHOOK_SECRET, or disabled); skipping /webhook/github route")
		return nil
	}
	if githubManager == nil {
		slog.Error("github webhook: app manager is unavailable")
		return nil
	}
	submitter := webhook.NewServiceSubmitter(&serviceSubmitterAdapter{svc: svc})
	resumer := &sessionResumerAdapter{svc: svc}
	return webhook.NewHandler(webhook.HandlerConfig{
		Disabled:      cfg.GitHubWebhookDisabled,
		WebhookSecret: cfg.GitHubWebhookSecret,
	}, githubManager, submitter, svc, &auditLoggerAdapter{svc: svc}, &artifactRecorderAdapter{svc: svc}, resumer, deliveryStore)
}

func newGitHubManager(cfg config.Config) (*webhook.Manager, error) {
	options := make([]webhook.ManagerOption, 0, 1)
	if cfg.GitHubInstallationID > 0 {
		options = append(options, webhook.WithLegacyInstallationID(cfg.GitHubInstallationID))
	}
	return webhook.NewManager(cfg.GitHubAppID, cfg.GitHubAppPrivateKeyB64, options...)
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
		TaskID:             params.TaskID,
		AgentSessionID:     params.AgentSessionID,
		UserPromptID:       params.UserPromptID,
		ExecutionAttemptID: params.ExecutionAttemptID,
		ArtifactType:       params.ArtifactType,
		Repo:               params.Repo,
		Number:             params.Number,
		URL:                params.URL,
		Ref:                params.Ref,
		SHA:                params.SHA,
		DiscoverySource:    params.DiscoverySource,
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
		TeamID:               req.TeamID,
		Prompt:               req.Prompt,
		GitURL:               req.GitURL,
		GitRef:               req.GitRef,
		GitHubRepo:           req.GitHubRepo,
		GitHubInstallationID: req.GitHubInstallationID,
		AgentImage:           req.AgentImage,
		Agent:                req.Agent,
		ProviderID:           req.ProviderID,
		ModelID:              req.ModelID,
		VariantID:            req.VariantID,
		Skills:               req.Skills,
		Env:                  req.Env,
		TimeoutSec:           req.TimeoutSec,
		TriggerName:          req.TriggerName,
		TriggerType:          req.TriggerType,
		SubmissionSource:     "trigger",
		SessionMode:          req.SessionMode,
		PauseReason:          req.PauseReason,
		TTLHours:             req.TTLHours,
		Isolation:            req.Isolation,
	})
}

// envDuration reads a duration from an environment variable, falling back to
// the provided default if unset or unparseable.
func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid duration, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return d
}
