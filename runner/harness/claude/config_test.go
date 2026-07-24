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
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "", "", task.TaskRequest{}, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	mcpData, err := os.ReadFile(filepath.Join(wsDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
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
	for _, server := range settings.EnabledMCPServers {
		if server == "runner-bridge" {
			for _, rule := range settings.Permissions.Allow {
				if rule == "mcp__runner-bridge__*" {
					return
				}
			}
			t.Fatalf("runner bridge MCP permission missing: %q", settings.Permissions.Allow)
		}
	}
	t.Fatalf("runner bridge MCP server approval missing: %q", settings.EnabledMCPServers)
}
