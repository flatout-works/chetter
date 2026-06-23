package claude

import (
	"encoding/json"
	"fmt"
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

func claudeEnv(wsDir string, req task.TaskRequest) map[string]string {
	env := map[string]string{
		"CLAUDE_CONFIG_DIR":                        wsDir + "/.claude",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_ATTRIBUTION_HEADER":           "0",
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

func buildClaudeCommand(req task.TaskRequest) []string {
	prompt := req.Prompt
	if len(req.Skills) > 0 {
		prompt = fmt.Sprintf("You have access to the following skills: %s. Use them when relevant.\n\n%s",
			strings.Join(req.Skills, ", "), prompt)
	}
	_, model := claudeModelFields(req)

	args := []string{
		"claude",
		"--bare",
		"-p", prompt,
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--model", model,
		"--permission-mode", "bypassPermissions",
		"--max-turns", "100",
	}

	if req.Agent != "" {
		systemPrompt := resolveAgentSystemPrompt(req.Agent)
		if systemPrompt != "" {
			args = append(args, "--system-prompt", systemPrompt)
		}
	}

	return args
}

func resolveAgentSystemPrompt(agentName string) string {
	agentPaths := []string{
		".claude/agents/" + agentName + ".md",
		".opencode/agent/" + agentName + ".md",
	}
	for _, p := range agentPaths {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func summarizeStreamJSON(raw string) string {
	var summary strings.Builder
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		typ, _ := ev["type"].(string)
		if typ == "stream_event" {
			if event, ok := ev["event"].(map[string]any); ok {
				if delta, ok := event["delta"].(map[string]any); ok {
					if t, _ := delta["type"].(string); t == "text_delta" {
						if text, _ := delta["text"].(string); text != "" {
							summary.WriteString(text)
						}
					}
				}
			}
		}
		if typ == "user" {
			if msg, ok := ev["message"].(map[string]any); ok {
				if text, _ := msg["text"].(string); text != "" {
					summary.WriteString(text)
				}
			}
		}
	}
	return summary.String()
}
