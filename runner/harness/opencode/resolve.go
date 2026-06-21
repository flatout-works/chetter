package opencode

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func promptWithSkillHints(prompt string, skills []string) string {
	if len(skills) == 0 {
		return prompt
	}
	return "Requested OpenCode skills: " + strings.Join(skills, ", ") + ". Use these skills when applicable.\n\n" + prompt
}

func promptModel(req task.TaskRequest, defaultProvider, defaultModel string) (string, string) {
	providerID := req.ProviderID
	modelID := req.ModelID
	if providerID == "" && strings.Contains(modelID, "/") {
		parts := strings.SplitN(modelID, "/", 2)
		providerID = parts[0]
		modelID = parts[1]
	}
	if providerID == "" {
		providerID = envValue(req.Env, "LLM_PROVIDER", defaultProvider)
	}
	if modelID == "" {
		modelID = envValue(req.Env, "LLM_MODEL_CODER", defaultModel)
	}
	return providerID, modelID
}

func resolvedChetterModelID(req task.TaskRequest) string {
	providerID, modelID := promptModel(req, "synthetic", "hf:zai-org/GLM-5.2")
	return providerID + "/" + modelID
}

func agentModelFromConfig(wsDir, agentName string) (providerID, modelID string) {
	if agentName == "" || wsDir == "" {
		return "", ""
	}
	path := filepath.Join(wsDir, ".opencode", "agent", agentName+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "model:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, "model:"))
		if val == "" {
			continue
		}
		if strings.Contains(val, "/") {
			parts := strings.SplitN(val, "/", 2)
			return parts[0], parts[1]
		}
		return "", val
	}
	return "", ""
}

func promptVariant(req task.TaskRequest) string {
	if req.VariantID != "" {
		return req.VariantID
	}
	return envValue(req.Env, "LLM_VARIANT", "")
}

func modelFlag(req task.TaskRequest) string {
	if req.ProviderID != "" && req.ModelID != "" {
		return req.ProviderID + "/" + req.ModelID
	}
	if req.ModelID != "" {
		return req.ModelID
	}
	provider := req.Env["LLM_PROVIDER"]
	model := req.Env["LLM_MODEL_CODER"]
	if provider != "" && model != "" {
		return provider + "/" + model
	}
	if model != "" {
		return model
	}
	return ""
}

func resolveCommand(req task.TaskRequest) []string {
	if len(req.Command) > 0 {
		return req.Command
	}
	if req.Prompt != "" {
		model := modelFlag(req)
		cmd := []string{
			"opencode", "run",
		}
		if !mem9Enabled() {
			cmd = append(cmd, "--pure")
		}
		cmd = append(cmd,
			"--port", "0",
			"--dir", "/workspace",
			"--print-logs",
			"--log-level", "DEBUG",
		)
		if model != "" {
			cmd = append(cmd, "-m", model)
		}
		if req.VariantID != "" {
			cmd = append(cmd, "--variant", req.VariantID)
		}
		if req.Agent != "" {
			cmd = append(cmd, "--agent", req.Agent)
		}
		cmd = append(cmd, promptWithSkillHints(req.Prompt, req.Skills), "--format", "json", "--dangerously-skip-permissions")
		return cmd
	}
	return nil
}

func envValue(env map[string]string, key, fallback string) string {
	if env != nil {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
