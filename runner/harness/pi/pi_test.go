package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestBuildRPCCommand(t *testing.T) {
	req := task.TaskRequest{
		ProviderID: "zai",
		ModelID:    "glm-5.2",
		VariantID:  "high",
	}
	got := buildRPCCommand(req)
	want := []string{"pi", "--mode", "rpc", "--no-session", "--offline", "--approve", "--provider", "zai", "--model", "glm-5.2", "--thinking", "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildRPCCommand() = %#v, want %#v", got, want)
	}
}

func TestBuildRPCCommandProviderQualifiedModel(t *testing.T) {
	req := task.TaskRequest{ModelID: "anthropic/claude-sonnet-4-5"}
	got := buildRPCCommand(req)
	want := []string{"pi", "--mode", "rpc", "--no-session", "--offline", "--approve", "--provider", "anthropic", "--model", "claude-sonnet-4-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildRPCCommand() = %#v, want %#v", got, want)
	}
}

func TestResolvedModelID(t *testing.T) {
	req := task.TaskRequest{ProviderID: "zai", ModelID: "glm-5.2"}
	if got := resolvedModelID(req); got != "zai/glm-5.2" {
		t.Fatalf("resolvedModelID() = %q", got)
	}
}

func TestGenerateConfigWritesSettingsAndMCP(t *testing.T) {
	t.Setenv("PI_MCP_ADAPTER_PATH", "")

	wsDir := t.TempDir()
	req := task.TaskRequest{MCPProfiles: []task.MCPProfile{{
		Name: "context", URL: "https://mcp.example.com/mcp", BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}}}
	if err := GenerateConfig(wsDir, "http://localhost:9999/mcp", "https://chetter.example.com/mcp", "token", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	assertJSONPath(t, filepath.Join(wsDir, ".pi", "agent", "settings.json"))
	projectSettings := assertJSONPath(t, filepath.Join(wsDir, ".pi", "settings.json"))
	extensions, ok := projectSettings["extensions"].([]any)
	if !ok || len(extensions) != 1 || extensions[0] != "/opt/pi-extensions/node_modules/pi-mcp-adapter" {
		t.Fatalf("expected project settings to load the agent-image adapter, got %#v", projectSettings["extensions"])
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("expected mcpServers map")
	}
	if _, ok := servers["runner-bridge"]; !ok {
		t.Fatal("expected runner-bridge MCP server")
	}
	if _, ok := servers["chetter"]; !ok {
		t.Fatal("expected chetter MCP server")
	}
	profile, ok := servers["context"].(map[string]any)
	if !ok || profile["auth"] != "bearer" || profile["bearerTokenEnv"] != "EXAMPLE_MCP_TOKEN" {
		t.Fatalf("unexpected task MCP profile: %#v", servers["context"])
	}
}

func TestGenerateConfigWritesCustomProvider(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:         "litellm",
		ProviderName:       "Corporate LiteLLM",
		ProviderBaseURL:    "https://litellm.example.test/v1",
		ProviderAPIKeyEnv:  "LITELLM_API_KEY",
		ProviderAPI:        "openai-completions",
		ProviderAuthHeader: true,
		ModelID:            "coding-model",
	}
	if err := GenerateConfig(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	models := assertJSONPath(t, filepath.Join(wsDir, ".pi", "agent", "models.json"))
	providers := models["providers"].(map[string]any)
	provider := providers["litellm"].(map[string]any)
	if provider["baseUrl"] != "https://litellm.example.test/v1" || provider["api"] != "openai-completions" || provider["apiKey"] != "$LITELLM_API_KEY" || provider["authHeader"] != true {
		t.Fatalf("unexpected provider config: %+v", provider)
	}
	registeredModels := provider["models"].([]any)
	model := registeredModels[0].(map[string]any)
	if model["id"] != "coding-model" {
		t.Fatalf("unexpected registered model: %+v", model)
	}
}

func TestGenerateConfigNoProviderConfigWithoutAPI(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:      "litellm",
		ProviderBaseURL: "https://litellm.example.test/v1",
		ModelID:         "coding-model",
	}
	if err := GenerateConfig(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wsDir, ".pi", "agent", "models.json")); err == nil {
		t.Fatal("models.json should not be written when ProviderAPI is empty")
	}
}

func TestGenerateConfigProviderConfigMissingFields(t *testing.T) {
	tests := []struct {
		name string
		req  task.TaskRequest
	}{
		{
			name: "missing provider_id",
			req:  task.TaskRequest{ProviderAPI: "openai-completions", ProviderBaseURL: "https://x.test", ModelID: "m"},
		},
		{
			name: "missing model_id",
			req:  task.TaskRequest{ProviderAPI: "openai-completions", ProviderID: "litellm", ProviderBaseURL: "https://x.test"},
		},
		{
			name: "missing base_url",
			req:  task.TaskRequest{ProviderAPI: "openai-completions", ProviderID: "litellm", ModelID: "m"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wsDir := t.TempDir()
			if err := GenerateConfig(wsDir, "", "", "", tc.req, false); err == nil {
				t.Fatal("expected error when provider config fields are missing")
			}
		})
	}
}

func TestGenerateConfigProviderConfigWithoutAuthHeader(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:        "litellm",
		ProviderBaseURL:   "https://litellm.example.test/v1",
		ProviderAPIKeyEnv: "LITELLM_API_KEY",
		ProviderAPI:       "openai-completions",
		ModelID:           "coding-model",
	}
	if err := GenerateConfig(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	models := assertJSONPath(t, filepath.Join(wsDir, ".pi", "agent", "models.json"))
	provider := models["providers"].(map[string]any)["litellm"].(map[string]any)
	if _, ok := provider["authHeader"]; ok {
		t.Fatal("authHeader should be omitted when false")
	}
}

func TestGenerateConfigProviderConfigWithoutAPIKey(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:      "litellm",
		ProviderBaseURL: "https://litellm.example.test/v1",
		ProviderAPI:     "openai-completions",
		ModelID:         "coding-model",
	}
	if err := GenerateConfig(wsDir, "", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	models := assertJSONPath(t, filepath.Join(wsDir, ".pi", "agent", "models.json"))
	provider := models["providers"].(map[string]any)["litellm"].(map[string]any)
	if _, ok := provider["apiKey"]; ok {
		t.Fatal("apiKey should be omitted when ProviderAPIKeyEnv is empty")
	}
}

func assertJSONPath(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}
