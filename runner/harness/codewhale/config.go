package codewhale

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/safefs"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfigWithRunnerToken(wsDir, runnerMCPURL, "", chetterMCPURL, chetterMCPToken, req, isLocal)
}

func GenerateConfigWithRunnerToken(wsDir, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	if err := safefs.EnsureDir(wsDir, ".codewhale", 0750); err != nil {
		return err
	}

	mcpServers := map[string]any{}

	if runnerMCPURL != "" {
		bridge := map[string]any{
			"type":    "remote",
			"url":     runnerMCPURL,
			"enabled": true,
		}
		if runnerMCPToken != "" {
			bridge["headers"] = map[string]string{
				"Authorization": "Bearer " + runnerMCPToken,
			}
		}
		mcpServers["runner-bridge"] = bridge
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
	if err := mcpconfig.AddHTTPServers(mcpServers, req.MCPProfiles); err != nil {
		return err
	}

	if len(mcpServers) > 0 {
		agentMCP := map[string]any{
			"mcpServers": mcpServers,
		}
		agentMCPData, err := json.MarshalIndent(agentMCP, "", "  ")
		if err != nil {
			return err
		}
		const agentMCPRelPath = ".codewhale/mcp.json"
		if err := safefs.WriteFile(wsDir, agentMCPRelPath, agentMCPData, 0644); err != nil {
			return err
		}
		slog.Info("wrote codewhale mcp config", "path", filepath.Join(wsDir, agentMCPRelPath))
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
