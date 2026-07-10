package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigAddsTaskMCPProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()
	req := task.TaskRequest{MCPProfiles: []task.MCPProfile{{
		Name: "context", URL: "https://mcp.example.com/mcp", BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}}}
	if err := GenerateConfigForTask(wsDir, "", "", "", false, req, false); err != nil {
		t.Fatalf("GenerateConfigForTask: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wsDir, ".opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	profile := cfg["mcp"].(map[string]any)["context"].(map[string]any)
	if profile["headers"].(map[string]any)["Authorization"] != "Bearer {env:EXAMPLE_MCP_TOKEN}" {
		t.Fatalf("unexpected MCP profile: %#v", profile)
	}
}
