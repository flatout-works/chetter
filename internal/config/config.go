// Package config loads chetter service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings for the chetter MCP service.
type Config struct {
	HTTPAddr               string
	WebAddr                string
	MCPAuthToken           string
	RunnerRPCToken         string
	DatabaseDSN            string
	DefaultAgentImage      string
	DefaultTaskTimeoutSec  int
	ArcaneServerURL        string
	ArcaneAPIKey           string
	GitHubAppID            int64
	GitHubAppPrivateKeyB64 string
	GitHubWebhookSecret    string
	GitHubWebhookDisabled  bool
	GitHubInstallationID   int64
}

// Load returns configuration using environment variables and safe defaults.
func Load() Config {
	return Config{
		HTTPAddr:               env("HTTP_ADDR", ":8080"),
		WebAddr:                env("WEB_ADDR", ":8090"),
		MCPAuthToken:           os.Getenv("MCP_AUTH_TOKEN"),
		RunnerRPCToken:         os.Getenv("CHETTER_RUNNER_RPC_TOKEN"),
		DatabaseDSN:            os.Getenv("DATABASE_DSN"),
		DefaultAgentImage:      env("DEFAULT_AGENT_IMAGE", "ghcr.io/flatout-works/chetter-runner:latest"),
		DefaultTaskTimeoutSec:  envInt("DEFAULT_TASK_TIMEOUT_SEC", 600),
		ArcaneServerURL:        env("ARCANE_SERVER_URL", ""),
		ArcaneAPIKey:           env("ARCANE_API_KEY", ""),
		GitHubAppID:            envInt64("GITHUB_APP_ID", 0),
		GitHubAppPrivateKeyB64: os.Getenv("GITHUB_APP_PRIVATE_KEY_B64"),
		GitHubWebhookSecret:    os.Getenv("GITHUB_WEBHOOK_SECRET"),
		GitHubWebhookDisabled:  envBool("GITHUB_WEBHOOK_DISABLED", false),
		GitHubInstallationID:   envInt64("GITHUB_INSTALLATION_ID", 0),
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

// GitHubConfigured reports whether the GitHub App integration is enabled.
// Returns true only if all required fields are present.
func (c Config) GitHubConfigured() bool {
	return !c.GitHubWebhookDisabled &&
		c.GitHubWebhookSecret != "" &&
		c.GitHubAppID > 0 &&
		c.GitHubAppPrivateKeyB64 != "" &&
		c.GitHubInstallationID > 0
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
