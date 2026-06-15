package config

import (
	"testing"
)

func TestValidate(t *testing.T) {
	t.Run("all required fields present", func(t *testing.T) {
		cfg := Config{
			DatabaseDSN: "root@tcp(localhost:4000)/db",
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
		cfg := Load()
		if cfg.HTTPAddr != ":8080" {
			t.Errorf("expected :8080, got %q", cfg.HTTPAddr)
		}
		if cfg.DefaultTaskTimeoutSec != 600 {
			t.Errorf("expected 600, got %d", cfg.DefaultTaskTimeoutSec)
		}
	})
	t.Run("env overrides", func(t *testing.T) {
		t.Setenv("HTTP_ADDR", ":9090")
		t.Setenv("DEFAULT_AGENT_IMAGE", "custom:latest")
		t.Setenv("DEFAULT_TASK_TIMEOUT_SEC", "300")
		cfg := Load()
		if cfg.HTTPAddr != ":9090" {
			t.Errorf("expected :9090, got %q", cfg.HTTPAddr)
		}
		if cfg.DefaultAgentImage != "custom:latest" {
			t.Errorf("expected custom:latest, got %q", cfg.DefaultAgentImage)
		}
		if cfg.DefaultTaskTimeoutSec != 300 {
			t.Errorf("expected 300, got %d", cfg.DefaultTaskTimeoutSec)
		}
	})
	t.Run("github fields", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("GITHUB_INSTALLATION_ID", "67890")
		t.Setenv("GITHUB_APP_PRIVATE_KEY_B64", "cHJpdmF0ZSBrZXk=")
		t.Setenv("GITHUB_WEBHOOK_SECRET", "secret123")
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
	})
	t.Run("github not configured by default", func(t *testing.T) {
		cfg := Load()
		if cfg.GitHubConfigured() {
			t.Error("expected GitHub not configured")
		}
	})
}

func TestGitHubConfigured(t *testing.T) {
	t.Run("all required fields present", func(t *testing.T) {
		cfg := Config{
			GitHubWebhookSecret:    "secret",
			GitHubAppID:            1,
			GitHubAppPrivateKeyB64: "key",
			GitHubInstallationID:   1,
		}
		if !cfg.GitHubConfigured() {
			t.Error("expected configured")
		}
	})
	t.Run("missing webhook secret", func(t *testing.T) {
		cfg := Config{
			GitHubAppID:             1,
			GitHubAppPrivateKeyB64:  "key",
			GitHubInstallationID:    1,
		}
		if cfg.GitHubConfigured() {
			t.Error("expected not configured")
		}
	})
	t.Run("missing app id", func(t *testing.T) {
		cfg := Config{
			GitHubWebhookSecret:    "secret",
			GitHubAppPrivateKeyB64: "key",
			GitHubInstallationID:   1,
		}
		if cfg.GitHubConfigured() {
			t.Error("expected not configured")
		}
	})
	t.Run("disabled by flag", func(t *testing.T) {
		cfg := Config{
			GitHubWebhookDisabled:   true,
			GitHubWebhookSecret:     "secret",
			GitHubAppID:             1,
			GitHubAppPrivateKeyB64:  "key",
			GitHubInstallationID:    1,
		}
		if cfg.GitHubConfigured() {
			t.Error("expected not configured when disabled")
		}
	})
}
