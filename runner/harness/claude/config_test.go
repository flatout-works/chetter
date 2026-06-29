package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigForTaskAddsMCPProfiles(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		MCPProfiles: []task.MCPProfile{{
			Name:      "docs-search",
			Transport: "http",
			URL:       "https://mcp.example.test/mcp",
			Headers: map[string]string{
				"X-Test": "ok",
			},
		}},
	}
	if err := GenerateConfigForTask(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfigForTask failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wsDir, ".claude", "mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse mcp.json: %v", err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	server := servers["docs-search"].(map[string]any)
	if server["type"] != "http" || server["url"] != "https://mcp.example.test/mcp" || server["enabled"] != true {
		t.Fatalf("unexpected MCP server config: %+v", server)
	}
}

func TestGenerateConfigForTaskAddsRunnerBridgeAuth(t *testing.T) {
	wsDir := t.TempDir()
	if err := GenerateConfigForTaskWithRunnerToken(wsDir, "http://localhost:9999/mcp", "runner-token", "", "", task.TaskRequest{}, false); err != nil {
		t.Fatalf("GenerateConfigForTaskWithRunnerToken failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wsDir, ".claude", "mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse mcp.json: %v", err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	bridge := servers["runner-bridge"].(map[string]any)
	headers := bridge["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer runner-token" {
		t.Fatalf("runner bridge auth header = %v, want bearer token", headers["Authorization"])
	}
}

func TestGenerateConfigForTaskRejectsAllowlistedMCPProfiles(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		MCPProfiles: []task.MCPProfile{{
			Name:          "restricted",
			URL:           "https://mcp.example.test/mcp",
			ToolAllowlist: []string{"allowed_tool"},
		}},
	}
	err := GenerateConfigForTask(wsDir, "", "", "", req, false)
	if err == nil {
		t.Fatal("expected allowlisted mcp profile to fail")
	}
	if !strings.Contains(err.Error(), "cannot enforce per-tool MCP restrictions") {
		t.Fatalf("error = %q, want enforcement message", err)
	}
}
