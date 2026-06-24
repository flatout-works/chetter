package codewhale

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	codewhaleDir := wsDir + "/.codewhale"
	if err := os.MkdirAll(codewhaleDir, 0750); err != nil {
		return err
	}

	mcpServers := map[string]any{}

	if runnerMCPURL != "" {
		mcpServers["runner-bridge"] = map[string]any{
			"type":    "remote",
			"url":     runnerMCPURL,
			"enabled": true,
		}
	}

	if chetterMCPURL != "" {
		chetterMCP := map[string]any{
			"type":    "http",
			"url":     chetterMCPURL,
			"enabled": true,
		}
		if chetterMCPToken != "" {
			chetterMCP["headers"] = map[string]string{
				"Authorization": "Bearer " + chetterMCPToken,
			}
		}
		mcpServers["chetter"] = chetterMCP
	}

	if len(mcpServers) > 0 {
		agentMCP := map[string]any{
			"mcpServers": mcpServers,
		}
		agentMCPData, err := json.MarshalIndent(agentMCP, "", "  ")
		if err != nil {
			return err
		}
		agentMCPPath := codewhaleDir + "/mcp.json"
		if err := os.WriteFile(agentMCPPath, agentMCPData, 0644); err != nil {
			return err
		}
		slog.Info("wrote codewhale mcp config", "path", agentMCPPath)
	}

	return nil
}

func codewhaleEnv(wsDir string, req task.TaskRequest) map[string]string {
	env := map[string]string{
		"CODEWHALE_CONFIG_DIR": wsDir + "/.codewhale",
		"CODEWHALE_OFFLINE":    "1",
	}

	provider := strings.ToLower(strings.TrimSpace(req.ProviderID))
	if provider == "deepseek" {
		baseURL := strings.TrimSpace(req.ProviderBaseURL)
		apiKeyEnv := strings.TrimSpace(req.ProviderAPIKeyEnv)
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		if apiKeyEnv == "" {
			apiKeyEnv = "DEEPSEEK_API_KEY"
		}
		env["CODEWHALE_API_BASE"] = baseURL
		if apiKey := os.Getenv(apiKeyEnv); apiKey != "" {
			env["CODEWHALE_API_KEY"] = apiKey
		}
	}

	return env
}
