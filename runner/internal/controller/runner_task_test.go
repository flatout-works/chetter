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

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/harness/claude"
	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/harness/pi"
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

	if providers, ok := parsed["provider"].(map[string]any); !ok || len(providers) != 0 {
		t.Fatalf("expected empty provider map when task has no resolved provider, got %+v", parsed["provider"])
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

func TestSelectHarnessByName_Pi(t *testing.T) {
	h := selectHarnessByName("pi")
	if h.Name() != "pi" {
		t.Fatalf("expected pi harness, got %s", h.Name())
	}
	if _, ok := h.(*pi.Pi); !ok {
		t.Fatalf("expected *pi.Pi, got %T", h)
	}
	if !h.SupportsRpc() {
		t.Fatal("pi should support RPC")
	}
	if h.SupportsServe() {
		t.Fatal("pi should not support serve")
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
	if h.SupportsRpc() {
		t.Fatal("claude-code should not support RPC")
	}
	if h.SupportsServe() {
		t.Fatal("claude-code should not support serve")
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
	if h.SupportsRpc() {
		t.Fatal("opencode should not support RPC")
	}
	if !h.SupportsServe() {
		t.Fatal("opencode should support serve")
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
		TaskId:  "task-1",
		Prompt:  "test",
		Harness: "pi",
	})
	if req.Harness != "pi" {
		t.Fatalf("expected harness='pi', got %q", req.Harness)
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

func TestDockerRPCArgsRunsHarnessInsideAgentImage(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{
		TaskID:     "task-123",
		AgentImage: "ghcr.io/flatout-works/chetter-runner:main",
		Agent:      "issue-creator",
		ProviderID: "synthetic",
		ModelID:    "pi-model",
		Env: map[string]string{
			"CUSTOM_ENV":     "custom-value",
			"OPENAI_API_KEY": "task-key",
		},
	}
	args := dockerRPCArgs(req, "/tmp/ws", "/tmp/chetter.sock", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "", false)

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
	if rmIdx := indexOf(args, "--rm"); rmIdx == -1 {
		t.Fatalf("expected --rm flag when not keeping container, got %v", args)
	}
	if imageIdx == len(args)-1 || args[imageIdx+1] != "--mode" {
		t.Fatalf("expected pi RPC args after image, got %v", args[imageIdx:])
	}
	if !hasAdjacentArgs(args, "-v", "/tmp/chetter.sock:"+containerSocketPath) {
		t.Fatalf("expected socket mounted at %s, got %v", containerSocketPath, args)
	}
	if !hasAdjacentArgs(args, "-e", "MCP_SOCKET_PATH="+containerSocketPath) {
		t.Fatalf("expected MCP_SOCKET_PATH to use container socket, got %v", args)
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

func TestDockerCheckpointParts(t *testing.T) {
	containerID, checkpointName, ok := dockerCheckpointParts("/var/lib/docker/containers/abc123/checkpoints/chetter-checkpoint-task_1")
	if !ok {
		t.Fatal("expected checkpoint path to parse")
	}
	if containerID != "abc123" {
		t.Fatalf("containerID = %q, want abc123", containerID)
	}
	if checkpointName != "chetter-checkpoint-task_1" {
		t.Fatalf("checkpointName = %q", checkpointName)
	}

	if _, _, ok := dockerCheckpointParts("/tmp/not-a-checkpoint"); ok {
		t.Fatal("expected invalid checkpoint path not to parse")
	}
}

func TestParseDockerPortOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{name: "ipv4", in: "0.0.0.0:34219", want: 34219, ok: true},
		{name: "ipv6 and ipv4", in: "0.0.0.0:34219\n[::]:34219", want: 34219, ok: true},
		{name: "empty", in: "", ok: false},
		{name: "bad", in: "not-a-port", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseDockerPortOutput(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("port = %d, want %d", got, tc.want)
			}
		})
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
