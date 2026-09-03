package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	t.Run("all required fields present", func(t *testing.T) {
		cfg := Config{
			DatabaseDSN:    "root@tcp(localhost:4000)/db",
			MCPAuthToken:   "secure-token",
			RunnerRPCToken: "runner-secret",
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
	t.Run("missing DatabaseDSN", func(t *testing.T) {
		cfg := Config{}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "DATABASE_DSN is required" {
			t.Errorf("expected DATABASE_DSN error, got %q", err.Error())
		}
	})
	t.Run("all missing", func(t *testing.T) {
		cfg := Config{}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "DATABASE_DSN is required" {
			t.Errorf("expected DATABASE_DSN as first error, got %q", err.Error())
		}
	})
	t.Run("missing MCPAuthToken", func(t *testing.T) {
		cfg := Config{DatabaseDSN: "root@tcp(localhost:4000)/db"}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "MCP_AUTH_TOKEN is required" {
			t.Errorf("expected MCP_AUTH_TOKEN error, got %q", err.Error())
		}
	})
	t.Run("placeholder MCPAuthToken", func(t *testing.T) {
		cfg := Config{
			DatabaseDSN:  "root@tcp(localhost:4000)/db",
			MCPAuthToken: "change-me-mcp-token",
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "MCP_AUTH_TOKEN must not use a placeholder value" {
			t.Errorf("expected placeholder MCP_AUTH_TOKEN error, got %q", err.Error())
		}
	})
}

func TestEnv(t *testing.T) {
	t.Run("env var set", func(t *testing.T) {
		t.Setenv("TEST_ENV_KEY", "myvalue")
		got := env("TEST_ENV_KEY", "fallback")
		if got != "myvalue" {
			t.Errorf("expected myvalue, got %q", got)
		}
	})
	t.Run("env var not set", func(t *testing.T) {
		got := env("TEST_ENV_MISSING_XYZ", "fallback")
		if got != "fallback" {
			t.Errorf("expected fallback, got %q", got)
		}
	})
}

func TestEnvBool(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		t.Setenv("TEST_BOOL_KEY", "true")
		got := envBool("TEST_BOOL_KEY", false)
		if got != true {
			t.Errorf("expected true, got %v", got)
		}
	})
	t.Run("false", func(t *testing.T) {
		t.Setenv("TEST_BOOL_KEY2", "false")
		got := envBool("TEST_BOOL_KEY2", true)
		if got != false {
			t.Errorf("expected false, got %v", got)
		}
	})
	t.Run("not set returns fallback", func(t *testing.T) {
		got := envBool("TEST_BOOL_MISSING_XYZ", true)
		if got != true {
			t.Errorf("expected fallback true, got %v", got)
		}
	})
	t.Run("invalid value returns fallback", func(t *testing.T) {
		t.Setenv("TEST_BOOL_KEY3", "notabool")
		got := envBool("TEST_BOOL_KEY3", false)
		if got != false {
			t.Errorf("expected fallback false for invalid, got %v", got)
		}
	})
}

func TestEnvInt(t *testing.T) {
	t.Run("valid integer", func(t *testing.T) {
		t.Setenv("TEST_INT_KEY", "42")
		got := envInt("TEST_INT_KEY", 0)
		if got != 42 {
			t.Errorf("expected 42, got %d", got)
		}
	})
	t.Run("zero", func(t *testing.T) {
		t.Setenv("TEST_INT_KEY2", "0")
		got := envInt("TEST_INT_KEY2", 10)
		if got != 0 {
			t.Errorf("expected 0, got %d", got)
		}
	})
	t.Run("not set returns fallback", func(t *testing.T) {
		got := envInt("TEST_INT_MISSING_XYZ", 99)
		if got != 99 {
			t.Errorf("expected fallback 99, got %d", got)
		}
	})
	t.Run("invalid value returns fallback", func(t *testing.T) {
		t.Setenv("TEST_INT_KEY3", "notanumber")
		got := envInt("TEST_INT_KEY3", 7)
		if got != 7 {
			t.Errorf("expected fallback 7 for invalid, got %d", got)
		}
	})
}

func TestEnvInt64(t *testing.T) {
	t.Run("valid int64", func(t *testing.T) {
		t.Setenv("TEST_INT64_KEY", "123456789012")
		got := envInt64("TEST_INT64_KEY", 0)
		if got != 123456789012 {
			t.Errorf("expected 123456789012, got %d", got)
		}
	})
	t.Run("invalid value returns fallback", func(t *testing.T) {
		t.Setenv("TEST_INT64_KEY2", "notanumber")
		got := envInt64("TEST_INT64_KEY2", 42)
		if got != 42 {
			t.Errorf("expected fallback 42, got %d", got)
		}
	})
	t.Run("not set returns fallback", func(t *testing.T) {
		got := envInt64("TEST_INT64_MISSING", 42)
		if got != 42 {
			t.Errorf("expected 42, got %d", got)
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("CHETTER_ALLOW_TOKEN_LOGIN", "")
		t.Setenv("CHETTER_METRICS_AUTH_TOKEN", "")
		cfg := Load()
		if cfg.HTTPAddr != ":8080" {
			t.Errorf("expected :8080, got %q", cfg.HTTPAddr)
		}
		if cfg.DefaultTaskTimeoutSec != 600 {
			t.Errorf("expected 600, got %d", cfg.DefaultTaskTimeoutSec)
		}
		if !cfg.AutoRecovery {
			t.Errorf("expected AutoRecovery default true, got false")
		}
		if !cfg.AllowTokenLogin {
			t.Error("expected AllowTokenLogin default true")
		}
		if cfg.MetricsAuthToken != "" {
			t.Errorf("expected empty MetricsAuthToken, got %q", cfg.MetricsAuthToken)
		}
	})
	t.Run("env overrides", func(t *testing.T) {
		t.Setenv("HTTP_ADDR", ":9090")
		t.Setenv("DEFAULT_AGENT_IMAGE", "custom:latest")
		t.Setenv("AGENT_IMAGE_PREFIX", "ghcr.io/example")
		t.Setenv("DEFAULT_TASK_TIMEOUT_SEC", "300")
		cfg := Load()
		if cfg.HTTPAddr != ":9090" {
			t.Errorf("expected :9090, got %q", cfg.HTTPAddr)
		}
		if cfg.DefaultAgentImage != "custom:latest" {
			t.Errorf("expected custom:latest, got %q", cfg.DefaultAgentImage)
		}
		if cfg.AgentImagePrefix != "ghcr.io/example" {
			t.Errorf("expected ghcr.io/example, got %q", cfg.AgentImagePrefix)
		}
		if cfg.DefaultTaskTimeoutSec != 300 {
			t.Errorf("expected 300, got %d", cfg.DefaultTaskTimeoutSec)
		}
		t.Setenv("DEFAULT_AUTO_RECOVERY", "false")
		cfg = Load()
		if cfg.AutoRecovery {
			t.Errorf("expected AutoRecovery false, got true")
		}
	})
	t.Run("security flags", func(t *testing.T) {
		t.Setenv("CHETTER_ALLOW_TOKEN_LOGIN", "false")
		t.Setenv("CHETTER_METRICS_AUTH_TOKEN", "metrics-secret")
		cfg := Load()
		if cfg.AllowTokenLogin {
			t.Error("expected AllowTokenLogin false")
		}
		if cfg.MetricsAuthToken != "metrics-secret" {
			t.Errorf("MetricsAuthToken = %q", cfg.MetricsAuthToken)
		}
	})
	t.Run("github fields", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("GITHUB_INSTALLATION_ID", "67890")
		t.Setenv("GITHUB_APP_PRIVATE_KEY_B64", "cHJpdmF0ZSBrZXk=")
		t.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
		t.Setenv("CHETTER_SELF_TEST_GITHUB_REPO", "flatout-works/diagnostics")
		cfg := Load()
		if cfg.GitHubAppID != 12345 {
			t.Errorf("expected 12345, got %d", cfg.GitHubAppID)
		}
		if cfg.GitHubInstallationID != 67890 {
			t.Errorf("expected 67890, got %d", cfg.GitHubInstallationID)
		}
		if cfg.GitHubAppPrivateKeyB64 != "cHJpdmF0ZSBrZXk=" {
			t.Errorf("private key mismatch")
		}
		if cfg.GitHubWebhookSecret != "secret123" {
			t.Errorf("webhook secret mismatch")
		}
		if cfg.SelfTestGitHubRepo != "flatout-works/diagnostics" {
			t.Errorf("self-test GitHub repo = %q", cfg.SelfTestGitHubRepo)
		}
	})
	t.Run("github not configured by default", func(t *testing.T) {
		cfg := Load()
		if cfg.GitHubWebhookConfigured() {
			t.Error("expected GitHub not configured")
		}
	})
}

func TestGitHubConfigurationPredicates(t *testing.T) {
	t.Run("app credentials do not require legacy installation", func(t *testing.T) {
		cfg := Config{GitHubAppID: 1, GitHubAppPrivateKeyB64: "key"}
		if !cfg.GitHubAppConfigured() {
			t.Error("expected app configured")
		}
	})
	t.Run("webhook ready without legacy installation", func(t *testing.T) {
		cfg := Config{
			GitHubWebhookSecret:    "secret",
			GitHubAppID:            1,
			GitHubAppPrivateKeyB64: "key",
		}
		if !cfg.GitHubWebhookConfigured() {
			t.Error("expected configured")
		}
	})
	t.Run("missing webhook secret", func(t *testing.T) {
		cfg := Config{
			GitHubAppID:            1,
			GitHubAppPrivateKeyB64: "key",
			GitHubInstallationID:   1,
		}
		if cfg.GitHubWebhookConfigured() {
			t.Error("expected not configured")
		}
	})
	t.Run("missing app id", func(t *testing.T) {
		cfg := Config{
			GitHubWebhookSecret:    "secret",
			GitHubAppPrivateKeyB64: "key",
			GitHubInstallationID:   1,
		}
		if cfg.GitHubWebhookConfigured() {
			t.Error("expected not configured")
		}
	})
	t.Run("disabled by flag", func(t *testing.T) {
		cfg := Config{
			GitHubWebhookDisabled:  true,
			GitHubWebhookSecret:    "secret",
			GitHubAppID:            1,
			GitHubAppPrivateKeyB64: "key",
			GitHubInstallationID:   1,
		}
		if cfg.GitHubWebhookConfigured() {
			t.Error("expected not configured when disabled")
		}
	})
}

func TestEnvDuration(t *testing.T) {
	t.Run("valid duration", func(t *testing.T) {
		t.Setenv("TEST_DUR_KEY", "2h30m")
		got := envDuration("TEST_DUR_KEY", 0)
		if got != 2*time.Hour+30*time.Minute {
			t.Errorf("expected 2h30m, got %v", got)
		}
	})
	t.Run("zero value", func(t *testing.T) {
		t.Setenv("TEST_DUR_KEY2", "0s")
		got := envDuration("TEST_DUR_KEY2", 24*time.Hour)
		if got != 0 {
			t.Errorf("expected 0s, got %v", got)
		}
	})
	t.Run("not set returns fallback", func(t *testing.T) {
		got := envDuration("TEST_DUR_MISSING_XYZ", 24*time.Hour)
		if got != 24*time.Hour {
			t.Errorf("expected fallback 24h, got %v", got)
		}
	})
	t.Run("invalid value returns fallback", func(t *testing.T) {
		t.Setenv("TEST_DUR_KEY3", "notaduration")
		got := envDuration("TEST_DUR_KEY3", 24*time.Hour)
		if got != 24*time.Hour {
			t.Errorf("expected fallback 24h for invalid, got %v", got)
		}
	})
}

func TestLoadSessionArtifactTTL(t *testing.T) {
	t.Run("defaults to 24h", func(t *testing.T) {
		cfg := Load()
		if cfg.SessionArtifactTTL != 24*time.Hour {
			t.Errorf("expected SessionArtifactTTL 24h, got %v", cfg.SessionArtifactTTL)
		}
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv("SESSION_ARTIFACT_TTL", "12h")
		cfg := Load()
		if cfg.SessionArtifactTTL != 12*time.Hour {
			t.Errorf("expected SessionArtifactTTL 12h, got %v", cfg.SessionArtifactTTL)
		}
	})
	t.Run("zero disables GC", func(t *testing.T) {
		t.Setenv("SESSION_ARTIFACT_TTL", "0s")
		cfg := Load()
		if cfg.SessionArtifactTTL != 0 {
			t.Errorf("expected SessionArtifactTTL 0, got %v", cfg.SessionArtifactTTL)
		}
	})
}

func TestLoadMaxPendingTasks(t *testing.T) {
	t.Run("defaults to zero (admission control disabled)", func(t *testing.T) {
		cfg := Load()
		if cfg.MaxPendingTasks != 0 {
			t.Errorf("expected MaxPendingTasks 0, got %d", cfg.MaxPendingTasks)
		}
	})
	t.Run("env override sets the limit", func(t *testing.T) {
		t.Setenv("CHETTER_MAX_PENDING_TASKS", "25")
		cfg := Load()
		if cfg.MaxPendingTasks != 25 {
			t.Errorf("expected MaxPendingTasks 25, got %d", cfg.MaxPendingTasks)
		}
	})
	t.Run("invalid value falls back to zero", func(t *testing.T) {
		t.Setenv("CHETTER_MAX_PENDING_TASKS", "notanumber")
		cfg := Load()
		if cfg.MaxPendingTasks != 0 {
			t.Errorf("expected 0 for invalid value, got %d", cfg.MaxPendingTasks)
		}
	})
}

func TestLoadCallbackMaxDepth(t *testing.T) {
	t.Run("defaults to five", func(t *testing.T) {
		cfg := Load()
		if cfg.CallbackMaxDepth != 5 {
			t.Errorf("expected CallbackMaxDepth 5, got %d", cfg.CallbackMaxDepth)
		}
	})
	t.Run("env override sets the limit", func(t *testing.T) {
		t.Setenv("CHETTER_CALLBACK_MAX_DEPTH", "3")
		cfg := Load()
		if cfg.CallbackMaxDepth != 3 {
			t.Errorf("expected CallbackMaxDepth 3, got %d", cfg.CallbackMaxDepth)
		}
	})
	t.Run("zero disables the guard", func(t *testing.T) {
		t.Setenv("CHETTER_CALLBACK_MAX_DEPTH", "0")
		cfg := Load()
		if cfg.CallbackMaxDepth != 0 {
			t.Errorf("expected CallbackMaxDepth 0, got %d", cfg.CallbackMaxDepth)
		}
	})
}

func TestLoadTaskMaxMemoryMB(t *testing.T) {
	t.Run("defaults to 4096", func(t *testing.T) {
		cfg := Load()
		if cfg.TaskMaxMemoryMB != 4096 {
			t.Errorf("expected TaskMaxMemoryMB 4096, got %d", cfg.TaskMaxMemoryMB)
		}
	})
	t.Run("env override sets the limit", func(t *testing.T) {
		t.Setenv("CHETTER_TASK_MAX_MEMORY_MB", "8192")
		cfg := Load()
		if cfg.TaskMaxMemoryMB != 8192 {
			t.Errorf("expected TaskMaxMemoryMB 8192, got %d", cfg.TaskMaxMemoryMB)
		}
	})
	t.Run("invalid value falls back to default", func(t *testing.T) {
		t.Setenv("CHETTER_TASK_MAX_MEMORY_MB", "huge")
		cfg := Load()
		if cfg.TaskMaxMemoryMB != 4096 {
			t.Errorf("expected TaskMaxMemoryMB 4096 for invalid value, got %d", cfg.TaskMaxMemoryMB)
		}
	})
}

func TestLoadRetention(t *testing.T) {
	t.Run("defaults to zero (pruning disabled)", func(t *testing.T) {
		cfg := Load()
		if cfg.EventsRetentionDays != 0 {
			t.Errorf("expected EventsRetentionDays 0, got %d", cfg.EventsRetentionDays)
		}
		if cfg.AuditRetentionDays != 0 {
			t.Errorf("expected AuditRetentionDays 0, got %d", cfg.AuditRetentionDays)
		}
		if cfg.ArtifactRetentionDays != 0 {
			t.Errorf("expected ArtifactRetentionDays 0, got %d", cfg.ArtifactRetentionDays)
		}
	})
	t.Run("env overrides enable pruning", func(t *testing.T) {
		t.Setenv("EVENTS_RETENTION_DAYS", "30")
		t.Setenv("AUDIT_RETENTION_DAYS", "90")
		t.Setenv("ARTIFACT_RETENTION_DAYS", "180")
		cfg := Load()
		if cfg.EventsRetentionDays != 30 {
			t.Errorf("expected EventsRetentionDays 30, got %d", cfg.EventsRetentionDays)
		}
		if cfg.AuditRetentionDays != 90 {
			t.Errorf("expected AuditRetentionDays 90, got %d", cfg.AuditRetentionDays)
		}
		if cfg.ArtifactRetentionDays != 180 {
			t.Errorf("expected ArtifactRetentionDays 180, got %d", cfg.ArtifactRetentionDays)
		}
	})
	t.Run("invalid value falls back to zero", func(t *testing.T) {
		t.Setenv("EVENTS_RETENTION_DAYS", "notanumber")
		cfg := Load()
		if cfg.EventsRetentionDays != 0 {
			t.Errorf("expected 0 for invalid value, got %d", cfg.EventsRetentionDays)
		}
	})
}

func TestLoadLogging(t *testing.T) {
	t.Run("defaults to info level and text format", func(t *testing.T) {
		cfg := Load()
		if cfg.Logging.Level != slog.LevelInfo {
			t.Errorf("expected default level info, got %v", cfg.Logging.Level)
		}
		if cfg.Logging.LevelRaw != "" {
			t.Errorf("expected empty LevelRaw, got %q", cfg.Logging.LevelRaw)
		}
		if cfg.Logging.Format != "text" {
			t.Errorf("expected default format text, got %q", cfg.Logging.Format)
		}
	})
	t.Run("env level overrides parse", func(t *testing.T) {
		for _, tc := range []struct {
			raw  string
			want slog.Level
		}{
			{"debug", slog.LevelDebug},
			{"info", slog.LevelInfo},
			{"warn", slog.LevelWarn},
			{"error", slog.LevelError},
			{"DEBUG", slog.LevelDebug},
		} {
			t.Setenv("CHETTER_LOG_LEVEL", tc.raw)
			cfg := Load()
			if cfg.Logging.Level != tc.want {
				t.Errorf("CHETTER_LOG_LEVEL=%q: expected level %v, got %v", tc.raw, tc.want, cfg.Logging.Level)
			}
			if cfg.Logging.LevelRaw != tc.raw {
				t.Errorf("CHETTER_LOG_LEVEL=%q: expected LevelRaw %q, got %q", tc.raw, tc.raw, cfg.Logging.LevelRaw)
			}
		}
	})
	t.Run("env format overrides parse case-insensitively", func(t *testing.T) {
		for _, raw := range []string{"json", "JSON", "  text  "} {
			t.Setenv("CHETTER_LOG_FORMAT", raw)
			cfg := Load()
			want := "text"
			if strings.ToLower(strings.TrimSpace(raw)) == "json" {
				want = "json"
			}
			if cfg.Logging.Format != want {
				t.Errorf("CHETTER_LOG_FORMAT=%q: expected format %q, got %q", raw, want, cfg.Logging.Format)
			}
		}
	})
	t.Run("invalid level keeps default but fails validation", func(t *testing.T) {
		t.Setenv("CHETTER_LOG_LEVEL", "verbose")
		cfg := Load()
		if cfg.Logging.Level != slog.LevelInfo {
			t.Errorf("expected fallback level info, got %v", cfg.Logging.Level)
		}
		base := Config{DatabaseDSN: "dsn", MCPAuthToken: "t", RunnerRPCToken: "r"}
		base.Logging = cfg.Logging
		err := base.Validate()
		if err == nil {
			t.Fatal("expected validation error for invalid CHETTER_LOG_LEVEL")
		}
		if !strings.Contains(err.Error(), "CHETTER_LOG_LEVEL") {
			t.Errorf("expected error to name CHETTER_LOG_LEVEL, got %q", err.Error())
		}
	})
	t.Run("invalid format keeps raw value but fails validation", func(t *testing.T) {
		t.Setenv("CHETTER_LOG_FORMAT", "xml")
		cfg := Load()
		if cfg.Logging.Format != "xml" {
			t.Errorf("expected raw format kept for validation, got %q", cfg.Logging.Format)
		}
		base := Config{DatabaseDSN: "dsn", MCPAuthToken: "t", RunnerRPCToken: "r"}
		base.Logging = cfg.Logging
		err := base.Validate()
		if err == nil {
			t.Fatal("expected validation error for invalid CHETTER_LOG_FORMAT")
		}
		if !strings.Contains(err.Error(), "CHETTER_LOG_FORMAT") {
			t.Errorf("expected error to name CHETTER_LOG_FORMAT, got %q", err.Error())
		}
	})
	t.Run("valid logging configuration passes validation", func(t *testing.T) {
		cfg := Config{
			DatabaseDSN:    "dsn",
			MCPAuthToken:   "t",
			RunnerRPCToken: "r",
			Logging: Logging{
				Level:  slog.LevelDebug,
				Format: "json",
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})
	t.Run("zero-value Logging passes validation", func(t *testing.T) {
		cfg := Config{
			DatabaseDSN:    "dsn",
			MCPAuthToken:   "t",
			RunnerRPCToken: "r",
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected nil error for zero Logging, got %v", err)
		}
	})
}
