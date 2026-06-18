package controller

import (
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

	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/internal/task"
)

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
		got := shellQuoteArg(tc.in)
		if got != tc.want {
			t.Errorf("shellQuoteArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellQuoteArgs(t *testing.T) {
	result := shellQuoteArgs([]string{"opencode", "run", "--pure"})
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
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := opencode.GenerateConfig(wsDir, socketPath, "mcp-bridge", "", "", false, false); err != nil {
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

	wsDir := t.TempDir()
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := opencode.GenerateConfig(wsDir, socketPath, "mcp-bridge", "https://chetter.example.com/mcp", "test-token", false, false); err != nil {
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

	wsDir := t.TempDir()
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := opencode.GenerateConfig(wsDir, socketPath, "/usr/local/bin/mcp-bridge", "", "", true, true); err != nil {
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

	wsDir := t.TempDir()
	socketPath := filepath.Join(wsDir, "socket.sock")

	if err := opencode.GenerateConfig(wsDir, socketPath, "mcp-bridge", "", "", false, false); err != nil {
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
			socketPath := filepath.Join(wsDir, "socket.sock")

			if err := opencode.GenerateConfig(wsDir, socketPath, "mcp-bridge", tt.chetterURL, tt.chetterToken, tt.includeBridge, false); err != nil {
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

	cmd := exec.Command("opencode", h.ServeArgs(port)...)
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
