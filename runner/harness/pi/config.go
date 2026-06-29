package pi

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/safefs"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, isLocal bool) error {
	return GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, "", chetterMCPURL, chetterMCPToken, task.TaskRequest{}, isLocal)
}

func GenerateConfigForTask(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, "", chetterMCPURL, chetterMCPToken, req, isLocal)
}

func GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	if err := safefs.EnsureDir(wsDir, ".pi/agent", 0750); err != nil {
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
	if err := writeJSON(wsDir, ".pi/agent/settings.json", globalSettings, 0644); err != nil {
		return err
	}

	projectSettings := map[string]any{}
	if adapterPath := mcpAdapterPath(); adapterPath != "" {
		projectSettings["extensions"] = []string{adapterPath}
	}
	if len(projectSettings) > 0 {
		if err := writeJSON(wsDir, ".pi/settings.json", projectSettings, 0644); err != nil {
			return err
		}
	}

	mcpServers := map[string]any{}
	if runnerMCPURL != "" {
		bridge := map[string]any{
			"url":         runnerMCPURL,
			"lifecycle":   "keep-alive",
			"idleTimeout": 0,
		}
		if runnerMCPToken != "" {
			bridge["headers"] = map[string]string{
				"Authorization": "Bearer " + runnerMCPToken,
			}
		}
		mcpServers["runner-bridge"] = bridge
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
	if err := mcpconfig.AddPiServers(mcpServers, req.MCPProfiles); err != nil {
		return err
	}
	if len(mcpServers) > 0 {
		mcpConfig := map[string]any{
			"mcpServers": mcpServers,
		}
		if err := writeJSON(wsDir, ".mcp.json", mcpConfig, 0644); err != nil {
			return err
		}
		slog.Info("wrote pi mcp config", "path", filepath.Join(wsDir, ".mcp.json"))
	}

	if isLocal {
		copyPiState(wsDir)
	}
	return nil
}

func mcpAdapterPath() string {
	if path := os.Getenv("PI_MCP_ADAPTER_PATH"); path != "" {
		return path
	}
	const defaultPath = "/opt/pi-extensions/node_modules/pi-mcp-adapter"
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath
	}
	return ""
}

func writeJSON(root, relPath string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := safefs.WriteFile(root, relPath, data, perm); err != nil {
		return err
	}
	slog.Info("wrote pi config", "path", filepath.Join(root, relPath))
	return nil
}
