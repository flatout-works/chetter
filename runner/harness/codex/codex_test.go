package codex

import (
	"os"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:        "openai",
		ModelID:           "gpt-5.4",
		ProviderBaseURL:   "https://api.example.test/v1",
		ProviderAPIKeyEnv: "EXAMPLE_API_KEY",
	}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "https://chetter.test/mcp", "secret", req); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	data, err := os.ReadFile(wsDir + "/.codex/config.toml")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(data)
	for _, want := range []string{
		`model = "gpt-5.4"`,
		`model_provider = "chetter"`,
		`approval_policy = "never"`,
		`sandbox_mode = "workspace-write"`,
		`base_url = "https://api.example.test/v1"`,
		`env_key = "EXAMPLE_API_KEY"`,
		`wire_api = "responses"`,
		`[mcp_servers.runner-bridge]`,
		`[mcp_servers.chetter]`,
		`Authorization = "Bearer secret"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}

func TestResolvedModelIDDefaults(t *testing.T) {
	if got := resolvedModelID(task.TaskRequest{}); got != "openai/gpt-5.4" {
		t.Fatalf("resolved model = %q", got)
	}
}

func TestPromptWithSkillHints(t *testing.T) {
	if got := promptWithSkillHints("do work", []string{"go", "svelte"}); !strings.Contains(got, "go, svelte") || !strings.HasSuffix(got, "do work") {
		t.Fatalf("unexpected prompt: %q", got)
	}
}

func TestGenerateBedrockConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:      "aws-bedrock",
		ModelID:         "us.anthropic.claude-sonnet-4-20250514-v1:0",
		ProviderBaseURL: "https://bedrock-runtime.us-west-2.amazonaws.com",
		Env: map[string]string{
			"__chetter_provider_kind": "aws_bedrock",
			"__chetter_aws_profile":   "chetter-bedrock",
			"__chetter_aws_region":    "us-west-2",
		},
	}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "https://chetter.test/mcp", "secret", req); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	data, err := os.ReadFile(wsDir + "/.codex/config.toml")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(data)
	for _, want := range []string{
		`model = "us.anthropic.claude-sonnet-4-20250514-v1:0"`,
		`model_provider = "amazon-bedrock"`,
		`[model_providers.amazon-bedrock]`,
		`[model_providers.amazon-bedrock.aws]`,
		`profile = "chetter-bedrock"`,
		`region = "us-west-2"`,
		`base_url = "https://bedrock-runtime.us-west-2.amazonaws.com"`,
		`approval_policy = "never"`,
		`sandbox_mode = "workspace-write"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, `model_provider = "chetter"`) {
		t.Fatal("Bedrock config should not contain 'chetter' model provider")
	}
}
