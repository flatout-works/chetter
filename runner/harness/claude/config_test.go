package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigDeniesInteractiveQuestions(t *testing.T) {
	wsDir := t.TempDir()
	if err := GenerateConfig(wsDir, "", "", "", task.TaskRequest{}, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	for _, rule := range settings.Permissions.Deny {
		if rule == "AskUserQuestion" {
			return
		}
	}
	t.Fatalf("expected AskUserQuestion to be denied, got %q", settings.Permissions.Deny)
}

func TestGenerateConfigIncludesRunnerBridgeHTTPServer(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{RunnerMCPToken: "runner-token"}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "http://chetter.test/mcp", "chetter-token", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	mcpPath := filepath.Join(wsDir, ".mcp.json")
	mcpData, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
	}
	info, err := os.Stat(mcpPath)
	if err != nil {
		t.Fatalf("stat MCP config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("MCP config permissions = %v, want 0600", info.Mode().Perm())
	}
	var mcpConfig struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpData, &mcpConfig); err != nil {
		t.Fatalf("parse MCP config: %v", err)
	}
	bridge := mcpConfig.MCPServers["runner-bridge"]
	if bridge["type"] != "http" {
		t.Fatalf("runner bridge type = %v, want http", bridge["type"])
	}
	if bridge["url"] != "http://runner.test/mcp" {
		t.Fatalf("runner bridge URL = %v", bridge["url"])
	}
	headers, ok := bridge["headers"].(map[string]any)
	if !ok || headers["Authorization"] != "Bearer runner-token" {
		t.Fatalf("runner bridge headers = %#v", bridge["headers"])
	}
	chetter := mcpConfig.MCPServers["chetter"]
	if chetter["type"] != "http" || chetter["url"] != "http://chetter.test/mcp" {
		t.Fatalf("chetter MCP server = %#v", chetter)
	}
	chetterHeaders, ok := chetter["headers"].(map[string]any)
	if !ok || chetterHeaders["Authorization"] != "Bearer chetter-token" {
		t.Fatalf("chetter MCP headers = %#v", chetter["headers"])
	}

	settingsData, err := os.ReadFile(filepath.Join(wsDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		EnabledMCPServers []string `json:"enabledMcpjsonServers"`
		Permissions       struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	for _, server := range []string{"runner-bridge", "chetter"} {
		if !contains(settings.EnabledMCPServers, server) {
			t.Fatalf("MCP server %q approval missing: %q", server, settings.EnabledMCPServers)
		}
		if !contains(settings.Permissions.Allow, "mcp__"+server+"__*") {
			t.Fatalf("MCP server %q permission missing: %q", server, settings.Permissions.Allow)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
