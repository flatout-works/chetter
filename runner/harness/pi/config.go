package pi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	piDir := filepath.Join(wsDir, ".pi")
	agentDir := filepath.Join(piDir, "agent")
	if err := os.MkdirAll(agentDir, 0750); err != nil {
		return err
	}

	globalSettings := map[string]any{
		"defaultProjectTrust":    "always",
		"quietStartup":           true,
		"enableInstallTelemetry": false,
		"compaction": map[string]any{
			"enabled":          true,
			"reserveTokens":    16384,
			"keepRecentTokens": 20000,
		},
		"retry": map[string]any{
			"enabled":    true,
			"maxRetries": 3,
		},
	}
	if err := writeJSON(filepath.Join(agentDir, "settings.json"), globalSettings, 0644); err != nil {
		return err
	}
	if err := writeProviderConfig(agentDir, req); err != nil {
		return err
	}

	projectSettings := map[string]any{}
	if adapterPath := mcpAdapterPath(); adapterPath != "" {
		projectSettings["extensions"] = []string{adapterPath}
	}
	if len(projectSettings) > 0 {
		if err := writeJSON(filepath.Join(piDir, "settings.json"), projectSettings, 0644); err != nil {
			return err
		}
	}

	mcpServers := map[string]any{}
	if runnerMCPURL != "" {
		mcpServers["runner-bridge"] = map[string]any{
			"url":         runnerMCPURL,
			"lifecycle":   "keep-alive",
			"idleTimeout": 0,
		}
	}
	if chetterMCPURL != "" {
		server := map[string]any{
			"url":       chetterMCPURL,
			"lifecycle": "keep-alive",
		}
		if chetterMCPToken != "" {
			server["headers"] = map[string]string{
				"Authorization": "Bearer " + chetterMCPToken,
			}
		}
		mcpServers["chetter"] = server
	}
	if len(req.MCPProfiles) > 0 {
		if err := mcpconfig.AddPiServers(mcpServers, req.MCPProfiles); err != nil {
			return err
		}
	}
	if len(mcpServers) > 0 {
		mcpConfig := map[string]any{
			"mcpServers": mcpServers,
		}
		if err := writeJSON(filepath.Join(wsDir, ".mcp.json"), mcpConfig, 0644); err != nil {
			return err
		}
		slog.Info("wrote pi mcp config", "path", filepath.Join(wsDir, ".mcp.json"))
	}

	if isLocal {
		copyPiState(wsDir)
	}
	return nil
}

func writeProviderConfig(agentDir string, req task.TaskRequest) error {
	api := strings.TrimSpace(req.ProviderAPI)
	if api == "" {
		return nil
	}
	if strings.TrimSpace(req.ProviderID) == "" || strings.TrimSpace(req.ModelID) == "" || strings.TrimSpace(req.ProviderBaseURL) == "" {
		return fmt.Errorf("pi custom provider requires provider_id, model_id, and provider_base_url")
	}

	provider := map[string]any{
		"baseUrl": req.ProviderBaseURL,
		"api":     api,
		"models": []map[string]string{{
			"id": req.ModelID,
		}},
	}
	if apiKeyEnv := strings.TrimSpace(req.ProviderAPIKeyEnv); apiKeyEnv != "" {
		provider["apiKey"] = "$" + apiKeyEnv
	}
	if req.ProviderAuthHeader {
		provider["authHeader"] = true
	}

	return writeJSON(filepath.Join(agentDir, "models.json"), map[string]any{
		"providers": map[string]any{
			req.ProviderID: provider,
		},
	}, 0644)
}

func mcpAdapterPath() string {
	if path := os.Getenv("PI_MCP_ADAPTER_PATH"); path != "" {
		return path
	}
	return "/opt/pi-extensions/node_modules/pi-mcp-adapter"
}

func writeJSON(path string, v any, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	slog.Info("wrote pi config", "path", path)
	return nil
}
