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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/harness/claude"
	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/harness/pi"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
	"github.com/flatout-works/chetter/runner/internal/workspace"
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
	if !isRunnerOwnedEnv("ANTHROPIC_AUTH_TOKEN") {
		t.Fatal("ANTHROPIC_AUTH_TOKEN should be runner-owned")
	}
	if !isRunnerOwnedEnv("ANTHROPIC_BASE_URL") {
		t.Fatal("ANTHROPIC_BASE_URL should be runner-owned")
	}
	if !isRunnerOwnedEnv("CLAUDE_CODE_SUBAGENT_MODEL") {
		t.Fatal("CLAUDE_CODE_SUBAGENT_MODEL should be runner-owned")
	}
	if !isRunnerOwnedEnv("GH_TOKEN") {
		t.Fatal("GH_TOKEN should be runner-owned")
	}
	if isRunnerOwnedEnv("LLM_PROVIDER") {
		t.Fatal("LLM_PROVIDER should not be treated as runner-owned env")
	}
}

func TestAddRunnerOwnedEnvUsesRunnerValue(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "runner-github-token")
	t.Setenv("GH_TOKEN", "runner-gh-token")
	t.Setenv("MEM9_API_KEY", "runner-key")
	t.Setenv("OPENAI_API_KEY", "runner-openai-key")
	t.Setenv("DEEPSEEK_API_KEY", "runner-deepseek-key")
	req := task.TaskRequest{Env: map[string]string{injectedGitHubTokenTaskEnv: "ghs_claim_token"}}
	env := map[string]string{"MEM9_API_KEY": "task-key", "OPENAI_API_KEY": "task-openai-key", "DEEPSEEK_API_KEY": "task-deepseek-key"}
	addRunnerOwnedEnv(env, req)
	if env["GITHUB_TOKEN"] != "ghs_claim_token" {
		t.Fatalf("expected injected github token to win, got %q", env["GITHUB_TOKEN"])
	}
	if env["GH_TOKEN"] != "ghs_claim_token" {
		t.Fatalf("expected injected github token to win for GH_TOKEN, got %q", env["GH_TOKEN"])
	}
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

func TestRunnerOwnedEnvDoesNotForwardHostGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "runner-github-token")
	t.Setenv("GH_TOKEN", "runner-gh-token")
	env := map[string]string{}
	addRunnerOwnedEnv(env, task.TaskRequest{Env: map[string]string{}})
	if _, ok := env["GITHUB_TOKEN"]; ok {
		t.Fatalf("host GITHUB_TOKEN should not be forwarded without injected task token: %#v", env)
	}
	if _, ok := env["GH_TOKEN"]; ok {
		t.Fatalf("host GH_TOKEN should not be forwarded without injected task token: %#v", env)
	}
	if got := runnerOwnedEnvValue("GITHUB_TOKEN", task.TaskRequest{Env: map[string]string{injectedGitHubTokenTaskEnv: "ghs_claim_token"}}); got != "ghs_claim_token" {
		t.Fatalf("runnerOwnedEnvValue injected token = %q, want ghs_claim_token", got)
	}
	if got := runnerOwnedEnvValue("GH_TOKEN", task.TaskRequest{Env: map[string]string{injectedGitHubTokenTaskEnv: "ghs_claim_token"}}); got != "ghs_claim_token" {
		t.Fatalf("runnerOwnedEnvValue GH_TOKEN injected token = %q, want ghs_claim_token", got)
	}
}

func TestChetterMCPForRequestRequiresPrivilegedChetterProfile(t *testing.T) {
	r := &Runner{cfg: &config.Config{ChetterMCP: config.ChetterMCPConfig{
		URL:       "https://chetter.example.test/mcp",
		AuthToken: "admin-token",
	}}}

	url, token := r.chetterMCPForRequest(task.TaskRequest{Env: map[string]string{}})
	if url != "" || token != "" {
		t.Fatalf("unprivileged chetter MCP = (%q, %q), want empty", url, token)
	}

	url, token = r.chetterMCPForRequest(task.TaskRequest{
		Env: map[string]string{privilegedMCPProfileEnv: "true"},
		MCPProfiles: []task.MCPProfile{{
			Name: "private-docs",
			URL:  "https://docs.example.test/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:DOCS_TOKEN}",
			},
		}},
	})
	if url != "" || token != "" {
		t.Fatalf("unrelated privileged chetter MCP = (%q, %q), want empty", url, token)
	}

	url, token = r.chetterMCPForRequest(task.TaskRequest{
		Env: map[string]string{privilegedMCPProfileEnv: "true"},
		MCPProfiles: []task.MCPProfile{
			{
				Name: "private-docs",
				URL:  "https://docs.example.test/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer ${env:DOCS_TOKEN}",
				},
			},
			{
				Name: "public-chetter",
				URL:  "https://chetter.example.test/mcp/",
			},
		},
	})
	if url != "" || token != "" {
		t.Fatalf("uncredentialed chetter URL profile unlocked MCP = (%q, %q), want empty", url, token)
	}

	url, token = r.chetterMCPForRequest(task.TaskRequest{
		Env: map[string]string{privilegedMCPProfileEnv: "true"},
		MCPProfiles: []task.MCPProfile{{
			Name: "chetter-orchestration",
			URL:  "https://chetter.example.test/mcp/",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:CHETTER_MCP_AUTH_TOKEN}",
			},
		}},
	})
	if url != "https://chetter.example.test/mcp" || token != "admin-token" {
		t.Fatalf("privileged chetter profile MCP = (%q, %q), want configured values", url, token)
	}
}

func TestFilteredHostEnvRemovesRunnerOwnedCredentials(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("GITHUB_TOKEN", "runner-github-token")
	t.Setenv("GH_TOKEN", "runner-gh-token")
	t.Setenv("OPENAI_API_KEY", "runner-openai-key")
	t.Setenv(injectedGitHubTokenTaskEnv, "ghs_claim_token")
	t.Setenv("CHETTER_MCP_AUTH_TOKEN", "admin-mcp-token")
	t.Setenv("MCP_AUTH_TOKEN", "server-admin-token")
	t.Setenv("CHETTER_RUNNER_RPC_TOKEN", "runner-rpc-token")
	t.Setenv("DATABASE_DSN", "root@tcp(localhost:4000)/")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_B64", "private-key")
	t.Setenv("CUSTOM_ENV", "custom-value")

	env := envListToMap(filteredHostEnv())
	for _, key := range []string{
		"GITHUB_TOKEN",
		"GH_TOKEN",
		"OPENAI_API_KEY",
		injectedGitHubTokenTaskEnv,
		"CHETTER_MCP_AUTH_TOKEN",
		"MCP_AUTH_TOKEN",
		"CHETTER_RUNNER_RPC_TOKEN",
		"DATABASE_DSN",
		"GITHUB_APP_PRIVATE_KEY_B64",
		"CUSTOM_ENV",
	} {
		if _, ok := env[key]; ok {
			t.Fatalf("%s should be removed from filtered host env: %#v", key, env)
		}
	}
	if got := env["PATH"]; got != "/usr/bin:/bin" {
		t.Fatalf("PATH = %q, want /usr/bin:/bin", got)
	}
}

func TestAgentEnvUsesInjectedGitHubTokenWithoutLeakingHostTokens(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "runner-github-token")
	t.Setenv("GH_TOKEN", "runner-gh-token")
	t.Setenv("OPENAI_API_KEY", "runner-openai-key")
	t.Setenv("CHETTER_MCP_AUTH_TOKEN", "admin-mcp-token")
	t.Setenv("MCP_AUTH_TOKEN", "server-admin-token")
	t.Setenv("DATABASE_DSN", "root@tcp(localhost:4000)/")
	req := task.TaskRequest{
		TaskID: "task-123",
		Agent:  "reviewer",
		Env: map[string]string{
			"CUSTOM_ENV":               "custom-value",
			"GITHUB_TOKEN":             "[redacted]",
			"GH_TOKEN":                 "task-gh-token",
			"OPENAI_API_KEY":           "task-openai-key",
			injectedGitHubTokenTaskEnv: "ghs_claim_token",
			privilegedMCPProfileEnv:    "true",
		},
	}

	env := envListToMap((&Runner{}).agentEnv(req, "/tmp/ws", "", pi.New()))
	if got := env["GITHUB_TOKEN"]; got != "ghs_claim_token" {
		t.Fatalf("GITHUB_TOKEN = %q, want injected task token", got)
	}
	if got := env["GH_TOKEN"]; got != "ghs_claim_token" {
		t.Fatalf("GH_TOKEN = %q, want injected task token", got)
	}
	if got := env["OPENAI_API_KEY"]; got != "runner-openai-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want runner-owned value", got)
	}
	if got := env["CUSTOM_ENV"]; got != "custom-value" {
		t.Fatalf("CUSTOM_ENV = %q, want custom-value", got)
	}
	if _, ok := env[injectedGitHubTokenTaskEnv]; ok {
		t.Fatalf("private injected token env should not be forwarded: %#v", env)
	}
	if _, ok := env[privilegedMCPProfileEnv]; ok {
		t.Fatalf("privileged mcp marker should not be forwarded: %#v", env)
	}
	for _, key := range []string{"CHETTER_MCP_AUTH_TOKEN", "MCP_AUTH_TOKEN", "DATABASE_DSN"} {
		if _, ok := env[key]; ok {
			t.Fatalf("%s should not be forwarded from host env: %#v", key, env)
		}
	}
}

func TestCloneURLForRequestUsesTaskScopedGitHubToken(t *testing.T) {
	got := cloneURLForRequest(task.TaskRequest{
		GitURL: "https://github.com/flatout-works/chetter.git",
		Env:    map[string]string{injectedGitHubTokenTaskEnv: "ghs_claim_token"},
	}, "")
	if !strings.Contains(got, "x-access-token:ghs_claim_token@github.com") {
		t.Fatalf("clone URL = %q, want scoped GitHub token", got)
	}
}

func TestCloneURLForRequestDoesNotLeakTaskTokenToNonGitHubHost(t *testing.T) {
	got := cloneURLForRequest(task.TaskRequest{
		GitURL: "https://git.example.test/flatout-works/chetter.git",
		Env:    map[string]string{injectedGitHubTokenTaskEnv: "ghs_claim_token"},
	}, "")
	if got != "https://git.example.test/flatout-works/chetter.git" {
		t.Fatalf("clone URL = %q, want original URL", got)
	}
}

func TestCloneURLForRequestDoesNotLeakRunnerPATToNonGitHubHost(t *testing.T) {
	got := cloneURLForRequest(task.TaskRequest{
		GitURL: "https://git.example.test/flatout-works/chetter.git",
	}, "ghp_runner_pat")
	if got != "https://git.example.test/flatout-works/chetter.git" {
		t.Fatalf("clone URL = %q, want original URL", got)
	}
}

func TestCloneURLForRequestPrefersTaskScopedGitHubTokenOverRunnerPAT(t *testing.T) {
	got := cloneURLForRequest(task.TaskRequest{
		GitURL: "https://github.com/flatout-works/chetter.git",
		Env:    map[string]string{injectedGitHubTokenTaskEnv: "ghs_claim_token"},
	}, "ghp_runner_pat")
	if !strings.Contains(got, "x-access-token:ghs_claim_token@github.com") || strings.Contains(got, "ghp_runner_pat") {
		t.Fatalf("clone URL = %q, want task-scoped token precedence", got)
	}
}

func TestCloneArgsForPinnedReviewHeadAvoidsBranchCheckout(t *testing.T) {
	args := cloneArgsForRequest(task.TaskRequest{
		GitRef: "feature",
		Env:    map[string]string{"PR_HEAD_SHA": "abc123"},
	}, "https://github.com/fork/repo.git")
	if !hasValue(args, "--no-checkout") {
		t.Fatalf("clone args = %v, want --no-checkout", args)
	}
	if !hasAdjacentArgs(args, "-b", "feature") {
		t.Fatalf("clone args = %v, want branch fetch", args)
	}
}

func TestCloneRepositoryPinsReviewHeadSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	origin := t.TempDir()
	runGitForTest(t, origin, "init")
	runGitForTest(t, origin, "config", "user.email", "test@example.com")
	runGitForTest(t, origin, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(origin, "file.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runGitForTest(t, origin, "add", "file.txt")
	runGitForTest(t, origin, "commit", "-m", "base")
	runGitForTest(t, origin, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(origin, "file.txt"), []byte("authorized\n"), 0644); err != nil {
		t.Fatalf("write authorized file: %v", err)
	}
	runGitForTest(t, origin, "commit", "-am", "authorized")
	authorizedSHA := gitOutputForTest(t, origin, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(origin, "file.txt"), []byte("raced\n"), 0644); err != nil {
		t.Fatalf("write raced file: %v", err)
	}
	runGitForTest(t, origin, "commit", "-am", "raced")

	wsDir := t.TempDir()
	err := cloneRepository(context.Background(), wsDir, task.TaskRequest{
		GitURL: origin,
		GitRef: "feature",
		Env:    map[string]string{"PR_HEAD_SHA": authorizedSHA},
	}, "", "")
	if err != nil {
		t.Fatalf("cloneRepository: %v", err)
	}
	if got := gitOutputForTest(t, wsDir, "rev-parse", "HEAD"); got != authorizedSHA {
		t.Fatalf("HEAD = %q, want authorized SHA %q", got, authorizedSHA)
	}
	data, err := os.ReadFile(filepath.Join(wsDir, "file.txt"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(data) != "authorized\n" {
		t.Fatalf("cloned file = %q, want authorized content", string(data))
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

	if err := opencode.GenerateConfigForTaskWithRunnerToken(wsDir, "http://localhost:9999/mcp", "runner-token", "", "", true, task.TaskRequest{}, true); err != nil {
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
	headers, ok := bridge["headers"].(map[string]any)
	if !ok {
		t.Fatal("expected runner-bridge MCP to include auth headers")
	}
	if headers["Authorization"] != "Bearer runner-token" {
		t.Errorf("unexpected runner-bridge auth header: %v", headers["Authorization"])
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

func TestDockerAgentResumeRegeneratesHarnessConfigWithFreshRunnerBridge(t *testing.T) {
	wsDir := t.TempDir()
	events := make(chan *runnerv1.TaskEvent, 8)
	fakeHarness := &recordingResumeHarness{}
	r := &Runner{
		cfg: &config.Config{ChetterMCP: config.ChetterMCPConfig{
			URL:       "https://chetter.example.test/mcp",
			AuthToken: "chetter-token",
		}},
		rpcClient: recordingRunnerRPCClient{events: events},
	}
	req := task.TaskRequest{
		TaskID:                 "task-resume",
		Prompt:                 "continue",
		AgentImage:             "runner:latest",
		ResumeWorkspacePath:    wsDir,
		ResumeHarnessSessionID: "session-123",
		MCPProfiles: []task.MCPProfile{{
			Name: "chetter-orchestration",
			URL:  "https://chetter.example.test/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:CHETTER_MCP_AUTH_TOKEN}",
			},
		}},
		Env: map[string]string{
			"GITHUB_REPO":           "flatout-works/chetter",
			privilegedMCPProfileEnv: "true",
		},
	}

	r.runDockerAgentResume(context.Background(), &task.TaskSession{TaskID: req.TaskID}, req, fakeHarness)

	if fakeHarness.calls != 1 {
		t.Fatalf("GenerateConfig calls = %d, want 1", fakeHarness.calls)
	}
	if fakeHarness.wsDir != wsDir {
		t.Fatalf("GenerateConfig workspace = %q, want %q", fakeHarness.wsDir, wsDir)
	}
	if fakeHarness.runnerMCPURL == "" {
		t.Fatal("GenerateConfig runner MCP URL is empty")
	}
	if fakeHarness.runnerMCPToken == "" {
		t.Fatal("GenerateConfig runner MCP token is empty")
	}
	if fakeHarness.chetterMCPURL != "https://chetter.example.test/mcp" {
		t.Fatalf("GenerateConfig chetter URL = %q", fakeHarness.chetterMCPURL)
	}
	if fakeHarness.chetterMCPToken != "chetter-token" {
		t.Fatalf("GenerateConfig chetter token = %q", fakeHarness.chetterMCPToken)
	}
	if len(fakeHarness.req.MCPProfiles) != 1 || fakeHarness.req.MCPProfiles[0].Name != "chetter-orchestration" {
		t.Fatalf("GenerateConfig MCP profiles = %#v, want preserved chetter-orchestration profile", fakeHarness.req.MCPProfiles)
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

func TestRunTaskFailsWhenHarnessConfigFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspaceRoot := t.TempDir()
	events := make(chan *runnerv1.TaskEvent, 8)
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	r := &Runner{
		cfg: &config.Config{
			Runner: config.RunnerConfig{WorkspaceRoot: workspaceRoot, MaxConcurrent: 1},
		},
		defaultHarness: "opencode",
		wsManager:      workspace.NewManager(workspaceRoot),
		rpcClient:      recordingRunnerRPCClient{events: events},
		runCtx:         context.Background(),
		tasks:          map[string]*task.TaskSession{},
		runnerID:       "runner-test",
		startedAt:      time.Now(),
		terminalTasks:  map[string]struct{}{},
		cancelledTasks: map[string]struct{}{},
		sem:            sem,
	}

	r.runTask(task.TaskRequest{
		TaskID:     "task-config-error",
		Prompt:     "review",
		TimeoutSec: 30,
		MCPProfiles: []task.MCPProfile{{
			Name: "missing-profile",
		}},
	})

	event := waitForTaskEvent(t, events, "error")
	if !strings.Contains(event.Error, `harness config: mcp profile "missing-profile" url is required`) {
		t.Fatalf("error = %q, want harness config failure", event.Error)
	}
	if strings.Contains(event.Error, "agent_image is required") {
		t.Fatalf("task continued after harness config failure: %q", event.Error)
	}
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
			"CUSTOM_ENV":               "custom-value",
			"OPENAI_API_KEY":           "task-key",
			"GITHUB_TOKEN":             "[redacted]",
			injectedGitHubTokenTaskEnv: "ghs_claim_token",
			privilegedMCPProfileEnv:    "true",
		},
	}
	args := dockerRPCArgs(req, "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), false, "", "")

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
	if !hasAdjacentArgs(args, "-e", "GITHUB_TOKEN=ghs_claim_token") {
		t.Fatalf("expected injected GitHub token to be forwarded as GITHUB_TOKEN, got %v", args)
	}
	if hasAdjacentArgs(args, "-e", injectedGitHubTokenTaskEnv+"=ghs_claim_token") {
		t.Fatalf("private injected token env must not be forwarded directly, got %v", args)
	}
	if hasAdjacentArgs(args, "-e", privilegedMCPProfileEnv+"=true") {
		t.Fatalf("privileged mcp marker must not be forwarded directly, got %v", args)
	}
}

func TestDockerRPCArgsRoutesChetterMCPThroughProxyForGVisor(t *testing.T) {
	h := pi.New()
	req := task.TaskRequest{
		TaskID:     "task-123",
		AgentImage: "ghcr.io/flatout-works/chetter-runner:main",
		ProviderID: "synthetic",
		ModelID:    "pi-model",
		Env:        map[string]string{},
	}
	args := dockerRPCArgs(req, "/tmp/ws", "chetter-task-task-123", h, h.RpcCommand(req), true, "chetter_default", "172.21.0.1")
	if !hasAdjacentArgs(args, "-e", "HTTP_PROXY=http://172.21.0.1:18080") {
		t.Fatalf("expected HTTP proxy env, got %v", args)
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-e" && strings.HasPrefix(args[i+1], "NO_PROXY=") && strings.Contains(args[i+1], "chetter-mcp") {
			t.Fatalf("NO_PROXY should not bypass chetter-mcp in gVisor args: %v", args)
		}
		if args[i] == "-e" && strings.HasPrefix(args[i+1], "no_proxy=") && strings.Contains(args[i+1], "chetter-mcp") {
			t.Fatalf("no_proxy should not bypass chetter-mcp in gVisor args: %v", args)
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

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

func hasValue(values []string, want string) bool {
	return indexOf(values, want) >= 0
}

func hasAdjacentArgs(values []string, key, value string) bool {
	for i := 0; i < len(values)-1; i++ {
		if values[i] == key && values[i+1] == value {
			return true
		}
	}
	return false
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func envListToMap(values []string) map[string]string {
	env := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if ok {
			env[key] = val
		}
	}
	return env
}

type recordingRunnerRPCClient struct {
	events chan *runnerv1.TaskEvent
}

func (c recordingRunnerRPCClient) RegisterRunner(context.Context, *connect.Request[runnerv1.RegisterRunnerRequest]) (*connect.Response[runnerv1.RegisterRunnerResponse], error) {
	return connect.NewResponse(&runnerv1.RegisterRunnerResponse{}), nil
}

func (c recordingRunnerRPCClient) Heartbeat(context.Context, *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	return connect.NewResponse(&runnerv1.HeartbeatResponse{}), nil
}

func (c recordingRunnerRPCClient) ClaimTask(context.Context, *connect.Request[runnerv1.ClaimTaskRequest]) (*connect.Response[runnerv1.ClaimTaskResponse], error) {
	return connect.NewResponse(&runnerv1.ClaimTaskResponse{}), nil
}

func (c recordingRunnerRPCClient) ReportTaskEvents(_ context.Context, req *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error) {
	for _, event := range req.Msg.Events {
		c.events <- event
	}
	return connect.NewResponse(&runnerv1.ReportTaskEventsResponse{}), nil
}

func (c recordingRunnerRPCClient) PruneWorkspaces(context.Context, *connect.Request[runnerv1.PruneWorkspacesRequest]) (*connect.Response[runnerv1.PruneWorkspacesResponse], error) {
	return connect.NewResponse(&runnerv1.PruneWorkspacesResponse{}), nil
}

func (c recordingRunnerRPCClient) GitHubCreateIssue(context.Context, *connect.Request[runnerv1.GitHubCreateIssueRequest]) (*connect.Response[runnerv1.GitHubCreateIssueResponse], error) {
	return connect.NewResponse(&runnerv1.GitHubCreateIssueResponse{}), nil
}

func (c recordingRunnerRPCClient) GitHubIssueComment(context.Context, *connect.Request[runnerv1.GitHubIssueCommentRequest]) (*connect.Response[runnerv1.GitHubIssueCommentResponse], error) {
	return connect.NewResponse(&runnerv1.GitHubIssueCommentResponse{}), nil
}

func (c recordingRunnerRPCClient) GitHubCreatePR(context.Context, *connect.Request[runnerv1.GitHubCreatePRRequest]) (*connect.Response[runnerv1.GitHubCreatePRResponse], error) {
	return connect.NewResponse(&runnerv1.GitHubCreatePRResponse{}), nil
}

func (c recordingRunnerRPCClient) GitHubPRReview(context.Context, *connect.Request[runnerv1.GitHubPRReviewRequest]) (*connect.Response[runnerv1.GitHubPRReviewResponse], error) {
	return connect.NewResponse(&runnerv1.GitHubPRReviewResponse{}), nil
}

func waitForTaskEvent(t *testing.T, events <-chan *runnerv1.TaskEvent, status string) *runnerv1.TaskEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Status == status {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s event", status)
		}
	}
}

type recordingResumeHarness struct {
	calls           int
	wsDir           string
	runnerMCPURL    string
	runnerMCPToken  string
	chetterMCPURL   string
	chetterMCPToken string
	req             task.TaskRequest
}

func (h *recordingResumeHarness) Name() string { return "recording-resume" }

func (h *recordingResumeHarness) GenerateConfig(wsDir, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	h.calls++
	h.wsDir = wsDir
	h.runnerMCPURL = runnerMCPURL
	h.runnerMCPToken = runnerMCPToken
	h.chetterMCPURL = chetterMCPURL
	h.chetterMCPToken = chetterMCPToken
	h.req = req
	return nil
}

func (h *recordingResumeHarness) ConfigFilePath(wsDir string) string {
	return filepath.Join(wsDir, "config.json")
}
func (h *recordingResumeHarness) ConfigFilePathGlobal(wsDir string) string {
	return filepath.Join(wsDir, "global.json")
}
func (h *recordingResumeHarness) Env(wsDir string, secret string, req task.TaskRequest) map[string]string {
	return nil
}
func (h *recordingResumeHarness) ServeCommand(port int) []string { return nil }
func (h *recordingResumeHarness) ServeArgsResume(port int) []string {
	return nil
}
func (h *recordingResumeHarness) ServerPassword() string { return "secret" }
func (h *recordingResumeHarness) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return nil
}
func (h *recordingResumeHarness) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return "", nil
}
func (h *recordingResumeHarness) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	return "", nil
}
func (h *recordingResumeHarness) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return nil
}
func (h *recordingResumeHarness) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return "", nil
}
func (h *recordingResumeHarness) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return "", nil
}
func (h *recordingResumeHarness) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
}
func (h *recordingResumeHarness) PipeOutput(taskID, stream string, reader io.Reader) {}
func (h *recordingResumeHarness) ResolvedModelID(req task.TaskRequest) string {
	return "test/model"
}
func (h *recordingResumeHarness) SupportsRpc() bool { return false }
func (h *recordingResumeHarness) RpcCommand(req task.TaskRequest) []string {
	return nil
}
func (h *recordingResumeHarness) DockerConfigPath(wsDir string) string {
	return filepath.Join(wsDir, "docker.json")
}
