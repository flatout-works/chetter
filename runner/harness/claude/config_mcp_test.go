package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigAddsTaskMCPProfile(t *testing.T) {
	t.Setenv("EXAMPLE_MCP_TOKEN", "runner-secret")
	wsDir := t.TempDir()
	req := task.TaskRequest{MCPProfiles: []task.MCPProfile{{
		Name:           "context",
		Transport:      "http",
		URL:            "https://mcp.example.com/mcp",
		BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}}}
	if err := GenerateConfig(wsDir, "", req, false); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse MCP config: %v", err)
	}
	profile := cfg.Servers["context"]
	if profile.Type != "http" || profile.URL != "https://mcp.example.com/mcp" || profile.Headers["Authorization"] != "Bearer runner-secret" {
		t.Fatalf("unexpected MCP profile: %#v", profile)
	}

	settingsData, err := os.ReadFile(filepath.Join(wsDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	found := false
	for _, permission := range settings.Permissions.Allow {
		if permission == "mcp__context__*" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected context MCP tools to be allowed: %#v", settings.Permissions.Allow)
	}
}
