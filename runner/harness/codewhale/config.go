package codewhale

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
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
			"url":     runnerMCPURL,
			"enabled": true,
		}
	}

	if chetterMCPURL != "" {
		chetterMCP := map[string]any{
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
	if len(req.MCPProfiles) > 0 {
		if err := mcpconfig.AddCodeWhaleServers(mcpServers, req.MCPProfiles); err != nil {
			return err
		}
	}

	if len(mcpServers) > 0 {
		agentMCP := map[string]any{"servers": mcpServers}
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

func codewhaleEnv(wsDir, secret string, req task.TaskRequest) map[string]string {
	provider, model := codewhaleModelFields(req)
	env := map[string]string{
		"DEEPSEEK_MCP_CONFIG":     wsDir + "/.codewhale/mcp.json",
		"CODEWHALE_OFFLINE":       "1",
		"CODEWHALE_RUNTIME_TOKEN": secret,
		"CODEWHALE_PROVIDER":      provider,
		"CODEWHALE_MODEL":         model,
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	if baseURL := strings.TrimSpace(req.ProviderBaseURL); baseURL != "" {
		env["CODEWHALE_BASE_URL"] = baseURL
	}
	if provider == "deepseek" {
		baseURL := strings.TrimSpace(req.ProviderBaseURL)
		apiKeyEnv := strings.TrimSpace(req.ProviderAPIKeyEnv)
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		if apiKeyEnv == "" {
			apiKeyEnv = "DEEPSEEK_API_KEY"
		}
		env["CODEWHALE_BASE_URL"] = baseURL
		if apiKey := os.Getenv(apiKeyEnv); apiKey != "" {
			env["DEEPSEEK_API_KEY"] = apiKey
		}
	}

	return env
}
