package pi

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

func GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, isLocal bool) error {
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
	if mcpBridgePath != "" {
		mcpServers["runner-bridge"] = map[string]any{
			"command":     []string{mcpBridgePath, socketPath},
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
