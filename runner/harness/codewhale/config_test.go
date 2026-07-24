package codewhale

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigWritesNativeMCPConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		McpEndpoints: []task.MCPEndpoint{{
			Name:           "docs",
			URL:            "https://docs.example.test/mcp",
			BearerTokenEnv: "DOCS_MCP_TOKEN",
		}},
	}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "http://chetter.test/mcp", "secret", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".codewhale", "mcp.json"))
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
	}
	var config struct {
		Servers map[string]map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse MCP config: %v", err)
	}
	for _, name := range []string{"runner-bridge", "chetter", "docs"} {
		if _, ok := config.Servers[name]; !ok {
			t.Fatalf("MCP server %q missing from config: %#v", name, config.Servers)
		}
	}
	for _, name := range []string{"runner-bridge", "chetter"} {
		if _, ok := config.Servers[name]["type"]; ok {
			t.Fatalf("CodeWhale server %q must not contain an OpenCode type field: %#v", name, config.Servers[name])
		}
	}
	if got := config.Servers["docs"]["bearer_token_env_var"]; got != "DOCS_MCP_TOKEN" {
		t.Fatalf("docs bearer token env = %v", got)
	}
}
