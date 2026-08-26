package pi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	t.Setenv("PI_PROVIDER", "ambient-provider")
	t.Setenv("PI_MODEL", "ambient-model")
	req := task.TaskRequest{ModelID: "anthropic/claude-sonnet-4-5"}
	got := buildRPCCommand(req)
	want := []string{"pi", "--mode", "rpc", "--no-session", "--offline", "--approve", "--provider", "anthropic", "--model", "claude-sonnet-4-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildRPCCommand() = %#v, want %#v", got, want)
	}
}

func TestModelFieldsPrecedence(t *testing.T) {
	t.Setenv("PI_PROVIDER", "environment-provider")
	t.Setenv("PI_MODEL", "environment-model")

	tests := []struct {
		name         string
		req          task.TaskRequest
		wantProvider string
		wantModel    string
	}{
		{name: "explicit fields", req: task.TaskRequest{ProviderID: "explicit-provider", ModelID: "explicit-model"}, wantProvider: "explicit-provider", wantModel: "explicit-model"},
		{name: "qualified explicit model", req: task.TaskRequest{ModelID: "model-provider/qualified-model"}, wantProvider: "model-provider", wantModel: "qualified-model"},
		{name: "explicit provider with environment model", req: task.TaskRequest{ProviderID: "explicit-provider"}, wantProvider: "explicit-provider", wantModel: "environment-model"},
		{name: "environment defaults", req: task.TaskRequest{}, wantProvider: "environment-provider", wantModel: "environment-model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model := modelFields(tt.req)
			if provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("modelFields() = (%q, %q), want (%q, %q)", provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

func TestResolvedModelID(t *testing.T) {
	req := task.TaskRequest{ProviderID: "zai", ModelID: "glm-5.2"}
	if got := resolvedModelID(req); got != "zai/glm-5.2" {
		t.Fatalf("resolvedModelID() = %q", got)
	}
}

func TestGenerateConfigWritesSettingsAndMCP(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{RunnerMCPToken: "runner-token"}
	if err := GenerateConfig(wsDir, "http://localhost:9999/mcp", "https://chetter.example.com/mcp", "token", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	assertJSONPath(t, filepath.Join(wsDir, ".pi", "agent", "settings.json"))
	projectSettings := assertJSONPath(t, filepath.Join(wsDir, ".pi", "settings.json"))
	extensions, ok := projectSettings["extensions"].([]any)
	if !ok || len(extensions) == 0 {
		t.Fatal("expected project settings to load pi-mcp-adapter extension")
	}
	if extensions[0] != "/opt/pi-extensions/node_modules/pi-mcp-adapter" {
		t.Fatalf("expected default adapter path, got %v", extensions[0])
	}

	mcpPath := filepath.Join(wsDir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	info, err := os.Stat(mcpPath)
	if err != nil {
		t.Fatalf("stat .mcp.json: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("MCP config permissions = %v, want 0600", info.Mode().Perm())
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("expected mcpServers map")
	}
	runner, ok := servers["runner-bridge"].(map[string]any)
	if !ok {
		t.Fatal("expected runner-bridge MCP server")
	}
	headers, ok := runner["headers"].(map[string]any)
	if !ok || headers["Authorization"] != "Bearer runner-token" {
		t.Fatalf("runner bridge headers = %#v", runner["headers"])
	}
	if runner["url"] != "http://localhost:9999/mcp" || runner["lifecycle"] != "keep-alive" {
		t.Fatalf("runner bridge config = %#v", runner)
	}
	if _, ok := runner["idleTimeout"]; ok {
		t.Fatal("runner bridge must not set idleTimeout; the pi MCP adapter treats it as a per-server idle override")
	}
	chetter, ok := servers["chetter"].(map[string]any)
	if !ok {
		t.Fatal("expected chetter MCP server")
	}
	if chetter["url"] != "https://chetter.example.com/mcp" || chetter["lifecycle"] != "keep-alive" {
		t.Fatalf("chetter MCP config = %#v", chetter)
	}
	chetterHeaders, ok := chetter["headers"].(map[string]any)
	if !ok || chetterHeaders["Authorization"] != "Bearer token" {
		t.Fatalf("chetter MCP headers = %#v", chetter["headers"])
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

// gzipTar builds a gzipped tar archive from a map of file path -> content.
func gzipTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestGenerateConfigInjectsSkillsAndPersona(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		Agent: "code-rev",
		AgentDefinition: `# Code Rev
You are a meticulous code reviewer. Focus on correctness and edge cases.`,
		SkillDefinitions: map[string][]byte{
			"review-checklist": gzipTar(t, map[string]string{
				"SKILL.md": "# Review Checklist\nCheck edge cases.",
			}),
		},
	}
	if err := GenerateConfig(wsDir, "http://localhost:9999/mcp", "https://chetter.example.com/mcp", "token", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	// Skill extracted to .pi/skills/<name>/.
	skillPath := filepath.Join(wsDir, ".pi", "skills", "review-checklist", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if !bytes.Contains(data, []byte("Review Checklist")) {
		t.Fatalf("skill content = %q", data)
	}

	// Persona written to .pi/agent/system-prompt.md.
	personaPath := filepath.Join(wsDir, ".pi", "agent", "system-prompt.md")
	data, err = os.ReadFile(personaPath)
	if err != nil {
		t.Fatalf("read persona: %v", err)
	}
	if !bytes.Contains(data, []byte("meticulous code reviewer")) {
		t.Fatalf("persona content = %q", data)
	}
	info, err := os.Stat(personaPath)
	if err != nil {
		t.Fatalf("stat persona: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("persona permissions = %v, want 0600", info.Mode().Perm())
	}
}

func TestBuildRPCCommandSystemPromptWhenAgentPresent(t *testing.T) {
	req := task.TaskRequest{Agent: "code-rev", AgentDefinition: "# Code Rev"}
	got := buildRPCCommand(req)
	found := false
	for i, a := range got {
		if a == "--system-prompt" {
			if i+1 >= len(got) || got[i+1] != filepath.Join(".pi", "agent", "system-prompt.md") {
				t.Fatalf("--system-prompt arg = %v, want %v", got[i+1], filepath.Join(".pi", "agent", "system-prompt.md"))
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("buildRPCCommand() = %v, want --system-prompt for a task agent", got)
	}
}

func TestBuildRPCCommandNoSystemPromptWithoutAgent(t *testing.T) {
	for _, a := range buildRPCCommand(task.TaskRequest{}) {
		if a == "--system-prompt" {
			t.Fatal("--system-prompt should be omitted when no agent definition is present")
		}
	}
}
