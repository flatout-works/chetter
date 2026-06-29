package opencode

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestBasicAuthHeader(t *testing.T) {
	h := basicAuthHeader("s3cret")
	if !strings.HasPrefix(h, "Basic ") {
		t.Fatalf("expected Basic auth header, got %q", h)
	}
	decoded, err := base64.StdEncoding.DecodeString(h[6:])
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}
	if string(decoded) != "opencode:s3cret" {
		t.Fatalf("expected opencode:s3cret, got %q", string(decoded))
	}
}

func TestBasicAuthHeader_NotBearer(t *testing.T) {
	h := basicAuthHeader("any-value")
	if strings.Contains(h, "Bearer") {
		t.Fatalf("auth header must not contain Bearer (regression: opencode uses Basic auth). got %q", h)
	}
}

func TestGeneratePassword(t *testing.T) {
	p1 := generatePassword()
	if len(p1) != 64 {
		t.Fatalf("expected 64 hex chars (32 bytes), got %d", len(p1))
	}
	p2 := generatePassword()
	if p1 == p2 {
		t.Fatalf("generated passwords should be unique")
	}
}

func TestModelFlag_FullConfig(t *testing.T) {
	env := map[string]string{
		"LLM_PROVIDER":    "devpass",
		"LLM_MODEL_CODER": "gpt-5.5",
	}
	result := modelFlag(task.TaskRequest{Env: env})
	if result != "devpass/gpt-5.5" {
		t.Fatalf("expected devpass/gpt-5.5, got %q", result)
	}
}

func TestModelFlag_ModelOnly(t *testing.T) {
	env := map[string]string{
		"LLM_MODEL_CODER": "gpt-5.5",
	}
	result := modelFlag(task.TaskRequest{Env: env})
	if result != "gpt-5.5" {
		t.Fatalf("expected gpt-5.5, got %q", result)
	}
}

func TestModelFlag_NoConfig(t *testing.T) {
	env := map[string]string{}
	result := modelFlag(task.TaskRequest{Env: env})
	if result != "" {
		t.Fatalf("expected empty string when no LLM config, got %q", result)
	}
}

func TestModelFlag_PartialProvider(t *testing.T) {
	env := map[string]string{
		"LLM_PROVIDER": "devpass",
	}
	result := modelFlag(task.TaskRequest{Env: env})
	if result != "" {
		t.Fatalf("expected empty string when model is missing (provider alone is insufficient), got %q", result)
	}
}

func TestModelFlag_ExplicitTaskModelWins(t *testing.T) {
	env := map[string]string{
		"LLM_PROVIDER":    "devpass",
		"LLM_MODEL_CODER": "gpt-5.5",
	}
	result := modelFlag(task.TaskRequest{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", Env: env})
	if result != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("expected explicit model to win, got %q", result)
	}
}

func TestResolvedChetterModelID_ExplicitModel(t *testing.T) {
	req := task.TaskRequest{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6"}
	if got := resolvedChetterModelID(req); got != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("expected explicit model, got %q", got)
	}
}

func TestResolvedChetterModelID_FallsBackToEnv(t *testing.T) {
	req := task.TaskRequest{Env: map[string]string{
		"LLM_PROVIDER":    "devpass",
		"LLM_MODEL_CODER": "gpt-5.5",
	}}
	if got := resolvedChetterModelID(req); got != "devpass/gpt-5.5" {
		t.Fatalf("expected env fallback, got %q", got)
	}
}

func TestResolvedChetterModelID_DefaultsWhenEmpty(t *testing.T) {
	req := task.TaskRequest{}
	if got := resolvedChetterModelID(req); got != "synthetic/hf:zai-org/GLM-5.2" {
		t.Fatalf("expected default model, got %q", got)
	}
}

func TestPromptWithSkillHints(t *testing.T) {
	result := promptWithSkillHints("Do work", []string{"update-docs-from-git", "openapi"})
	if !strings.Contains(result, "Requested OpenCode skills: update-docs-from-git, openapi.") {
		t.Fatalf("expected skills prefix, got %q", result)
	}
	if !strings.HasSuffix(result, "Do work") {
		t.Fatalf("expected original prompt suffix, got %q", result)
	}
}

func TestGenerateConfigForTaskAddsSelectedProvider(t *testing.T) {
	t.Setenv("SYNTHETIC_OPENAI_KEY", "test-key")
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:        "synthetic-openai",
		ProviderName:      "Synthetic OpenAI",
		ProviderBaseURL:   "https://api.example.test/openai",
		ProviderAPIKeyEnv: "SYNTHETIC_OPENAI_KEY",
		ModelID:           "mapped-model",
	}
	if err := GenerateConfigForTask(wsDir, "", "", "", false, req, false); err != nil {
		t.Fatalf("GenerateConfigForTask failed: %v", err)
	}
	data, err := os.ReadFile(wsDir + "/.opencode.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	providers := cfg["provider"].(map[string]any)
	provider := providers["synthetic-openai"].(map[string]any)
	if provider["name"] != "Synthetic OpenAI" || provider["baseURL"] != "https://api.example.test/openai" || provider["apiKey"] != "test-key" {
		t.Fatalf("unexpected provider config: %+v", provider)
	}
	models := provider["models"].(map[string]any)
	if _, ok := models["mapped-model"]; !ok {
		t.Fatalf("expected mapped-model in provider models: %+v", models)
	}
}

func TestGenerateConfigForTaskAddsMCPProfiles(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		MCPProfiles: []task.MCPProfile{{
			Name:          "review-tools",
			Transport:     "http",
			URL:           "http://review-tools:8080/mcp",
			ToolAllowlist: []string{"chetter_submit_task", "chetter_task_status"},
		}},
	}
	if err := GenerateConfigForTask(wsDir, "", "", "", false, req, false); err != nil {
		t.Fatalf("GenerateConfigForTask failed: %v", err)
	}
	data, err := os.ReadFile(wsDir + "/.opencode.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	mcps := cfg["mcp"].(map[string]any)
	server := mcps["review-tools"].(map[string]any)
	if server["type"] != "remote" || server["url"] != "http://review-tools:8080/mcp" || server["enabled"] != true {
		t.Fatalf("unexpected MCP server config: %+v", server)
	}
	if _, ok := server["headers"]; ok {
		t.Fatalf("unexpected headers for public MCP profile: %+v", server)
	}
	perms := cfg["permission"].(map[string]any)
	if perms["review-tools_*"] != "deny" {
		t.Fatalf("missing documented deny wildcard for review-tools: %+v", perms)
	}
	if perms["mcp__review-tools__*"] != "deny" {
		t.Fatalf("missing legacy deny wildcard for review-tools: %+v", perms)
	}
	if perms["review-tools_chetter_submit_task"] != "allow" {
		t.Fatalf("missing documented chetter_submit_task permission: %+v", perms)
	}
	if perms["mcp__review-tools__chetter_submit_task"] != "allow" {
		t.Fatalf("missing chetter_submit_task permission: %+v", perms)
	}
	if effectivePermissionForTest(perms, "review-tools_dangerous_tool") != "deny" {
		t.Fatalf("dangerous_tool should be denied by wildcard: %+v", perms)
	}
	if effectivePermissionForTest(perms, "mcp__review-tools__dangerous_tool") != "deny" {
		t.Fatalf("legacy dangerous_tool should be denied by wildcard: %+v", perms)
	}
	if effectivePermissionForTest(perms, "review-tools_chetter_submit_task") != "allow" {
		t.Fatalf("allowed tool should override wildcard deny: %+v", perms)
	}
	if perms["mcp__review-tools__chetter_task_status"] != "allow" {
		t.Fatalf("missing chetter_task_status permission: %+v", perms)
	}
}

func TestGenerateConfigForTaskAddsCredentialedMCPProfileWithoutAllowlist(t *testing.T) {
	t.Setenv("CHETTER_ORCHESTRATOR_TOKEN", "secret-token")
	wsDir := t.TempDir()
	req := task.TaskRequest{
		MCPProfiles: []task.MCPProfile{{
			Name:      "private-tools",
			Transport: "http",
			URL:       "http://private-tools:8080/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:CHETTER_ORCHESTRATOR_TOKEN}",
			},
		}},
	}
	if err := GenerateConfigForTask(wsDir, "", "", "", false, req, false); err != nil {
		t.Fatalf("GenerateConfigForTask failed: %v", err)
	}
	data, err := os.ReadFile(wsDir + "/.opencode.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	mcps := cfg["mcp"].(map[string]any)
	server := mcps["private-tools"].(map[string]any)
	headers := server["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer secret-token" {
		t.Fatalf("Authorization header = %v", headers["Authorization"])
	}
}

func TestGenerateConfigForTaskRejectsCredentialedAllowlistedMCPProfile(t *testing.T) {
	t.Setenv("CHETTER_ORCHESTRATOR_TOKEN", "secret-token")
	wsDir := t.TempDir()
	req := task.TaskRequest{
		MCPProfiles: []task.MCPProfile{{
			Name: "private-tools",
			URL:  "http://private-tools:8080/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:CHETTER_ORCHESTRATOR_TOKEN}",
			},
			ToolAllowlist: []string{"safe_tool"},
		}},
	}
	err := GenerateConfigForTask(wsDir, "", "", "", false, req, false)
	if err == nil {
		t.Fatal("expected credentialed allowlist rejection")
	}
	if !strings.Contains(err.Error(), "would expose unrestricted credentials") {
		t.Fatalf("error = %q, want credential exposure message", err)
	}
}

func TestGenerateConfigForTaskRejectsAllowlistedConfiguredChetterMCP(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		MCPProfiles: []task.MCPProfile{{
			Name:          "chetter-orchestration",
			URL:           "http://chetter-mcp:8080/mcp/",
			ToolAllowlist: []string{"chetter_submit_task"},
		}},
	}
	err := GenerateConfigForTask(wsDir, "", "http://chetter-mcp:8080/mcp", "secret-token", false, req, false)
	if err == nil {
		t.Fatal("expected configured Chetter MCP allowlist rejection")
	}
	if !strings.Contains(err.Error(), "OpenCode Chetter MCP config would expose unrestricted credentials") {
		t.Fatalf("error = %q, want Chetter MCP credential exposure message", err)
	}
}

func TestOpenCodeServeArgs_NoPure(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "")
	args := opencodeServeArgs(1234)
	if hasArg(args, "--pure") {
		t.Fatalf("unexpected --pure in serve args, got %v", args)
	}
	if !hasArg(args, "--port") {
		t.Fatalf("expected --port in serve args, got %v", args)
	}
}

func effectivePermissionForTest(perms map[string]any, tool string) string {
	keys := make([]string, 0, len(perms))
	for key := range perms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var action string
	for _, key := range keys {
		if key == tool || (strings.HasSuffix(key, "*") && strings.HasPrefix(tool, strings.TrimSuffix(key, "*"))) {
			if value, ok := perms[key].(string); ok {
				action = value
			}
		}
	}
	return action
}

func TestOpenCodeServeArgs_NoPureWithMem9(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "mem9-test-key")
	args := opencodeServeArgs(1234)
	if hasArg(args, "--pure") {
		t.Fatalf("unexpected --pure in serve args with mem9, got %v", args)
	}
	if !hasArg(args, "--port") {
		t.Fatalf("expected --port in serve args, got %v", args)
	}
}

func TestEnsureMem9Plugin(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "mem9-test-key")
	t.Setenv("MEM9_PLUGIN_SPEC", "")
	cfg := map[string]any{"plugin": []any{"existing-plugin"}}
	ensureMem9Plugin(cfg)
	plugins := cfg["plugin"].([]any)
	if !hasAny(plugins, "existing-plugin") || !hasAny(plugins, defaultMem9PluginSpec) {
		t.Fatalf("expected existing plugin and mem9 plugin, got %#v", plugins)
	}
}

func TestEnsureMem9PluginOverrideDedupes(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "mem9-test-key")
	t.Setenv("MEM9_PLUGIN_SPEC", "@mem9/opencode@0.1.3")
	cfg := map[string]any{"plugin": []any{"@mem9/opencode@0.1.3"}}
	ensureMem9Plugin(cfg)
	plugins := cfg["plugin"].([]any)
	if len(plugins) != 1 || plugins[0] != "@mem9/opencode@0.1.3" {
		t.Fatalf("expected deduped override plugin, got %#v", plugins)
	}
}

func TestEnsureProvider_AddsMissing(t *testing.T) {
	cfg := map[string]any{}
	ensureProvider(cfg, "synthetic")
	providers, ok := cfg["provider"].(map[string]any)
	if !ok {
		t.Fatal("expected provider key to be a map")
	}
	if _, ok := providers["synthetic"]; !ok {
		t.Fatal("expected synthetic provider to be added")
	}
}

func TestEnsureProvider_PreservesExisting(t *testing.T) {
	cfg := map[string]any{
		"provider": map[string]any{
			"devpass": map[string]any{"name": "DevPass"},
		},
	}
	ensureProvider(cfg, "synthetic")
	providers := cfg["provider"].(map[string]any)
	if _, ok := providers["devpass"]; !ok {
		t.Fatal("expected devpass provider to be preserved")
	}
	if _, ok := providers["synthetic"]; !ok {
		t.Fatal("expected synthetic provider to be added")
	}
}

func TestOpenCodeEventScannerBuffer(t *testing.T) {
	const longLineSize = 200 * 1024
	longLine := strings.Repeat("x", longLineSize)
	input := "data: " + longLine + "\n\n"

	br := bufio.NewReader(strings.NewReader(input))
	got, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString failed: %v", err)
	}
	got = strings.TrimRight(got, "\n\r")
	if !strings.HasPrefix(got, "data: ") {
		t.Fatalf("unexpected first line: %q", got)
	}
	if len(got) < longLineSize {
		t.Fatalf("expected line >= %d bytes, got %d", longLineSize, len(got))
	}
}

func TestAgentModelFromConfig(t *testing.T) {
	wsDir := t.TempDir()
	agentDir := filepath.Join(wsDir, ".opencode", "agent")
	if err := os.MkdirAll(agentDir, 0750); err != nil {
		t.Fatal(err)
	}

	writeAgent := func(name, content string) {
		if err := os.WriteFile(filepath.Join(agentDir, name+".md"), []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("reads provider/model", func(t *testing.T) {
		writeAgent("test-agent", "description: Does things\nmodel: opencode/deepseek-v4-flash-free\nmode: primary\n")
		prov, mdl := agentModelFromConfig(wsDir, "test-agent")
		if prov != "opencode" || mdl != "deepseek-v4-flash-free" {
			t.Errorf("expected opencode/deepseek-v4-flash-free, got %s/%s", prov, mdl)
		}
	})

	t.Run("model only without slash", func(t *testing.T) {
		writeAgent("no-provider", "description: test\nmodel: deepseek-v4-flash-free\n")
		prov, mdl := agentModelFromConfig(wsDir, "no-provider")
		if prov != "" || mdl != "deepseek-v4-flash-free" {
			t.Errorf("expected ''/deepseek-v4-flash-free, got %s/%s", prov, mdl)
		}
	})

	t.Run("missing model field returns empty", func(t *testing.T) {
		writeAgent("no-model", "description: just docs\nmode: primary\n")
		prov, mdl := agentModelFromConfig(wsDir, "no-model")
		if prov != "" || mdl != "" {
			t.Errorf("expected empty, got %s/%s", prov, mdl)
		}
	})

	t.Run("empty agent name returns empty", func(t *testing.T) {
		prov, mdl := agentModelFromConfig(wsDir, "")
		if prov != "" || mdl != "" {
			t.Errorf("expected empty, got %s/%s", prov, mdl)
		}
	})

	t.Run("nonexistent agent returns empty", func(t *testing.T) {
		prov, mdl := agentModelFromConfig(wsDir, "nope")
		if prov != "" || mdl != "" {
			t.Errorf("expected empty, got %s/%s", prov, mdl)
		}
	})

	t.Run("empty wsDir returns empty", func(t *testing.T) {
		prov, mdl := agentModelFromConfig("", "test-agent")
		if prov != "" || mdl != "" {
			t.Errorf("expected empty, got %s/%s", prov, mdl)
		}
	})

	t.Run("model with leading whitespace", func(t *testing.T) {
		writeAgent("spaces", "description: test\n   model:   opencode/foo  \nmode: primary\n")
		prov, mdl := agentModelFromConfig(wsDir, "spaces")
		if prov != "opencode" || mdl != "foo" {
			t.Errorf("expected opencode/foo, got %s/%s", prov, mdl)
		}
	})
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasAny(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSummarizeOpenCodeEvent(t *testing.T) {
	t.Run("text type", func(t *testing.T) {
		raw := `{"type":"text","text":"hello"}`
		got := summarizeEvent(raw)
		if got != "text" {
			t.Errorf("expected 'text', got %q", got)
		}
	})
	t.Run("tool_use type", func(t *testing.T) {
		raw := `{"type":"tool_use","properties":{"name":"read_file"}}`
		got := summarizeEvent(raw)
		if !strings.Contains(got, "tool_use") {
			t.Errorf("expected tool_use in summary, got %q", got)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		got := summarizeEvent("not json")
		if got == "" {
			t.Errorf("expected non-empty result for invalid json, got %q", got)
		}
		if len(got) > 303 {
			t.Errorf("expected truncation at 300+3 chars, got %d", len(got))
		}
	})
	t.Run("empty type", func(t *testing.T) {
		raw := `{"type":"","data":{}}`
		got := summarizeEvent(raw)
		if got != "" {
			t.Errorf("expected empty for empty type, got %q", got)
		}
	})
}

func TestCompactJSON(t *testing.T) {
	t.Run("simple map", func(t *testing.T) {
		m := map[string]any{"key": "value"}
		got := compactJSON(m)
		if !strings.Contains(got, "key") || !strings.Contains(got, "value") {
			t.Errorf("expected key/value in output, got %q", got)
		}
	})
	t.Run("large value truncation", func(t *testing.T) {
		m := map[string]any{"data": strings.Repeat("x", 600)}
		got := compactJSON(m)
		if len(got) > 503 {
			t.Errorf("expected truncation at 500+3 chars, got %d", len(got))
		}
	})
	t.Run("nil value", func(t *testing.T) {
		got := compactJSON(nil)
		if got != "" {
			t.Errorf("expected empty for nil, got %q", got)
		}
	})
}

func TestSummarizeOpenCodeEvent_Truncation(t *testing.T) {
	longText := strings.Repeat("a", 400)
	raw, _ := json.Marshal(map[string]any{"type": "unknown", "data": longText})
	got := summarizeEvent(string(raw))
	if got != "unknown" {
		t.Errorf("expected 'unknown' for default type, got %q", got)
	}
}
