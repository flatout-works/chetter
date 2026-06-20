package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/flatout-works/chetter/pkg/modelcatalog"
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
	defaultProvider, defaultModel := modelcatalog.ParseYAMLOrDefault(os.Getenv(modelcatalog.EnvKey)).DefaultForHarness("claude-code", "anthropic", "claude-sonnet-4-5")
	if model == "" {
		model = defaultModel
	}
	if provider == "" {
		provider = defaultProvider
	}
	return
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
