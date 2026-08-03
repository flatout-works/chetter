// Package config loads chetter service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/validation"
)

// Config holds all runtime settings for the chetter MCP service.
type Config struct {
	HTTPAddr               string
	WebAddr                string
	MCPAuthToken           string
	RunnerRPCToken         string
	DatabaseDSN            string
	DBDialect              string
	DefaultAgentImage      string
	AgentImagePrefix       string
	DefaultTaskTimeoutSec  int
	AutoRecovery           bool
	ArcaneServerURL        string
	ArcaneAPIKey           string
	GitHubAppID            int64
	GitHubAppPrivateKeyB64 string
	GitHubWebhookSecret    string
	GitHubWebhookDisabled  bool
	GitHubInstallationID   int64
	SelfTestGitHubRepo     string
	DefinitionsRepo        string
	DefinitionsBranch      string
	WebURL                 string
	// Retention TTLs (in days) for the reaper's storage pruner. A value of 0
	// disables pruning for that table, so existing deployments are unaffected
	// until an operator opts in. See issue #112.
	EventsRetentionDays   int
	AuditRetentionDays    int
	ArtifactRetentionDays int
	// EnvValidation configures task environment variable validation at submission
	// time. See env var CHETTER_ENV_* and internal/validation.
	EnvValidation validation.Config

	// TaskLimits configures deterministic task/trigger field validation
	// (harness, session mode, timeout/TTL bounds) applied centrally at
	// submission time. See env vars CHETTER_MAX_TASK_TIMEOUT_SEC and
	// CHETTER_MAX_SESSION_TTL_HOURS.
	TaskLimits validation.TaskLimits

	// SessionArtifactTTL is the duration after which terminal-session on-disk
	// artifacts (checkpoints, session exports) are eligible for garbage
	// collection by the reaper. A value of 0 disables GC. Default: 24h.
	SessionArtifactTTL time.Duration

	// AllowUnisolated is the documented escape hatch for single-tenant /
	// trusted deployments that intentionally run without gVisor. When true,
	// tasks are only marked isolation-requiring when explicitly configured
	// (isolation: required) or resumable; when false (default, hardened mode),
	// every task requires enforced isolation and is refused by runners that
	// cannot enforce it. See issue #291 and CHETTER_ALLOW_UNISOLATED.
	AllowUnisolated bool
}

// Load returns configuration using environment variables and safe defaults.
func Load() Config {
	return Config{
		HTTPAddr:               env("HTTP_ADDR", ":8080"),
		WebAddr:                env("WEB_ADDR", ":8090"),
		MCPAuthToken:           os.Getenv("MCP_AUTH_TOKEN"),
		RunnerRPCToken:         os.Getenv("CHETTER_RUNNER_RPC_TOKEN"),
		DatabaseDSN:            os.Getenv("DATABASE_DSN"),
		DBDialect:              os.Getenv("CHETTER_DB_DIALECT"),
		DefaultAgentImage:      env("DEFAULT_AGENT_IMAGE", "ghcr.io/flatout-works/chetter-agent-base:latest"),
		AgentImagePrefix:       os.Getenv("AGENT_IMAGE_PREFIX"),
		DefaultTaskTimeoutSec:  envInt("DEFAULT_TASK_TIMEOUT_SEC", 600),
		AutoRecovery:           envBool("DEFAULT_AUTO_RECOVERY", true),
		ArcaneServerURL:        env("ARCANE_SERVER_URL", ""),
		ArcaneAPIKey:           env("ARCANE_API_KEY", ""),
		GitHubAppID:            envInt64("GITHUB_APP_ID", 0),
		GitHubAppPrivateKeyB64: os.Getenv("GITHUB_APP_PRIVATE_KEY_B64"),
		GitHubWebhookSecret:    os.Getenv("GITHUB_WEBHOOK_SECRET"),
		GitHubWebhookDisabled:  envBool("GITHUB_WEBHOOK_DISABLED", false),
		GitHubInstallationID:   envInt64("GITHUB_INSTALLATION_ID", 0),
		SelfTestGitHubRepo:     os.Getenv("CHETTER_SELF_TEST_GITHUB_REPO"),
		DefinitionsRepo:        os.Getenv("DEFINITIONS_REPO"),
		DefinitionsBranch:      env("DEFINITIONS_BRANCH", "main"),
		WebURL:                 env("CHETTER_WEB_URL", ""),
		EventsRetentionDays:    envInt("EVENTS_RETENTION_DAYS", 0),
		AuditRetentionDays:     envInt("AUDIT_RETENTION_DAYS", 0),
		ArtifactRetentionDays:  envInt("ARTIFACT_RETENTION_DAYS", 0),
		EnvValidation:          envValidationConfig(),
		TaskLimits:             taskLimitsConfig(),
		SessionArtifactTTL:     envDuration("SESSION_ARTIFACT_TTL", 24*time.Hour),
		AllowUnisolated:        envBool("CHETTER_ALLOW_UNISOLATED", false),
	}
}

// Validate checks required configuration.
func (c Config) Validate() error {
	if c.DatabaseDSN == "" {
		return fmt.Errorf("DATABASE_DSN is required")
	}
	if strings.TrimSpace(c.MCPAuthToken) == "" {
		return fmt.Errorf("MCP_AUTH_TOKEN is required")
	}
	if isPlaceholderAuthToken(c.MCPAuthToken) {
		return fmt.Errorf("MCP_AUTH_TOKEN must not use a placeholder value")
	}
	if strings.TrimSpace(c.RunnerRPCToken) == "" {
		return fmt.Errorf("CHETTER_RUNNER_RPC_TOKEN is required")
	}
	if isPlaceholderAuthToken(c.RunnerRPCToken) {
		return fmt.Errorf("CHETTER_RUNNER_RPC_TOKEN must not use a placeholder value")
	}
	return nil
}

func isPlaceholderAuthToken(token string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(token)), "change-me")
}

// GitHubWebhookConfigured reports whether signed webhook processing is ready.
func (c Config) GitHubWebhookConfigured() bool {
	return !c.GitHubWebhookDisabled &&
		c.GitHubWebhookSecret != "" &&
		c.GitHubAppConfigured()
}

// GitHubAppConfigured reports whether GitHub App API credentials are present.
func (c Config) GitHubAppConfigured() bool {
	return c.GitHubAppID > 0 &&
		c.GitHubAppPrivateKeyB64 != ""
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envSlice(key string, fallback []string) []string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parts := strings.Split(value, ",")
	var out []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func envValidationConfig() validation.Config {
	cfg := validation.Defaults()
	cfg.BlockedPrefixes = envSlice("CHETTER_ENV_BLOCKED_PREFIXES", cfg.BlockedPrefixes)
	cfg.BlockedNames = envSlice("CHETTER_ENV_BLOCKED_NAMES", cfg.BlockedNames)
	cfg.MaxCount = envInt("CHETTER_ENV_MAX_COUNT", cfg.MaxCount)
	cfg.MaxNameLength = envInt("CHETTER_ENV_MAX_NAME_LENGTH", cfg.MaxNameLength)
	cfg.MaxValueLength = envInt("CHETTER_ENV_MAX_VALUE_LENGTH", cfg.MaxValueLength)
	return cfg
}

// taskLimitsConfig returns deterministic task/trigger field limits. Defaults
// come from validation.DefaultTaskLimits; operators may override either cap
// via env. Setting a cap to 0 explicitly disables it.
func taskLimitsConfig() validation.TaskLimits {
	limits := validation.DefaultTaskLimits()
	if v := envInt("CHETTER_MAX_TASK_TIMEOUT_SEC", limits.MaxTimeoutSec); v >= 0 {
		limits.MaxTimeoutSec = v
	}
	if v := envInt("CHETTER_MAX_SESSION_TTL_HOURS", limits.MaxTTLHours); v >= 0 {
		limits.MaxTTLHours = v
	}
	return limits
}
