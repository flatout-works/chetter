package codewhale

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigWritesNativeMCPConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		RunnerMCPToken: "runner-token",
		McpEndpoints: []task.MCPEndpoint{{
			Name:           "docs",
			URL:            "https://docs.example.test/mcp",
			BearerTokenEnv: "DOCS_MCP_TOKEN",
		}},
	}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "http://chetter.test/mcp", "secret", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	mcpPath := filepath.Join(wsDir, ".codewhale", "mcp.json")
	data, err := os.ReadFile(mcpPath)
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
		if config.Servers[name]["enabled"] != true {
			t.Fatalf("CodeWhale server %q is not enabled: %#v", name, config.Servers[name])
		}
	}
	if got := config.Servers["docs"]["bearer_token_env_var"]; got != "DOCS_MCP_TOKEN" {
		t.Fatalf("docs bearer token env = %v", got)
	}
	headers, ok := config.Servers["runner-bridge"]["headers"].(map[string]any)
	if !ok || headers["Authorization"] != "Bearer runner-token" {
		t.Fatalf("runner bridge headers = %#v", config.Servers["runner-bridge"]["headers"])
	}
	chetter := config.Servers["chetter"]
	if chetter["url"] != "http://chetter.test/mcp" {
		t.Fatalf("chetter MCP URL = %v", chetter["url"])
	}
	chetterHeaders, ok := chetter["headers"].(map[string]any)
	if !ok || chetterHeaders["Authorization"] != "Bearer secret" {
		t.Fatalf("chetter MCP headers = %#v", chetter["headers"])
	}
}

func TestGenerateConfigWritesSyntheticProviderConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:        "synthetic",
		ModelID:           "hf:zai-org/GLM-5.2",
		ProviderBaseURL:   "https://api.synthetic.new/openai/v1",
		ProviderAPIKeyEnv: "SYNTHETIC_API_KEY",
	}
	if err := GenerateConfig(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	configPath := filepath.Join(wsDir, ".codewhale", "chetter-config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read provider config: %v", err)
	}
	for _, want := range []string{
		"[providers.synthetic]",
		`kind = "openai-compatible"`,
		`base_url = "https://api.synthetic.new/openai/v1"`,
		`model = "hf:zai-org/GLM-5.2"`,
		`api_key_env = "SYNTHETIC_API_KEY"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("provider config missing %q:\n%s", want, data)
		}
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat provider config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("provider config permissions = %v, want 0600", info.Mode().Perm())
	}

	env := codewhaleEnv("/workspace", "secret", req)
	if got := env["CODEWHALE_CONFIG_PATH"]; got != "/workspace/.codewhale/chetter-config.toml" {
		t.Fatalf("CODEWHALE_CONFIG_PATH = %q", got)
	}
	if got := env["CODEWHALE_PROVIDER"]; got != "synthetic" {
		t.Fatalf("CODEWHALE_PROVIDER = %q", got)
	}
}
