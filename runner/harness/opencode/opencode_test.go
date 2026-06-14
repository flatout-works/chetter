package opencode

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
	if got := resolvedChetterModelID(req); got != "synthetic/hf:zai-org/GLM-5.1" {
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

func TestResolveCommand_Mem9DisabledKeepsPure(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "")
	cmd := resolveCommand(task.TaskRequest{Prompt: "Do work"})
	if !hasArg(cmd, "--pure") {
		t.Fatalf("expected --pure without mem9, got %v", cmd)
	}
}

func TestResolveCommand_Mem9EnabledRemovesPure(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "mem9-test-key")
	cmd := resolveCommand(task.TaskRequest{Prompt: "Do work"})
	if hasArg(cmd, "--pure") {
		t.Fatalf("did not expect --pure with mem9 enabled, got %v", cmd)
	}
}

func TestOpenCodeServeArgs_Mem9DisabledKeepsPure(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "")
	args := opencodeServeArgs(1234)
	if !hasArg(args, "--pure") {
		t.Fatalf("expected --pure without mem9, got %v", args)
	}
}

func TestOpenCodeServeArgs_Mem9EnabledRemovesPure(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "mem9-test-key")
	args := opencodeServeArgs(1234)
	if hasArg(args, "--pure") {
		t.Fatalf("did not expect --pure with mem9 enabled, got %v", args)
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

	scanner := bufio.NewScanner(strings.NewReader(input))
	scanner.Buffer(make([]byte, 0, 64*1024), opencodeEventLineMax)
	if !scanner.Scan() {
		t.Fatalf("scanner.Scan failed: %v", scanner.Err())
	}
	got := scanner.Text()
	if !strings.HasPrefix(got, "data: ") {
		t.Fatalf("unexpected first line: %q", got)
	}
	if len(got) < longLineSize {
		t.Fatalf("expected line >= %d bytes, got %d", longLineSize, len(got))
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner.Err after long line: %v", err)
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

func TestSummarizeJSONL(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		got := summarizeJSONL("")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
	t.Run("valid jsonl with text event", func(t *testing.T) {
		line := `{"type":"text","part":{"text":"hello world"}}`
		got := summarizeJSONL(line)
		if !strings.Contains(got, "hello world") {
			t.Errorf("expected text content in summary, got %q", got)
		}
	})
	t.Run("multiple lines", func(t *testing.T) {
		line1 := `{"type":"text","part":{"text":"hello"}}`
		line2 := `{"type":"text","part":{"text":"world"}}`
		got := summarizeJSONL(line1 + "\n" + line2)
		if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
			t.Errorf("expected both texts in summary, got %q", got)
		}
	})
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
