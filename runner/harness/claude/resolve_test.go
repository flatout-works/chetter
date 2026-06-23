package claude

import (
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestClaudeEnvSynthetic(t *testing.T) {
	t.Setenv("SYNTHETIC_API_KEY", "syn-key")

	env := claudeEnv("/workspace", task.TaskRequest{
		ProviderID: "synthetic",
		ModelID:    "syn:large:vision",
	})

	want := map[string]string{
		"CLAUDE_CONFIG_DIR":                        "/workspace/.claude",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_ATTRIBUTION_HEADER":           "0",
		"ANTHROPIC_BASE_URL":                       "https://api.synthetic.new/anthropic",
		"ANTHROPIC_AUTH_TOKEN":                     "syn-key",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":             "syn:large:vision",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":           "syn:large:vision",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":            "syn:large:vision",
		"CLAUDE_CODE_SUBAGENT_MODEL":               "syn:large:vision",
	}
	for key, value := range want {
		if got := env[key]; got != value {
			t.Fatalf("env[%s] = %q, want %q", key, got, value)
		}
	}
}

func TestClaudeEnvCustomAnthropicCompatibleProvider(t *testing.T) {
	t.Setenv("CUSTOM_API_KEY", "custom-key")

	env := claudeEnv("/workspace", task.TaskRequest{
		ProviderID:        "custom-provider",
		ModelID:           "custom-model",
		ProviderBaseURL:   "https://example.test/anthropic",
		ProviderAPIKeyEnv: "CUSTOM_API_KEY",
	})

	if got := env["ANTHROPIC_BASE_URL"]; got != "https://example.test/anthropic" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q", got)
	}
	if got := env["ANTHROPIC_AUTH_TOKEN"]; got != "custom-key" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q", got)
	}
	if got := env["CLAUDE_CODE_SUBAGENT_MODEL"]; got != "custom-model" {
		t.Fatalf("CLAUDE_CODE_SUBAGENT_MODEL = %q", got)
	}
}

func TestClaudeEnvAnthropicDefaultDoesNotOverrideEndpoint(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")

	env := claudeEnv("/workspace", task.TaskRequest{
		ProviderID:        "anthropic",
		ModelID:           "claude-sonnet-4-5",
		ProviderAPIKeyEnv: "ANTHROPIC_API_KEY",
	})

	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		t.Fatalf("ANTHROPIC_BASE_URL should not be set for native Anthropic")
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be set for native Anthropic")
	}
	if _, ok := env["CLAUDE_CODE_SUBAGENT_MODEL"]; ok {
		t.Fatalf("CLAUDE_CODE_SUBAGENT_MODEL should not be set for native Anthropic")
	}
}
