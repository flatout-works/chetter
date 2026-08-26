package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/harness/claude"
	"github.com/flatout-works/chetter/runner/harness/codex"
	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/harness/pi"
	"github.com/flatout-works/chetter/runner/internal/agentenv"
	"github.com/flatout-works/chetter/runner/internal/config"
	runnermcp "github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/network"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestRunnerOwnedEnv(t *testing.T) {
	for _, key := range []string{
		"CHETTER_TASK_ID", "CHETTER_AGENT_SESSION_ID", "CHETTER_USER_PROMPT_ID", "CHETTER_EXECUTION_ID",
		"MEM9_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_SERVE_PROXY_TOKEN",
	} {
		if !agentenv.IsRunnerOwnedEnv(key) {
			t.Fatalf("%s should be runner-owned", key)
		}
	}
	if agentenv.IsRunnerOwnedEnv("LLM_PROVIDER") {
		t.Fatal("LLM_PROVIDER should not be treated as runner-owned env")
	}
}

func TestTokenUsageAccumulatorConcurrentAdds(t *testing.T) {
	var usage tokenUsageAccumulator
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			usage.add(task.TokenUsage{InputTokens: 2, OutputTokens: 1, CacheReadTokens: 3, CostCents: 1})
		})
	}
	wg.Wait()
	got := usage.snapshot()
	if got.InputTokens != 200 || got.OutputTokens != 100 || got.CacheReadTokens != 300 || got.CostCents != 100 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestTokenUsageAccumulatorDelta(t *testing.T) {
	var a tokenUsageAccumulator

	// Initial delta should be zero.
	d := a.delta()
	if d != (task.TokenUsage{}) {
		t.Fatalf("initial delta = %+v, want zero", d)
	}

	// Add some tokens.
	a.add(task.TokenUsage{InputTokens: 10, OutputTokens: 5, CostCents: 1})

	// First delta should reflect the added tokens.
	d = a.delta()
	if d.InputTokens != 10 || d.OutputTokens != 5 || d.CostCents != 1 {
		t.Fatalf("first delta = %+v", d)
	}

	// Second delta should be zero since nothing new was added.
	d = a.delta()
	if d != (task.TokenUsage{}) {
		t.Fatalf("second delta = %+v, want zero", d)
	}

	// Add more and check delta is only the increment.
	a.add(task.TokenUsage{InputTokens: 3, OutputTokens: 2})
	d = a.delta()
	if d.InputTokens != 3 || d.OutputTokens != 2 || d.CostCents != 0 {
		t.Fatalf("incremental delta = %+v", d)
	}

	// Snapshot still returns the total.
	got := a.snapshot()
	if got.InputTokens != 13 || got.OutputTokens != 7 || got.CostCents != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestGitCloneCredentialDirLeavesWorkspaceEmpty(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "task", "workspace")
	if err := os.MkdirAll(workspace, 0750); err != nil {
		t.Fatal(err)
	}

	credentialDir := agentenv.GitCloneCredentialDir(workspace)
	if credentialDir == workspace {
		t.Fatal("clone credential directory must be outside the workspace")
	}
	if err := agentenv.WriteGitAskpass(credentialDir); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace must remain empty before clone, found %v", entries)
	}
}

func TestAddRunnerOwnedEnvUsesRunnerValue(t *testing.T) {
	t.Setenv("MEM9_API_KEY", "runner-key")
	t.Setenv("OPENAI_API_KEY", "runner-openai-key")
	t.Setenv("DEEPSEEK_API_KEY", "runner-deepseek-key")
	env := map[string]string{"MEM9_API_KEY": "task-key", "OPENAI_API_KEY": "task-openai-key", "DEEPSEEK_API_KEY": "task-deepseek-key"}
	agentenv.AddRunnerOwnedEnv(env)
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

func TestProviderCredentialEnvUsesResolvedRunnerCredential(t *testing.T) {
	t.Setenv("LITELLM_API_KEY", "runner-litellm-key")
	got := agentenv.ProviderCredentialEnv(task.TaskRequest{ProviderAPIKeyEnv: "LITELLM_API_KEY"})
	if len(got) != 1 || got[0] != "LITELLM_API_KEY=runner-litellm-key" {
		t.Fatalf("providerCredentialEnv() = %v", got)
	}
}

func TestProviderCredentialEnvEmptyKeyReturnsNil(t *testing.T) {
	got := agentenv.ProviderCredentialEnv(task.TaskRequest{})
	if got != nil {
		t.Fatalf("providerCredentialEnv() with empty key should return nil, got %v", got)
	}
}

func TestProviderCredentialEnvUnsetVarReturnsNil(t *testing.T) {
	got := agentenv.ProviderCredentialEnv(task.TaskRequest{ProviderAPIKeyEnv: "UNSET_LITELLM_KEY"})
	if got != nil {
		t.Fatalf("providerCredentialEnv() with unset env var should return nil, got %v", got)
	}
}

func TestManagedEnvRejectsTaskProviderCredential(t *testing.T) {
	req := task.TaskRequest{
		ProviderAPIKeyEnv: "LITELLM_API_KEY",
		McpEndpoints:      []task.MCPEndpoint{{BearerTokenEnv: "CONTEXT_MCP_TOKEN"}},
	}
	if !agentenv.IsManagedEnv("LITELLM_API_KEY", req) {
		t.Fatal("catalog-selected provider credential should be runner-managed")
	}
	if !agentenv.IsManagedEnv("OPENAI_API_KEY", req) {
		t.Fatal("existing runner credential should remain runner-managed")
	}
	if !agentenv.IsManagedEnv("CONTEXT_MCP_TOKEN", req) {
		t.Fatal("MCP endpoint token should be runner-managed")
	}
	if agentenv.IsManagedEnv("CUSTOM_ENV", req) {
		t.Fatal("unrelated task environment should be allowed")
	}
}

func TestManagedEnvEmptyProviderKeyDoesNotMatch(t *testing.T) {
	req := task.TaskRequest{}
	if agentenv.IsManagedEnv("", req) {
		t.Fatal("empty key should not be managed when ProviderAPIKeyEnv is empty")
	}
	if agentenv.IsManagedEnv("CUSTOM_ENV", req) {
		t.Fatal("non-runner-owned env should not be managed without a provider key")
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
		{"hello", "hello"},
		{"it's", "'it'\\''s'"},
		{`"quoted"`, `'"quoted"'`},
	}
	for _, tc := range tests {
		got := agentenv.ShellQuoteArg(tc.in)
		if got != tc.want {
			t.Errorf("shellQuoteArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellQuoteArgs(t *testing.T) {
	result := agentenv.ShellQuoteArgs([]string{"opencode", "run", "--pure"})
	if !strings.HasPrefix(result, "opencode") {
		t.Errorf("expected 'opencode' at start: %s", result)
	}
	if !strings.Contains(result, "run") {
		t.Errorf("expected 'run': %s", result)
	}
}

func TestFirstField(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "single", in: "172.18.0.4\n", want: "172.18.0.4"},
		{name: "multiple", in: "172.18.0.4 172.19.0.6\n", want: "172.18.0.4"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstField(tc.in); got != tc.want {
				t.Fatalf("firstField(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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

	wsDir := t.TempDir()

	if err := opencode.GenerateConfig(wsDir, "http://localhost:9999/mcp", "", "", false, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
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

	if providers, ok := parsed["provider"].(map[string]any); !ok || len(providers) != 0 {
		t.Fatalf("expected empty provider map when task has no resolved provider, got %+v", parsed["provider"])
	}
}

func TestGenerateOpenCodeConfig_ChetterMCPUnderMCPKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	wsDir := t.TempDir()

	if err := opencode.GenerateConfig(wsDir, "http://localhost:9999/mcp", "https://chetter.example.com/mcp", "test-token", false, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
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
	if chetter["oauth"] != false {
		t.Errorf("expected chetter MCP OAuth disabled, got %v", chetter["oauth"])
	}
	headers, ok := chetter["headers"].(map[string]any)
	if !ok {
		t.Fatal("expected chetter MCP to include auth headers")
	}
	if headers["Authorization"] != "Bearer test-token" {
		t.Errorf("unexpected auth header: %v", headers["Authorization"])
	}

	// Verify chetter MCP tool permissions are injected.
	perms, ok := parsed["permission"].(map[string]any)
	if !ok {
		t.Fatal("expected 'permission' key in config")
	}
	if v := perms["mcp__chetter__chetter_list_tasks"]; v != "allow" {
		t.Errorf("expected mcp__chetter__chetter_list_tasks permission 'allow', got %v", v)
	}
	if v := perms["mcp__chetter__chetter_task_export"]; v != "allow" {
		t.Errorf("expected mcp__chetter__chetter_task_export permission 'allow', got %v", v)
	}
	if v := perms["mcp__chetter__chetter_create_definition_proposal"]; v != "allow" {
		t.Errorf("expected mcp__chetter__chetter_create_definition_proposal permission 'allow', got %v", v)
	}
	// Admin-only tools should NOT be present.
	if _, ok := perms["mcp__chetter__chetter_delete_token"]; ok {
		t.Error("admin-only tool chetter_delete_token should not be in permissions")
	}
}

func TestGenerateOpenCodeConfig_MCPBridgeWhenRequested(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	wsDir := t.TempDir()

	if err := opencode.GenerateConfig(wsDir, "http://localhost:9999/mcp", "", "", true, true); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
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
	if bridge["type"] != "remote" {
		t.Errorf("expected runner-bridge MCP type 'remote', got %v", bridge["type"])
	}
	if bridge["enabled"] != true {
		t.Errorf("expected runner-bridge MCP enabled=true, got %v", bridge["enabled"])
	}
	if _, ok := bridge["url"]; !ok {
		t.Error("expected runner-bridge MCP to have a url")
	}
}

func TestGenerateOpenCodeConfig_NoMCPBridgeWhenNotRequested(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	wsDir := t.TempDir()

	if err := opencode.GenerateConfig(wsDir, "http://localhost:9999/mcp", "", "", false, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
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
			name:          "with_chetter_mcp",
			chetterURL:    "https://chetter.example.com/mcp",
			chetterToken:  "test-token",
			includeBridge: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			wsDir := t.TempDir()

			if err := opencode.GenerateConfig(wsDir, "http://localhost:9999/mcp", tt.chetterURL, tt.chetterToken, tt.includeBridge, false); err != nil {
				t.Fatalf("GenerateConfig failed: %v", err)
			}

			configPath := filepath.Join(wsDir, ".opencode.json")
			if err := validateConfigWithOpenCode(t, configPath, wsDir); err != nil {
				data, _ := os.ReadFile(configPath)
				t.Errorf("opencode rejected config:\n%s\nerror: %v", string(data), err)
			}
		})
	}
}

func validateConfigWithOpenCode(t *testing.T, configPath, workDir string) error {
	t.Helper()

	h := opencode.New()
	password := h.ServerPassword()
	ln, err := listenTCP()
	if err != nil {
		return fmt.Errorf("allocate port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cmd := exec.Command("opencode", h.ServeCommand(port)[1:]...)
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

func basicAuthHeader(password string) string {
	auth := base64.StdEncoding.EncodeToString([]byte("opencode:" + password))
	return "Basic " + auth
}

func TestSelectHarnessByName_Pi(t *testing.T) {
	h := selectHarnessByName("pi")
	if h.Name() != "pi" {
		t.Fatalf("expected pi harness, got %s", h.Name())
	}
	if _, ok := h.(*pi.Pi); !ok {
		t.Fatalf("expected *pi.Pi, got %T", h)
	}
	if _, ok := h.(harness.RPCHarness); !ok {
		t.Fatal("pi should support RPC")
	}
}

func TestPiRPCCompletionLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		events []map[string]any
		want   bool
	}{
		{name: "agent end", events: []map[string]any{{"type": "agent_end", "willRetry": false}}, want: true},
		{name: "agent settled", events: []map[string]any{{"type": "agent_settled"}}, want: true},
		{name: "agent end retry", events: []map[string]any{{"type": "agent_end", "willRetry": true}}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &rpcAgentState{lastPublished: time.Now(), activeTools: make(map[string]struct{})}
			r := &Runner{}
			for _, event := range tc.events {
				if err := r.handleRPCEvent(task.TaskRequest{}, io.Discard, event, state); err != nil {
					t.Fatal(err)
				}
			}
			if state.terminal != tc.want {
				t.Fatalf("terminal = %v, want %v", state.terminal, tc.want)
			}
		})
	}
}

func TestPiRPCMissingTerminalEOFFallback(t *testing.T) {
	state := &rpcAgentState{lastPublished: time.Now(), activeTools: make(map[string]struct{})}
	r := &Runner{}
	messageEnd := map[string]any{
		"type":    "message_end",
		"message": map[string]any{"role": "assistant", "stopReason": "stop"},
	}
	if err := r.handleRPCEvent(task.TaskRequest{}, io.Discard, messageEnd, state); err != nil {
		t.Fatal(err)
	}
	if !state.completeOnEOF() {
		t.Fatal("final assistant response should permit clean EOF completion")
	}
	_ = r.handleRPCEvent(task.TaskRequest{}, io.Discard, map[string]any{"type": "tool_execution_start", "toolCallId": "tool-1"}, state)
	if state.completeOnEOF() {
		t.Fatal("active tool must prevent EOF completion")
	}
	_ = r.handleRPCEvent(task.TaskRequest{}, io.Discard, map[string]any{"type": "tool_execution_end", "toolCallId": "tool-1"}, state)
	if !state.completeOnEOF() {
		t.Fatal("completed tool should restore EOF completion eligibility")
	}
	_ = r.handleRPCEvent(task.TaskRequest{}, io.Discard, map[string]any{
		"type":    "message_end",
		"message": map[string]any{"role": "assistant", "stopReason": "toolUse"},
	}, state)
	if state.completeOnEOF() {
		t.Fatal("assistant tool-use response must not be treated as final")
	}
}

func TestPiRPCCancellationStatus(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	status, message := cancellationStatus(cancelled, "pi")
	if status != "cancelled" || message != "pi cancelled" {
		t.Fatalf("cancellation = %q %q", status, message)
	}
	timedOut, timeoutCancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer timeoutCancel()
	<-timedOut.Done()
	status, message = cancellationStatus(timedOut, "pi")
	if status != "error" || message != "pi timed out" {
		t.Fatalf("timeout = %q %q", status, message)
	}
}

func TestPiRPCCleanupExportsBeforeAbort(t *testing.T) {
	wsDir := t.TempDir()
	lines := make(chan rpcLine, 2)
	lines <- rpcLine{data: []byte(`{"id":"messages","type":"response","success":true,"data":{"messages":[{"role":"assistant","content":"finished"}]}}`)}
	lines <- rpcLine{data: []byte(`{"id":"abort","type":"response","success":true}`)}
	close(lines)
	var commands bytes.Buffer
	r := &Runner{}
	state := &rpcAgentState{lastPublished: time.Now(), activeTools: make(map[string]struct{})}
	export := r.cleanupRPCSession(task.TaskRequest{}, wsDir, &commands, lines, state)
	if !strings.Contains(export, "finished") {
		t.Fatalf("export = %q", export)
	}
	written := commands.String()
	if strings.Index(written, `"id":"messages"`) >= strings.Index(written, `"id":"abort"`) {
		t.Fatalf("commands out of order: %s", written)
	}
	if _, err := os.Stat(filepath.Join(wsDir, ".pi", "session-export.md")); err != nil {
		t.Fatalf("session export missing: %v", err)
	}
}

// fakeServeHarness is a minimal harness.ServeHarness that only implements the
// cleanup methods exercised by shutdownDockerAgentSession. Embedding the
// interface keeps the fake small; unimplemented methods panic if invoked, which
// they are not in these tests.
type fakeServeHarness struct {
	harness.ServeHarness
	mu           sync.Mutex
	abortCtx     context.Context
	abortCtxErr  error
	abortCalls   int
	abortErr     error
	abortBlock   chan struct{}
	exportCalls  int
	exportResult string
	exportErr    error
	order        []string
}

func (f *fakeServeHarness) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	f.mu.Lock()
	f.abortCalls++
	f.abortCtx = ctx
	f.abortCtxErr = ctx.Err()
	block := f.abortBlock
	f.order = append(f.order, "abort")
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.abortErr
}

func (f *fakeServeHarness) ReadSessionExport(wsDir, sessionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exportCalls++
	f.order = append(f.order, "export")
	return f.exportResult, f.exportErr
}

func (f *fakeServeHarness) recordStop() {
	f.mu.Lock()
	f.order = append(f.order, "stop")
	f.mu.Unlock()
}

func (f *fakeServeHarness) abortCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.abortCalls
}

func (f *fakeServeHarness) exportCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exportCalls
}

func (f *fakeServeHarness) recordedOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

// mockEventClient records task event summaries reported via ReportTaskEvents
// so tests can assert diagnostic events are published during cleanup.
type recordedTaskEvent struct {
	taskID         string
	executionID    string
	agentSessionID string
	userPromptID   string
}

type mockEventClient struct {
	runnerRPCClient
	mu        sync.Mutex
	events    []string
	lastEvent recordedTaskEvent
}

func (m *mockEventClient) ReportTaskEvents(_ context.Context, req *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error) {
	m.mu.Lock()
	if req.Msg != nil {
		for _, e := range req.Msg.Events {
			m.events = append(m.events, e.Summary)
			m.lastEvent = recordedTaskEvent{
				taskID:         e.TaskId,
				executionID:    e.ExecutionId,
				agentSessionID: e.AgentSessionId,
				userPromptID:   e.UserPromptId,
			}
		}
	}
	m.mu.Unlock()
	return connect.NewResponse(&runnerv1.ReportTaskEventsResponse{}), nil
}

func (m *mockEventClient) recordedEvents() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...)
}

func (m *mockEventClient) recordedTaskEvent() recordedTaskEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastEvent
}

func newShutdownTestRunner() (*Runner, *mockEventClient) {
	mb := &mockEventClient{}
	return &Runner{rpcClient: mb}, mb
}

func containsEvent(events []string, substr string) bool {
	for _, e := range events {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func TestPublishEventIncludesExecutionHierarchy(t *testing.T) {
	r, mb := newShutdownTestRunner()
	req := task.TaskRequest{
		TaskID:         "task_1",
		ExecutionID:    "exec_1",
		AgentSessionID: "session_1",
		UserPromptID:   "prompt_1",
	}

	r.publishEvent(req, "diagnostic")
	event := mb.recordedTaskEvent()
	if event.taskID != req.TaskID || event.executionID != req.ExecutionID || event.agentSessionID != req.AgentSessionID || event.userPromptID != req.UserPromptID {
		t.Fatalf("event hierarchy = task=%q execution=%q session=%q prompt=%q, want task=%q execution=%q session=%q prompt=%q", event.taskID, event.executionID, event.agentSessionID, event.userPromptID, req.TaskID, req.ExecutionID, req.AgentSessionID, req.UserPromptID)
	}
}

// TestShutdownDockerAgentSessionUsesBoundedNonCancelledContext verifies the
// graceful abort runs against a fresh, bounded cleanup context (derived from
// context.Background) rather than the already-cancelled task context. This is
// the core fix for issue #46: previously the cancelled task context was
// propagated to AbortSession, so the abort never reached the harness.
func TestShutdownDockerAgentSessionUsesBoundedNonCancelledContext(t *testing.T) {
	r, _ := newShutdownTestRunner()
	h := &fakeServeHarness{}
	stopped := false
	r.shutdownDockerAgentSession(task.TaskRequest{TaskID: "task_1"}, "http://127.0.0.1:1", "sess_1", "secret", t.TempDir(), h, func() { stopped = true })

	if h.abortCallCount() != 1 {
		t.Fatalf("abort called %d times, want 1", h.abortCallCount())
	}
	if h.abortCtxErr != nil {
		t.Fatalf("abort context was already cancelled when AbortSession ran: %v", h.abortCtxErr)
	}
	if _, ok := h.abortCtx.Deadline(); !ok {
		t.Fatal("abort context is not bounded by a deadline")
	}
	if !stopped {
		t.Fatal("container was not stopped after abort")
	}
}

// TestShutdownDockerAgentSessionAbortsBeforeStopBeforeExport verifies the
// cleanup sequence is graceful abort, then stop the container, then read the
// session export from the workspace. This ordering gives the agent a chance to
// flush in-flight state before the container is killed. See issue #46.
func TestShutdownDockerAgentSessionAbortsBeforeStopBeforeExport(t *testing.T) {
	r, _ := newShutdownTestRunner()
	h := &fakeServeHarness{exportResult: "export"}
	r.shutdownDockerAgentSession(task.TaskRequest{TaskID: "task_1"}, "http://x", "sess_1", "secret", t.TempDir(), h, func() { h.recordStop() })

	if got := h.recordedOrder(); !reflect.DeepEqual(got, []string{"abort", "stop", "export"}) {
		t.Fatalf("cleanup order = %v, want [abort stop export]", got)
	}
}

// TestShutdownDockerAgentSessionPreservesExport verifies the session export
// collected from the workspace is returned to the caller so it can be attached
// to the terminal task report, even when the prompt failed. See issue #46.
func TestShutdownDockerAgentSessionPreservesExport(t *testing.T) {
	r, _ := newShutdownTestRunner()
	h := &fakeServeHarness{exportResult: "transcript"}
	export := r.shutdownDockerAgentSession(task.TaskRequest{TaskID: "task_1"}, "http://x", "sess_1", "secret", t.TempDir(), h, func() {})
	if export != "transcript" {
		t.Fatalf("export = %q, want transcript", export)
	}
}

// TestShutdownDockerAgentSessionRecordsAbortFailureEvent verifies that when
// the graceful abort fails, a diagnostic event is published but cleanup still
// completes and the export is still collected. Abort/export failures must
// not change the terminal task status (the caller owns that), only surface in
// the event stream. See issue #46.
func TestShutdownDockerAgentSessionRecordsAbortFailureEvent(t *testing.T) {
	r, mb := newShutdownTestRunner()
	h := &fakeServeHarness{abortErr: fmt.Errorf("harness unreachable"), exportResult: "partial"}
	r.shutdownDockerAgentSession(task.TaskRequest{TaskID: "task_1"}, "http://x", "sess_1", "secret", t.TempDir(), h, func() {})

	if !containsEvent(mb.recordedEvents(), "session abort failed") {
		t.Fatalf("abort failure not recorded as event: %v", mb.recordedEvents())
	}
	if h.exportCallCount() != 1 {
		t.Fatal("export should still be collected after abort failure")
	}
}

// TestShutdownDockerAgentSessionRecordsExportFailureEvent verifies that when
// reading the session export fails, a diagnostic event is published. See
// issue #46.
func TestShutdownDockerAgentSessionRecordsExportFailureEvent(t *testing.T) {
	r, mb := newShutdownTestRunner()
	h := &fakeServeHarness{exportErr: fmt.Errorf("opencode db locked")}
	r.shutdownDockerAgentSession(task.TaskRequest{TaskID: "task_1"}, "http://x", "sess_1", "secret", t.TempDir(), h, func() {})

	if !containsEvent(mb.recordedEvents(), "opencode db locked") {
		t.Fatalf("export failure not recorded as event: %v", mb.recordedEvents())
	}
}

// TestShutdownDockerAgentSessionBoundedWhenHarnessUnresponsive verifies that
// cleanup stays bounded when the harness never responds to the abort: the
// abort deadline expires, the failure is recorded, and cleanup (stop + export)
// still completes so cancellation cannot hang indefinitely. See issue #46.
func TestShutdownDockerAgentSessionBoundedWhenHarnessUnresponsive(t *testing.T) {
	defer func(d time.Duration) { dockerAbortTimeout = d }(dockerAbortTimeout)
	dockerAbortTimeout = 50 * time.Millisecond

	r, mb := newShutdownTestRunner()
	h := &fakeServeHarness{abortBlock: make(chan struct{}), exportResult: "stale"}
	stopped := false
	start := time.Now()
	export := r.shutdownDockerAgentSession(task.TaskRequest{TaskID: "task_1"}, "http://x", "sess_1", "secret", t.TempDir(), h, func() { stopped = true })
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("cleanup hung for %v waiting on an unresponsive harness", elapsed)
	}
	if !containsEvent(mb.recordedEvents(), "session abort failed") {
		t.Fatalf("unresponsive abort failure not recorded: %v", mb.recordedEvents())
	}
	if !stopped {
		t.Fatal("container not stopped after unresponsive abort")
	}
	if export != "stale" {
		t.Fatalf("export = %q, want stale", export)
	}
}

// TestShutdownDockerAgentSessionEmptySIDStopsWithoutAbort verifies that when
// there is no harness session (e.g. session creation failed), the container is
// still stopped but no abort or export is attempted. See issue #46.
func TestShutdownDockerAgentSessionEmptySIDStopsWithoutAbort(t *testing.T) {
	r, _ := newShutdownTestRunner()
	h := &fakeServeHarness{}
	stopped := false
	export := r.shutdownDockerAgentSession(task.TaskRequest{TaskID: "task_1"}, "http://x", "", "secret", t.TempDir(), h, func() { stopped = true })

	if export != "" {
		t.Fatalf("export = %q, want empty", export)
	}
	if h.abortCallCount() != 0 {
		t.Fatalf("abort called %d times, want 0", h.abortCallCount())
	}
	if h.exportCallCount() != 0 {
		t.Fatalf("export called %d times, want 0", h.exportCallCount())
	}
	if !stopped {
		t.Fatal("container should still be stopped without a session")
	}
}

func TestSelectHarnessByName_Claude(t *testing.T) {
	h := selectHarnessByName("claude-code")
	if h.Name() != "claude" {
		t.Fatalf("expected claude harness, got %s", h.Name())
	}
	if _, ok := h.(*claude.ClaudeCode); !ok {
		t.Fatalf("expected *claude.ClaudeCode, got %T", h)
	}
	if _, ok := h.(harness.RPCHarness); ok {
		t.Fatal("claude-code should not support RPC")
	}
}

func TestSelectHarnessByName_Codex(t *testing.T) {
	h := selectHarnessByName("codex")
	if h.Name() != "codex" {
		t.Fatalf("expected codex harness, got %s", h.Name())
	}
	if _, ok := h.(*codex.Codex); !ok {
		t.Fatalf("expected *codex.Codex, got %T", h)
	}
	if _, ok := h.(harness.RPCHarness); ok {
		t.Fatal("codex should not support RPC")
	}
}

func TestSelectHarnessByName_OpenCode(t *testing.T) {
	h := selectHarnessByName("opencode")
	if h.Name() != "opencode" {
		t.Fatalf("expected opencode harness, got %s", h.Name())
	}
	if _, ok := h.(*opencode.OpenCode); !ok {
		t.Fatalf("expected *opencode.OpenCode, got %T", h)
	}
	if _, ok := h.(harness.RPCHarness); ok {
		t.Fatal("opencode should not support RPC")
	}
}

func TestSelectHarnessByName_Default(t *testing.T) {
	h := selectHarnessByName("")
	if _, ok := h.(*opencode.OpenCode); !ok {
		t.Fatalf("empty name should default to opencode, got %T", h)
	}

	h = selectHarnessByName("unknown")
	if _, ok := h.(*opencode.OpenCode); !ok {
		t.Fatalf("unknown name should default to opencode, got %T", h)
	}
}

func TestHarnessFor_UsesDefault(t *testing.T) {
	r := &Runner{defaultHarness: "pi"}
	h := r.harnessFor("")
	if h.Name() != "pi" {
		t.Fatalf("empty request should use default 'pi', got %s", h.Name())
	}
}

func TestHarnessFor_OverridesDefault(t *testing.T) {
	r := &Runner{defaultHarness: "pi"}
	h := r.harnessFor("claude-code")
	if h.Name() != "claude" {
		t.Fatalf("explicit 'claude-code' should override default 'pi', got %s", h.Name())
	}
}

func TestHarnessFor_EmptyDefault(t *testing.T) {
	r := &Runner{defaultHarness: ""}
	h := r.harnessFor("")
	if h.Name() != "opencode" {
		t.Fatalf("empty default and empty request should use opencode, got %s", h.Name())
	}
}

func TestProtoTaskToRequest_MapsHarness(t *testing.T) {
	req := protoTaskToRequest(&runnerv1.Task{
		TaskId:         "task-1",
		AgentSessionId: "sess-1",
		UserPromptId:   "prompt-1",
		Prompt:         "test",
		Harness:        "pi",
	})
	if req.Harness != "pi" {
		t.Fatalf("expected harness='pi', got %q", req.Harness)
	}
	if req.AgentSessionID != "sess-1" || req.UserPromptID != "prompt-1" {
		t.Fatalf("hierarchy IDs = %q/%q", req.AgentSessionID, req.UserPromptID)
	}
}

func TestProtoTaskToRequest_EmptyHarness(t *testing.T) {
	req := protoTaskToRequest(&runnerv1.Task{
		TaskId: "task-2",
		Prompt: "test",
	})
	if req.Harness != "" {
		t.Fatalf("expected empty harness, got %q", req.Harness)
	}
}

func TestProtoTaskToRequest_MapsMcpEndpoints(t *testing.T) {
	req := protoTaskToRequest(&runnerv1.Task{McpEndpoints: []*runnerv1.MCPEndpoint{{
		Name: "context", Url: "https://mcp.example.com/mcp", BearerTokenEnv: "CONTEXT_MCP_TOKEN",
	}}})
	if len(req.McpEndpoints) != 1 || req.McpEndpoints[0].BearerTokenEnv != "CONTEXT_MCP_TOKEN" {
		t.Fatalf("unexpected MCP endpoints: %#v", req.McpEndpoints)
	}
}

func TestProtoTaskToRequestMapsGitHubRepo(t *testing.T) {
	req := protoTaskToRequest(&runnerv1.Task{GithubRepo: "Acme/Repo"})
	if req.GitHubRepo != "Acme/Repo" {
		t.Fatalf("GitHubRepo = %q", req.GitHubRepo)
	}
}

func TestValidateEndpointTokenEnvironment(t *testing.T) {
	endpoints := []task.MCPEndpoint{{BearerTokenEnv: "CONTEXT_MCP_TOKEN"}}
	t.Setenv("CONTEXT_MCP_TOKEN", "")
	if err := agentenv.ValidateEndpointTokenEnvironment(endpoints); err == nil {
		t.Fatal("expected missing endpoint token environment to fail")
	}
	t.Setenv("CONTEXT_MCP_TOKEN", "runner-secret")
	if err := agentenv.ValidateEndpointTokenEnvironment(endpoints); err != nil {
		t.Fatalf("expected configured endpoint token environment: %v", err)
	}
	if err := agentenv.ValidateEndpointTokenEnvironment([]task.MCPEndpoint{{BearerTokenEnv: "DEEPSEEK_MCP_CONFIG"}}); err == nil {
		t.Fatal("expected harness control environment to be rejected")
	}
}

func TestProtoTaskToRequestProviderTransport(t *testing.T) {
	req := protoTaskToRequest(&runnerv1.Task{
		ProviderId:         "litellm",
		ModelId:            "coding-model",
		ProviderBaseUrl:    "https://litellm.example.test/v1",
		ProviderApiKeyEnv:  "LITELLM_API_KEY",
		ProviderApi:        "openai-completions",
		ProviderAuthHeader: true,
	})
	if req.ProviderID != "litellm" || req.ModelID != "coding-model" || req.ProviderBaseURL != "https://litellm.example.test/v1" || req.ProviderAPIKeyEnv != "LITELLM_API_KEY" || req.ProviderAPI != "openai-completions" || !req.ProviderAuthHeader {
		t.Fatalf("unexpected resolved provider request: %+v", req)
	}
}

func testDockerRPCArgs(t *testing.T, req task.TaskRequest, runnerID, wsDir, containerName string, h harness.RPCHarness, command []string, gvisor bool, netName, runnerIP string, exec config.ExecutionConfig) []string {
	t.Helper()
	args, err := dockerRPCArgs(req, runnerID, wsDir, filepath.Dir(wsDir), containerName, h, command, gvisor, netName, runnerIP, exec)
	if err != nil {
		t.Fatalf("dockerRPCArgs: %v", err)
	}
	return args
}

func testDockerServeArgs(t *testing.T, r *Runner, req task.TaskRequest, workspaceDir, containerName string, h harness.ServeHarness, serveCmd []string, bindAddr string, hostPort int, gvisor bool, netName, runnerIP, secret string) []string {
	t.Helper()
	if r.cfg.Runner.WorkspaceRoot == "" {
		r.cfg.Runner.WorkspaceRoot = filepath.Dir(workspaceDir)
	}
	args, err := r.dockerServeArgs(req, workspaceDir, containerName, h, serveCmd, bindAddr, hostPort, gvisor, netName, runnerIP, secret)
	if err != nil {
		t.Fatalf("dockerServeArgs: %v", err)
	}
	return args
}

func TestDockerArgsRejectWorkspaceOutsideRoot(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest"}
	if _, err := dockerRPCArgs(req, "runner-test", "/tmp/outside", "/tmp/workspaces", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", config.ExecutionConfig{}); err == nil {
		t.Fatal("dockerRPCArgs accepted workspace outside root")
	}
	r := &Runner{cfg: &config.Config{Runner: config.RunnerConfig{WorkspaceRoot: "/tmp/workspaces"}}, runnerID: "runner-test"}
	serveHarness := opencode.New()
	if _, err := r.dockerServeArgs(req, "/tmp/outside", "chetter-task-task-123", serveHarness, serveHarness.ServeCommand(containerPortForServe), "", containerPortForServe, false, "", "", ""); err == nil {
		t.Fatal("dockerServeArgs accepted workspace outside root")
	}
}

func TestDockerRPCArgsRunsHarnessInsideAgentImage(t *testing.T) {
	t.Setenv("LITELLM_API_KEY", "runner-litellm-key")
	h := pi.New()
	req := task.TaskRequest{
		TaskID:            "task-123",
		AgentImage:        "ghcr.io/flatout-works/chetter-runner:main",
		Agent:             "issue-creator",
		ProviderID:        "synthetic",
		ProviderAPIKeyEnv: "LITELLM_API_KEY",
		ModelID:           "pi-model",
		Env: map[string]string{
			"CUSTOM_ENV":      "custom-value",
			"OPENAI_API_KEY":  "task-key",
			"LITELLM_API_KEY": "task-key",
		},
	}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", config.ExecutionConfig{})

	entrypointIdx := indexOf(args, "--entrypoint")
	if entrypointIdx == -1 || entrypointIdx == len(args)-1 {
		t.Fatalf("expected docker entrypoint in args: %v", args)
	}
	if got := args[entrypointIdx+1]; got != "pi" {
		t.Fatalf("expected docker entrypoint pi, got %q", got)
	}
	imageIdx := indexOf(args, req.AgentImage)
	if imageIdx == -1 {
		t.Fatalf("agent image %q not found in args: %v", req.AgentImage, args)
	}
	if imageIdx == len(args)-1 || args[imageIdx+1] != "--mode" {
		t.Fatalf("expected pi RPC args after image, got %v", args[imageIdx:])
	}
	if hasAdjacentArgs(args, "-v", "/tmp/chetter.sock:"+"/workspace/.chetter.sock") {
		t.Fatal("socket mount removed; should not have .chetter.sock mount")
	}
	if hasAdjacentArgs(args, "-e", "MCP_SOCKET_PATH="+"/workspace/.chetter.sock") {
		t.Fatal("MCP_SOCKET_PATH removed; should not have socket env")
	}
	if !hasAdjacentArgs(args, "-e", "WORKSPACE="+containerWorkspaceDir) {
		t.Fatalf("expected WORKSPACE to use container workspace, got %v", args)
	}
	if !hasAdjacentArgs(args, "-e", "CUSTOM_ENV=custom-value") {
		t.Fatalf("expected custom env to be forwarded, got %v", args)
	}
	if hasAdjacentArgs(args, "-e", "OPENAI_API_KEY=task-key") {
		t.Fatalf("runner-owned env must not use task-provided value, got %v", args)
	}
	if hasAdjacentArgs(args, "-e", "LITELLM_API_KEY=task-key") {
		t.Fatalf("catalog-selected credential must not use task-provided value, got %v", args)
	}
	if !hasAdjacentArgs(args, "-e", "LITELLM_API_KEY=runner-litellm-key") {
		t.Fatalf("expected runner provider credential, got %v", args)
	}
}

func TestDockerRPCArgsConfiguresRunnerDNSForGVisor(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest"}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), true, "chetter_default", "172.21.0.1", config.ExecutionConfig{})

	if !hasAdjacentArgs(args, "--dns", "172.21.0.1") {
		t.Fatalf("expected runner DNS in args: %v", args)
	}
	if !hasAdjacentArgs(args, "-e", "NO_PROXY=localhost,127.0.0.1,0.0.0.0,.local") {
		t.Fatalf("expected local-only no_proxy entry so MCP uses the proxy: %v", args)
	}
	if !hasAdjacentArgs(args, "-e", "NODE_USE_ENV_PROXY=1") {
		t.Fatalf("expected Node environment proxy support: %v", args)
	}
}

func TestDockerRPCArgsAppliesContainerSecurityFlags(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest"}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", config.ExecutionConfig{})
	for _, want := range [][]string{{"--cap-drop", "ALL"}, {"--security-opt", "no-new-privileges"}} {
		if !hasAdjacentArgs(args, want[0], want[1]) {
			t.Fatalf("expected %s %s in args: %v", want[0], want[1], args)
		}
	}
	for _, a := range args {
		if a == "--privileged" {
			t.Fatalf("task container must not be privileged: %v", args)
		}
	}
}

func TestDockerRPCArgsAppliesContainerLimits(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest"}
	exec := config.ExecutionConfig{ContainerMemory: "512m", ContainerCPU: 1.5, ContainerPIDs: 200}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", exec)
	if !hasAdjacentArgs(args, "--memory", "512m") {
		t.Fatalf("expected --memory 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--memory-swap", "512m") {
		t.Fatalf("expected --memory-swap 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--cpus", "1.5") {
		t.Fatalf("expected --cpus 1.5 in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--pids-limit", "200") {
		t.Fatalf("expected --pids-limit 200 in args: %v", args)
	}
}

// TestDockerRPCArgsRunnerLimitsCapTask verifies that runner-level limits remain
// hard safety caps when a task requests more resources.
func TestDockerRPCArgsRunnerLimitsCapTask(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest", MaxMemoryMB: 2048, MaxCPU: 3}
	exec := config.ExecutionConfig{ContainerMemory: "512m", ContainerCPU: 1.5, ContainerPIDs: 200}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", exec)
	if !hasAdjacentArgs(args, "--memory", "512m") {
		t.Fatalf("expected runner cap --memory 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--memory-swap", "512m") {
		t.Fatalf("expected runner cap --memory-swap 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--cpus", "1.5") {
		t.Fatalf("expected runner cap --cpus 1.5 in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--pids-limit", "200") {
		t.Fatalf("expected config --pids-limit 200 fallback in args: %v", args)
	}
}

func TestDockerRPCArgsTaskLimitsCanTightenRunnerCaps(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest", MaxMemoryMB: 768, MaxCPU: 2}
	exec := config.ExecutionConfig{ContainerMemory: "4g", ContainerCPU: 4}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", exec)
	if !hasAdjacentArgs(args, "--memory", "768m") {
		t.Fatalf("expected stricter task --memory 768m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--cpus", "2") {
		t.Fatalf("expected stricter task --cpus 2 in args: %v", args)
	}
}

// TestDockerRPCArgsPerTaskLimitsWhenConfigUnset verifies per-task limits are
// applied even when no runner-level config limits are configured.
func TestDockerRPCArgsPerTaskLimitsWhenConfigUnset(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest", MaxMemoryMB: 768, MaxCPU: 2}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", config.ExecutionConfig{})
	if !hasAdjacentArgs(args, "--memory", "768m") {
		t.Fatalf("expected --memory 768m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--cpus", "2") {
		t.Fatalf("expected --cpus 2 in args: %v", args)
	}
	if indexOf(args, "--pids-limit") != -1 {
		t.Fatalf("expected no --pids-limit when unset, got: %v", args)
	}
}

func TestDockerRPCArgsOmitsContainerLimitsWhenUnset(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest"}
	args := testDockerRPCArgs(t, req, "runner-test", "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", config.ExecutionConfig{})
	for _, flag := range []string{"--memory", "--memory-swap", "--cpus", "--pids-limit"} {
		if indexOf(args, flag) != -1 {
			t.Fatalf("expected no %q flag when limits unset, got: %v", flag, args)
		}
	}
}

func TestDockerServeArgsAppliesContainerLimits(t *testing.T) {
	r := &Runner{cfg: &config.Config{Execution: config.ExecutionConfig{ContainerMemory: "512m", ContainerCPU: 2, ContainerPIDs: 256}}, runnerID: "runner-test"}
	h := opencode.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest", Agent: "issue-creator"}
	serveCmd := h.ServeCommand(containerPortForServe)
	args := testDockerServeArgs(t, r, req, "/tmp/ws", "chetter-task-task-123", h, serveCmd, "", containerPortForServe, false, "", "", "")
	if !hasAdjacentArgs(args, "--memory", "512m") {
		t.Fatalf("expected --memory 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--memory-swap", "512m") {
		t.Fatalf("expected --memory-swap 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--cpus", "2") {
		t.Fatalf("expected --cpus 2 in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--pids-limit", "256") {
		t.Fatalf("expected --pids-limit 256 in args: %v", args)
	}
}

// TestDockerServeArgsRunnerLimitsCapTask verifies that runner-level limits cap
// larger task values on the serve path used by both new and resumed tasks.
func TestDockerServeArgsRunnerLimitsCapTask(t *testing.T) {
	r := &Runner{cfg: &config.Config{Execution: config.ExecutionConfig{ContainerMemory: "512m", ContainerCPU: 2, ContainerPIDs: 256}}, runnerID: "runner-test"}
	h := opencode.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest", Agent: "issue-creator", MaxMemoryMB: 4096, MaxCPU: 4}
	serveCmd := h.ServeCommand(containerPortForServe)
	args := testDockerServeArgs(t, r, req, "/tmp/ws", "chetter-task-task-123", h, serveCmd, "", containerPortForServe, false, "", "", "")
	if !hasAdjacentArgs(args, "--memory", "512m") {
		t.Fatalf("expected runner cap --memory 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--memory-swap", "512m") {
		t.Fatalf("expected runner cap --memory-swap 512m in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--cpus", "2") {
		t.Fatalf("expected runner cap --cpus 2 in args: %v", args)
	}
	if !hasAdjacentArgs(args, "--pids-limit", "256") {
		t.Fatalf("expected config --pids-limit 256 fallback in args: %v", args)
	}
}

func TestDockerServeArgsOmitsContainerLimitsWhenUnset(t *testing.T) {
	r := &Runner{cfg: &config.Config{}, runnerID: "runner-test"}
	h := opencode.New()
	req := task.TaskRequest{TaskID: "task-123", AgentImage: "chetter-agent:latest", Agent: "issue-creator"}
	serveCmd := h.ServeCommand(containerPortForServe)
	args := testDockerServeArgs(t, r, req, "/tmp/ws", "chetter-task-task-123", h, serveCmd, "", containerPortForServe, false, "", "", "")
	for _, flag := range []string{"--memory", "--memory-swap", "--cpus", "--pids-limit"} {
		if indexOf(args, flag) != -1 {
			t.Fatalf("expected no %q flag when limits unset, got: %v", flag, args)
		}
	}
}

func TestDockerEnvironmentUsesContainerAskpassAndCredentialBridge(t *testing.T) {
	r := &Runner{cfg: &config.Config{}, runnerID: "runner-test"}
	h := opencode.New()
	req := task.TaskRequest{
		TaskID: "task-123", AgentImage: "chetter-agent:latest", Agent: "reviewer",
		GitHubCredentialURL:   "http://172.20.0.2/internal/github-credential",
		GitHubCredentialToken: "execution-capability",
		Env: map[string]string{
			agentenv.GitHubCredentialURLEnv:   "http://attacker",
			agentenv.GitHubCredentialTokenEnv: "attacker-capability",
		},
	}
	args := testDockerServeArgs(t, r, req, "/host/workspace", "container", h, h.ServeCommand(containerPortForServe), "", containerPortForServe, false, "bridge", "", "")
	for _, want := range []string{
		"GIT_ASKPASS=/workspace/.chetter-git-askpass",
		agentenv.GitHubCredentialURLEnv + "=http://172.20.0.2/internal/github-credential",
		agentenv.GitHubCredentialTokenEnv + "=execution-capability",
	} {
		if !hasAdjacentArgs(args, "-e", want) {
			t.Fatalf("missing managed environment %q in %v", want, args)
		}
	}
	if hasAdjacentArgs(args, "-e", agentenv.GitHubCredentialTokenEnv+"=attacker-capability") {
		t.Fatal("task overrode credential capability")
	}
}

func TestDockerRPCEnvironmentIncludesCredentialBridge(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{
		TaskID: "task-123", AgentImage: "chetter-agent:latest",
		GitHubCredentialURL:   "http://172.20.0.2/internal/github-credential",
		GitHubCredentialToken: "execution-capability",
	}
	args := testDockerRPCArgs(t, req, "runner-test", "/host/workspace", "container", h, h.RpcCommand(req), false, "bridge", "172.20.0.2", config.ExecutionConfig{})
	for _, want := range []string{
		agentenv.GitHubCredentialURLEnv + "=http://172.20.0.2/internal/github-credential",
		agentenv.GitHubCredentialTokenEnv + "=execution-capability",
		"GIT_ASKPASS=/workspace/.chetter-git-askpass",
	} {
		if !hasAdjacentArgs(args, "-e", want) {
			t.Fatalf("missing RPC environment %q in %v", want, args)
		}
	}
}

func TestLocalAgentEnvironmentProtectsCredentialBridgeAndRunnerBearer(t *testing.T) {
	t.Setenv("CHETTER_RUNNER_AUTH_TOKEN", "runner-rpc-secret")
	r := &Runner{}
	req := task.TaskRequest{
		GitHubCredentialURL:   "http://127.0.0.1/internal/github-credential",
		GitHubCredentialToken: "execution-capability",
		Env: map[string]string{
			agentenv.GitHubCredentialTokenEnv: "attacker-capability",
			"CHETTER_RUNNER_AUTH_TOKEN":       "attacker-runner-token",
		},
	}
	env := strings.Join(r.agentEnv(req, "/workspace", "", opencode.New()), "\n")
	if !strings.Contains(env, agentenv.GitHubCredentialTokenEnv+"=execution-capability") {
		t.Fatal("runner credential capability missing")
	}
	if strings.Contains(env, "attacker-capability") || strings.Contains(env, "runner-rpc-secret") || strings.Contains(env, "attacker-runner-token") {
		t.Fatal("task or runner RPC credential leaked into local agent environment")
	}
}

func TestCloneCredentialSelectionAndURLSafety(t *testing.T) {
	base := task.TaskRequest{GitURL: "https://github.com/Acme/Repo.git", GitHubRepo: "acme/repo", GitRef: "main"}
	calls := 0
	get := func(context.Context, task.TaskRequest) (string, error) {
		calls++
		return "broker-token", nil
	}
	token, err := selectCloneCredential(context.Background(), base, "static-token", get)
	if err != nil || token != "broker-token" || calls != 1 {
		t.Fatalf("base credential = %q, calls=%d, err=%v", token, calls, err)
	}
	args := gitCloneArgs(base)
	if strings.Join(args, " ") != "clone -b main https://github.com/Acme/Repo.git ." || strings.Contains(strings.Join(args, " "), token) {
		t.Fatalf("unsafe clone args: %v", args)
	}

	fork := base
	fork.GitURL = "https://github.com/Contributor/Fork.git"
	token, err = selectCloneCredential(context.Background(), fork, "static-token", get)
	if err != nil || token != "static-token" || calls != 1 {
		t.Fatalf("fork credential = %q, calls=%d, err=%v", token, calls, err)
	}
}

type fakeGitHubCredentialClient struct {
	request *runnerv1.GetGitHubCredentialRequest
}

func (f *fakeGitHubCredentialClient) GetGitHubCredential(_ context.Context, req *connect.Request[runnerv1.GetGitHubCredentialRequest]) (*connect.Response[runnerv1.GetGitHubCredentialResponse], error) {
	f.request = req.Msg
	return connect.NewResponse(&runnerv1.GetGitHubCredentialResponse{
		Username: "x-access-token", Token: "broker-token", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	}), nil
}

func TestRequestGitHubCredentialAddsRunnerOwnedIdentity(t *testing.T) {
	client := &fakeGitHubCredentialClient{}
	token, err := requestGitHubCredential(context.Background(), client, "runner-owner", &runnerv1.GetGitHubCredentialRequest{
		TaskId: "task-1", ExecutionId: "exec-1", ClaimId: "claim-1", Repo: "Acme/Repo",
	})
	if err != nil || token != "broker-token" {
		t.Fatalf("token = %q, err=%v", token, err)
	}
	if client.request.RunnerId != "runner-owner" || client.request.TaskId != "task-1" || client.request.ExecutionId != "exec-1" || client.request.ClaimId != "claim-1" || client.request.Repo != "Acme/Repo" {
		t.Fatalf("request = %+v", client.request)
	}
}

func TestGitHubCredentialBridgeURLUsesExecutionBackendHost(t *testing.T) {
	server, err := runnermcp.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.SetCredentialHandler(func(context.Context) (string, error) { return "token", nil }); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(server.Addr())

	local := &Runner{cfg: &config.Config{Execution: config.ExecutionConfig{Backend: "local"}}}
	if got, want := runnerGitHubCredentialURL(local, server), "http://127.0.0.1:"+port+runnermcp.GitHubCredentialPath; got != want {
		t.Fatalf("local URL = %q, want %q", got, want)
	}
	t.Setenv("RUNNER_HOST_IP", "172.21.0.3")
	for _, backend := range []string{"docker", "kubernetes"} {
		runner := &Runner{cfg: &config.Config{Execution: config.ExecutionConfig{Backend: backend}}}
		if got, want := runnerGitHubCredentialURL(runner, server), "http://172.21.0.3:"+port+runnermcp.GitHubCredentialPath; got != want {
			t.Fatalf("%s URL = %q, want %q", backend, got, want)
		}
	}
}

func TestGHWrapperBlocksWritesAndHasNoBypass(t *testing.T) {
	script := filepath.Join("..", "..", "scripts", "gh")
	contents, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "CHETTER_ALLOW_GH_WRITES") {
		t.Fatal("gh wrapper retains a task-enableable write bypass")
	}
	for _, args := range [][]string{{"api", "/user"}, {"issue", "create"}, {"pr", "checkout", "1"}} {
		cmd := exec.Command(script, args...)
		if err := cmd.Run(); err == nil {
			t.Fatalf("gh wrapper allowed %v", args)
		} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 64 {
			t.Fatalf("gh wrapper %v exit = %v", args, err)
		}
	}
}

func TestShouldPullAgentImage(t *testing.T) {
	for _, tc := range []struct {
		image string
		want  bool
	}{
		{image: "ghcr.io/flatout-works/chetter-agent:golang", want: true},
		{image: "chetter-agent:golang", want: false},
	} {
		if got := shouldPullAgentImage(tc.image); got != tc.want {
			t.Errorf("shouldPullAgentImage(%q) = %v, want %v", tc.image, got, tc.want)
		}
	}
}

func TestHarnessBaseURLUsesDockerGatewayForGVisor(t *testing.T) {
	t.Setenv("RUNNER_DOCKER_GATEWAY_IP", "172.21.0.1")
	got := harnessBaseURL("127.0.0.1", 34133, true, "chetter_default")
	if got != "http://172.21.0.1:34133" {
		t.Fatalf("expected Docker gateway base URL, got %q", got)
	}
}

func TestHarnessPublishBindAddrUsesAllInterfacesForGVisor(t *testing.T) {
	if got := harnessPublishBindAddr("127.0.0.1", true); got != "0.0.0.0" {
		t.Fatalf("expected gVisor publish bind addr 0.0.0.0, got %q", got)
	}
	if got := harnessPublishBindAddr("127.0.0.1", false); got != "127.0.0.1" {
		t.Fatalf("expected non-gVisor bind addr to be preserved, got %q", got)
	}
}

func TestGVisorNoProxyExcludesChetterMCPHost(t *testing.T) {
	got := gvisorNoProxy()
	if got != "localhost,127.0.0.1,0.0.0.0,.local" {
		t.Fatalf("unexpected no_proxy value: %q", got)
	}
}

func TestOpenCodeConfigContentIsHarnessControlled(t *testing.T) {
	if !agentenv.IsHarnessControlEnv("OPENCODE_CONFIG_CONTENT") {
		t.Fatal("OPENCODE_CONFIG_CONTENT must be protected from task environment overrides")
	}
}

func TestWorkspaceMCPServerRegistersAndRevokesRelayClaim(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	relay, err := network.NewMCPRelay("127.0.0.1:0", target.URL+"/mcp", "upstream-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Stop() })

	runner := &Runner{
		cfg:      &config.Config{Execution: config.ExecutionConfig{Backend: "local"}},
		mcpRelay: relay,
	}
	server, err := runner.startWorkspaceMCP(task.TaskRequest{TaskID: "task-1", ExecutionID: "exec-1", ClaimID: "claim-1"})
	if err != nil {
		t.Fatalf("startWorkspaceMCP: %v", err)
	}

	callRelay := func() int {
		req, err := http.NewRequest(http.MethodPost, "http://"+relay.Addr()+"/mcp", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+server.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if status := callRelay(); status != http.StatusNoContent {
		t.Fatalf("active claim status = %d, want %d", status, http.StatusNoContent)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("close workspace MCP server: %v", err)
	}
	if status := callRelay(); status != http.StatusUnauthorized {
		t.Fatalf("closed claim status = %d, want %d", status, http.StatusUnauthorized)
	}
}

func TestTaskChetterMCPURLUsesRunnerRelay(t *testing.T) {
	t.Setenv("RUNNER_HOST_IP", "172.21.0.3")
	relay, err := network.NewMCPRelay("127.0.0.1:0", "http://chetter-mcp:8080/mcp", "")
	if err != nil {
		t.Fatalf("new relay: %v", err)
	}
	if err := relay.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = relay.Stop() })

	runner := &Runner{
		cfg:      &config.Config{ChetterMCP: config.ChetterMCPConfig{URL: "http://chetter-mcp:8080/mcp"}},
		mcpRelay: relay,
	}
	_, port, err := net.SplitHostPort(relay.Addr())
	if err != nil {
		t.Fatalf("split relay address: %v", err)
	}
	if got, want := runner.taskChetterMCPURL(), "http://172.21.0.3:"+port+"/mcp"; got != want {
		t.Fatalf("task Chetter MCP URL = %q, want %q", got, want)
	}
	if got := runner.taskChetterMCPToken("claim-token"); got != "claim-token" {
		t.Fatalf("task Chetter MCP token = %q, want claim token", got)
	}
}

func TestTaskChetterMCPURLUsesConfiguredURLLocally(t *testing.T) {
	runner := &Runner{cfg: &config.Config{Execution: config.ExecutionConfig{Backend: "local"}, ChetterMCP: config.ChetterMCPConfig{URL: "http://chetter-mcp:8080/mcp", AuthToken: "local-token"}}}
	if got := runner.taskChetterMCPURL(); got != "http://chetter-mcp:8080/mcp" {
		t.Fatalf("task Chetter MCP URL = %q", got)
	}
	if got := runner.taskChetterMCPToken("claim-token"); got != "local-token" {
		t.Fatalf("task Chetter MCP token = %q", got)
	}
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

func hasAdjacentArgs(values []string, key, value string) bool {
	for i := 0; i < len(values)-1; i++ {
		if values[i] == key && values[i+1] == value {
			return true
		}
	}
	return false
}
