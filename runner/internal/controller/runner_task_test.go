package controller

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/config"
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
	cmd := (&Runner{}).resolveCommand(task.TaskRequest{Prompt: "Do work"})
	if !hasArg(cmd, "--pure") {
		t.Fatalf("expected --pure without mem9, got %v", cmd)
	}
}

func TestResolveCommand_Mem9EnabledRemovesPure(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "mem9-test-key")
	cmd := (&Runner{}).resolveCommand(task.TaskRequest{Prompt: "Do work"})
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

func TestRunnerOwnedEnv(t *testing.T) {
	if !isRunnerOwnedEnv("MEM9_API_KEY") {
		t.Fatal("MEM9_API_KEY should be runner-owned")
	}
	if !isRunnerOwnedEnv("OPENAI_API_KEY") {
		t.Fatal("OPENAI_API_KEY should be runner-owned")
	}
	if !isRunnerOwnedEnv("DEEPSEEK_API_KEY") {
		t.Fatal("DEEPSEEK_API_KEY should be runner-owned")
	}
	if isRunnerOwnedEnv("LLM_PROVIDER") {
		t.Fatal("LLM_PROVIDER should not be treated as runner-owned env")
	}
}

func TestAddRunnerOwnedEnvUsesRunnerValue(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "runner-key")
	t.Setenv("OPENAI_API_KEY", "runner-openai-key")
	t.Setenv("DEEPSEEK_API_KEY", "runner-deepseek-key")
	env := map[string]string{"MEM9_API_KEY": "task-key", "OPENAI_API_KEY": "task-openai-key", "DEEPSEEK_API_KEY": "task-deepseek-key"}
	addRunnerOwnedEnv(env)
	if env["MEM9_API_KEY"] != "runner-key" {
		t.Fatalf("expected runner mem9 key to win, got %q", env["MEM9_API_KEY"])
	}
	if env["OPENAI_API_KEY"] != "runner-openai-key" {
		t.Fatalf("expected runner openai key to win, got %q", env["OPENAI_API_KEY"])
	}
	if env["DEEPSEEK_API_KEY"] != "runner-deepseek-key" {
		t.Fatalf("expected runner deepseek key to win, got %q", env["DEEPSEEK_API_KEY"])
	}
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

func TestTruncateSummary(t *testing.T) {
	if s := truncateSummary("short"); s != "short" {
		t.Errorf("short text should not be truncated: %q", s)
	}
	long := strings.Repeat("x", maxSummaryBytes+100)
	result := truncateSummary(long)
	if len(result) > maxSummaryBytes+30 {
		t.Errorf("truncated summary too long: %d", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("should include truncation marker: %s", result)
	}
}

func TestShellQuoteArg(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "'hello'"},
		{"it's", "'it'\"'\"'s'"},
		{`"quoted"`, `'"quoted"'`},
	}
	for _, tc := range tests {
		got := shellQuoteArg(tc.in)
		if got != tc.want {
			t.Errorf("shellQuoteArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellQuoteArgs(t *testing.T) {
	result := shellQuoteArgs([]string{"opencode", "run", "--pure"})
	if !strings.Contains(result, "'opencode'") {
		t.Errorf("expected quoted 'opencode': %s", result)
	}
	if !strings.Contains(result, "'run'") {
		t.Errorf("expected quoted 'run': %s", result)
	}
}

func TestEnvValue_FromMap(t *testing.T) {
	env := map[string]string{"KEY": "val"}
	if v := envValue(env, "KEY", "fallback"); v != "val" {
		t.Errorf("expected 'val', got %q", v)
	}
}

func TestEnvValue_Fallback(t *testing.T) {
	env := map[string]string{}
	if v := envValue(env, "MISSING", "default"); v != "default" {
		t.Errorf("expected 'default', got %q", v)
	}
}

func TestEnvValue_EmptyTrimsToFallback(t *testing.T) {
	env := map[string]string{"KEY": "  "}
	if v := envValue(env, "KEY", "fallback"); v != "fallback" {
		t.Errorf("whitespace-only should fall back: got %q", v)
	}
}

func TestGenerateOpenCodeConfig_UsesMCPKeyNotMCPservers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	r := &Runner{
		cfg: &config.Config{
			Runner: config.RunnerConfig{WorkspaceRoot: t.TempDir()},
		},
	}

	wsDir := t.TempDir()
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := r.generateOpenCodeConfig(wsDir, socketPath, false); err != nil {
		t.Fatalf("generateOpenCodeConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".opencode.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if _, ok := parsed["mcpServers"]; ok {
		t.Error("config must not contain 'mcpServers' key — use 'mcp'")
	}

	providers, ok := parsed["provider"].(map[string]any)
	if !ok {
		t.Fatal("expected provider key to be a map")
	}
	if _, ok := providers["synthetic"]; !ok {
		t.Error("expected synthetic provider to be injected")
	}
}

func TestGenerateOpenCodeConfig_ChetterMCPUnderMCPKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	r := &Runner{
		cfg: &config.Config{
			Runner: config.RunnerConfig{WorkspaceRoot: t.TempDir()},
			ChetterMCP: config.ChetterMCPConfig{
				URL:       "https://chetter.example.com/mcp",
				AuthToken: "test-token",
			},
		},
	}

	wsDir := t.TempDir()
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := r.generateOpenCodeConfig(wsDir, socketPath, false); err != nil {
		t.Fatalf("generateOpenCodeConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".opencode.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if _, ok := parsed["mcpServers"]; ok {
		t.Error("config must not contain 'mcpServers' key — use 'mcp'")
	}

	mcps, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatal("expected 'mcp' key to be present with chetter configured")
	}

	chetter, ok := mcps["chetter"].(map[string]any)
	if !ok {
		t.Fatal("expected chetter MCP entry under 'mcp' key")
	}
	if chetter["type"] != "remote" {
		t.Errorf("expected chetter type 'remote', got %v", chetter["type"])
	}
	if chetter["url"] != "https://chetter.example.com/mcp" {
		t.Errorf("unexpected chetter URL: %v", chetter["url"])
	}
	if chetter["enabled"] != true {
		t.Errorf("expected chetter MCP enabled, got %v", chetter["enabled"])
	}
	headers, ok := chetter["headers"].(map[string]any)
	if !ok {
		t.Fatal("expected chetter MCP to include auth headers")
	}
	if headers["Authorization"] != "Bearer test-token" {
		t.Errorf("unexpected auth header: %v", headers["Authorization"])
	}
}

func TestGenerateOpenCodeConfig_MCPBridgeWhenRequested(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RUNNER_LOCAL", "true")

	r := &Runner{
		cfg: &config.Config{
			Runner: config.RunnerConfig{WorkspaceRoot: t.TempDir()},
		},
	}

	wsDir := t.TempDir()
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := r.generateOpenCodeConfig(wsDir, socketPath, true); err != nil {
		t.Fatalf("generateOpenCodeConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".opencode.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if _, ok := parsed["mcpServers"]; ok {
		t.Error("config must not contain 'mcpServers' key — use 'mcp'")
	}

	mcps, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatal("expected 'mcp' key to be present with includeRunnerMCP=true")
	}

	bridge, ok := mcps["runner-bridge"].(map[string]any)
	if !ok {
		t.Fatal("expected runner-bridge MCP bridge under 'mcp' key")
	}
	if bridge["type"] != "local" {
		t.Errorf("expected runner-bridge MCP type 'local', got %v", bridge["type"])
	}
	if bridge["enabled"] != true {
		t.Errorf("expected runner-bridge MCP enabled=true, got %v", bridge["enabled"])
	}
	if _, ok := bridge["command"]; !ok {
		t.Error("expected runner-bridge MCP to have a command")
	}
}

func TestGenerateOpenCodeConfig_NoMCPBridgeWhenNotRequested(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	r := &Runner{
		cfg: &config.Config{
			Runner: config.RunnerConfig{WorkspaceRoot: t.TempDir()},
		},
	}

	wsDir := t.TempDir()
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := r.generateOpenCodeConfig(wsDir, socketPath, false); err != nil {
		t.Fatalf("generateOpenCodeConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".opencode.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	mcps, _ := parsed["mcp"].(map[string]any)
	if mcps != nil {
		if _, ok := mcps["runner-bridge"]; ok {
			t.Error("runner-bridge MCP bridge should NOT be present when includeRunnerMCP=false")
		}
	}
}

func TestGenerateOpenCodeConfig_ValidatedByOpenCode(t *testing.T) {
	if _, err := os.Stat("/home/gokr/.opencode/bin/opencode"); os.IsNotExist(err) {
		t.Skip("opencode binary not found, skipping integration test")
	}

	tests := []struct {
		name          string
		chetterURL    string
		chetterToken  string
		includeBridge bool
	}{
		{
			name: "minimal",
		},
		{
			name:         "with_chetter_mcp",
			chetterURL:   "https://chetter.example.com/mcp",
			chetterToken: "test-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if tt.includeBridge {
				t.Setenv("RUNNER_LOCAL", "true")
			}

			r := &Runner{
				cfg: &config.Config{
					Runner: config.RunnerConfig{WorkspaceRoot: t.TempDir()},
					ChetterMCP: config.ChetterMCPConfig{
						URL:       tt.chetterURL,
						AuthToken: tt.chetterToken,
					},
				},
			}

			wsDir := t.TempDir()
			socketPath := filepath.Join(wsDir, "socket.sock")

			if err := r.generateOpenCodeConfig(wsDir, socketPath, tt.includeBridge); err != nil {
				t.Fatalf("generateOpenCodeConfig failed: %v", err)
			}

			configPath := filepath.Join(wsDir, ".opencode.json")
			if err := validateConfigWithOpenCode(t, configPath, wsDir); err != nil {
				// Dump config on failure for debugging
				data, _ := os.ReadFile(configPath)
				t.Errorf("opencode rejected config:\n%s\nerror: %v", string(data), err)
			}
		})
	}
}

func validateConfigWithOpenCode(t *testing.T, configPath, workDir string) error {
	t.Helper()

	password := generatePassword()
	ln, err := listenTCP()
	if err != nil {
		return fmt.Errorf("allocate port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cmd := exec.Command("opencode", opencodeServeArgs(port)...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"OPENCODE_CONFIG="+configPath,
		"OPENCODE_SERVER_PASSWORD="+password,
		"MEM9_API_KEY=",
	)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Drain stderr in the background so the process does not block when
	// the pipe buffer fills up. The captured output is used only if the
	// server fails to start within the deadline.
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		io.Copy(&stderrBuf, stderr)
		close(stderrDone)
	}()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start opencode serve: %w", err)
	}
	defer func() {
		cmd.Process.Kill()
		<-stderrDone
		cmd.Wait()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}

	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", baseURL+"/config", nil)
		req.Header.Set("Authorization", basicAuthHeader(password))
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil && time.Now().After(deadline) {
		return fmt.Errorf("opencode serve not ready: %w\nstderr: %s", lastErr, stderrBuf.String())
	}

	req, err := http.NewRequest("POST", baseURL+"/session", strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("create session request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", basicAuthHeader(password))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /session: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode session response: %w", err)
	}
	if result.ID == "" {
		return fmt.Errorf("session created but no ID returned")
	}

	t.Logf("session created: %s", result.ID)
	return nil
}

func TestDecorateTaskResponse_NoDefaultsWhenEnvEmpty(t *testing.T) {
	r := &Runner{}
	resp := &task.TaskResponse{TaskID: "test-task"}

	r.decorateTaskResponse(resp, nil, "")

	if resp.ProviderID != "" {
		t.Errorf("expected empty ProviderID when no env/request info, got %q", resp.ProviderID)
	}
	if resp.ModelID != "" {
		t.Errorf("expected empty ModelID when no env/request info, got %q", resp.ModelID)
	}
}

func TestDecorateTaskResponse_UsesEnvValues(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "should-not-be-used")
	t.Setenv("LLM_MODEL_CODER", "should-not-be-used")

	r := &Runner{}
	resp := &task.TaskResponse{TaskID: "test-task"}
	env := map[string]string{
		"LLM_PROVIDER":    "deepseek",
		"LLM_MODEL_CODER": "deepseek-chat",
	}

	r.decorateTaskResponse(resp, env, "")

	if resp.ProviderID != "deepseek" {
		t.Errorf("expected ProviderID from env, got %q", resp.ProviderID)
	}
	if resp.ModelID != "deepseek-chat" {
		t.Errorf("expected ModelID from env, got %q", resp.ModelID)
	}
}

func TestDecorateTaskResponse_UsesOSEnvAsFallback(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LLM_MODEL_CODER", "gpt-5.5")

	r := &Runner{}
	resp := &task.TaskResponse{TaskID: "test-task"}

	r.decorateTaskResponse(resp, nil, "")

	if resp.ProviderID != "openai" {
		t.Errorf("expected ProviderID from os env, got %q", resp.ProviderID)
	}
	if resp.ModelID != "gpt-5.5" {
		t.Errorf("expected ModelID from os env, got %q", resp.ModelID)
	}
}

func TestDecorateTaskResponseForRequest_NoDefaultsWhenRequestHasNoModel(t *testing.T) {
	r := &Runner{}
	resp := &task.TaskResponse{TaskID: "test-task"}
	req := task.TaskRequest{TaskID: "test-task"}

	r.decorateTaskResponseForRequest(resp, req, "")

	if resp.ProviderID != "" {
		t.Errorf("expected empty ProviderID when request has none, got %q", resp.ProviderID)
	}
	if resp.ModelID != "" {
		t.Errorf("expected empty ModelID when request has none, got %q", resp.ModelID)
	}
}

func TestDecorateTaskResponseForRequest_UsesExplicitRequestModel(t *testing.T) {
	r := &Runner{}
	resp := &task.TaskResponse{TaskID: "test-task"}
	req := task.TaskRequest{
		TaskID:     "test-task",
		ProviderID: "deepseek",
		ModelID:    "deepseek-chat",
	}

	r.decorateTaskResponseForRequest(resp, req, "")

	if resp.ProviderID != "deepseek" {
		t.Errorf("expected ProviderID from request, got %q", resp.ProviderID)
	}
	if resp.ModelID != "deepseek-chat" {
		t.Errorf("expected ModelID from request, got %q", resp.ModelID)
	}
}

func TestDecorateTaskResponse_PreservesAlreadySetFields(t *testing.T) {
	r := &Runner{}
	resp := &task.TaskResponse{
		TaskID:     "test-task",
		ProviderID: "anthropic",
		ModelID:    "claude-sonnet",
	}

	r.decorateTaskResponse(resp, nil, "")

	if resp.ProviderID != "anthropic" {
		t.Errorf("expected preserved ProviderID, got %q", resp.ProviderID)
	}
	if resp.ModelID != "claude-sonnet" {
		t.Errorf("expected preserved ModelID, got %q", resp.ModelID)
	}
}

func listenTCP() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

// TestOpenCodeEventScannerBuffer verifies that a bufio.Scanner configured with
// the opencodeEventLineMax buffer can read OpenCode event lines that exceed
// the default 64KB limit. Regression test for the "bufio.Scanner: token too
// long" error seen when OpenCode emits large PR diffs or tool outputs as a
// single JSON event line.
func TestOpenCodeEventScannerBuffer(t *testing.T) {
	const longLineSize = 200 * 1024 // 200 KiB, well above the 64KB default
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
