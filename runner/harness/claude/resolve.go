package claude

import (
	"os"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func resolvedClaudeModelID(req task.TaskRequest) string {
	provider, model := claudeModelFields(req)
	return provider + "/" + model
}

func claudeModelFields(req task.TaskRequest) (provider, model string) {
	provider = req.ProviderID
	model = req.ModelID
	if model == "" {
		model = os.Getenv("ANTHROPIC_MODEL")
	}
	if model == "" {
		model = "claude-sonnet-4-5"
	}
	if provider == "" {
		provider = "anthropic"
	}
	return
}

func claudeEnv(wsDir, secret string, req task.TaskRequest) map[string]string {
	env := map[string]string{
		"CLAUDE_CONFIG_DIR":                        wsDir + "/.claude",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_ATTRIBUTION_HEADER":           "0",
		"CLAUDE_SERVE_PROXY_TOKEN":                 secret,
	}

	provider := strings.ToLower(strings.TrimSpace(req.ProviderID))
	baseURL := strings.TrimSpace(req.ProviderBaseURL)
	apiKeyEnv := strings.TrimSpace(req.ProviderAPIKeyEnv)
	if provider == "synthetic" {
		if baseURL == "" {
			baseURL = "https://api.synthetic.new/anthropic"
		}
		if apiKeyEnv == "" {
			apiKeyEnv = "SYNTHETIC_API_KEY"
		}
	}

	useCustomAnthropicEndpoint := provider == "synthetic" || baseURL != ""

	if baseURL != "" {
		env["ANTHROPIC_BASE_URL"] = baseURL
	}
	if useCustomAnthropicEndpoint && apiKeyEnv != "" {
		if apiKey := os.Getenv(apiKeyEnv); apiKey != "" {
			env["ANTHROPIC_AUTH_TOKEN"] = apiKey
		}
	}

	if model := strings.TrimSpace(req.ModelID); model != "" && useCustomAnthropicEndpoint {
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = model
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = model
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = model
		env["CLAUDE_CODE_SUBAGENT_MODEL"] = model
	}

	return env
}
