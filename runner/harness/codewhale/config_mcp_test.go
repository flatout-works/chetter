package codewhale

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
		Transport:      "sse",
		URL:            "https://mcp.example.com/sse",
		BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}}}
	if err := GenerateConfig(wsDir, "", req, false); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".codewhale", "mcp.json"))
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
	if profile.Type != "sse" || profile.URL != "https://mcp.example.com/sse" || profile.Headers["Authorization"] != "Bearer runner-secret" {
		t.Fatalf("unexpected MCP profile: %#v", profile)
	}
}
