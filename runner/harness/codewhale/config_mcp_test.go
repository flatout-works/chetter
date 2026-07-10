package codewhale

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigAddsTaskMCPProfile(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{MCPProfiles: []task.MCPProfile{{
		Name: "context", Transport: "sse", URL: "https://mcp.example.com/sse", BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}}}
	if err := GenerateConfig(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wsDir, ".codewhale", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	profile := cfg["servers"].(map[string]any)["context"].(map[string]any)
	if profile["transport"] != "sse" || profile["bearer_token_env_var"] != "EXAMPLE_MCP_TOKEN" {
		t.Fatalf("unexpected MCP profile: %#v", profile)
	}
}
