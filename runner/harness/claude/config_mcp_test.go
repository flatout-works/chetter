package claude

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
		Name: "context", URL: "https://mcp.example.com/mcp", BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}}}
	if err := GenerateConfig(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wsDir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	profile := cfg["mcpServers"].(map[string]any)["context"].(map[string]any)
	if profile["headers"].(map[string]any)["Authorization"] != "Bearer ${EXAMPLE_MCP_TOKEN}" {
		t.Fatalf("unexpected MCP profile: %#v", profile)
	}
}
