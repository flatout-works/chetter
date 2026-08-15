package agentenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestShellQuoteArg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "plain", want: "plain"},
		{input: "it's", want: "'it'\\''s'"},
		{input: "", want: `""`},
	}
	for _, tc := range tests {
		if got := ShellQuoteArg(tc.input); got != tc.want {
			t.Errorf("ShellQuoteArg(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestValidateEndpointTokenEnvironment(t *testing.T) {
	t.Setenv("MCP_TOKEN", "secret")
	if err := ValidateEndpointTokenEnvironment([]task.MCPEndpoint{{BearerTokenEnv: "MCP_TOKEN"}}); err != nil {
		t.Fatalf("valid endpoint token: %v", err)
	}
	if err := ValidateEndpointTokenEnvironment([]task.MCPEndpoint{{BearerTokenEnv: "MISSING_MCP_TOKEN"}}); err == nil {
		t.Fatal("missing endpoint token should fail")
	}
	if err := ValidateEndpointTokenEnvironment([]task.MCPEndpoint{{BearerTokenEnv: "OPENCODE_CONFIG"}}); err == nil {
		t.Fatal("harness control variable should fail")
	}
	if err := ValidateEndpointTokenEnvironment([]task.MCPEndpoint{{BearerTokenEnv: "CODEWHALE_CONFIG_PATH"}}); err == nil {
		t.Fatal("CodeWhale config path should be reserved")
	}
}

func TestPrepareGitWorkspaceWritesAskpass(t *testing.T) {
	workspace := t.TempDir()
	req := task.TaskRequest{GitAuthorName: "Test User", GitAuthorEmail: "test@example.com"}
	if err := PrepareGitWorkspace(context.Background(), workspace, req); err != nil {
		t.Fatalf("PrepareGitWorkspace: %v", err)
	}
	path := filepath.Join(workspace, gitAskpassFilename)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("askpass file: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("askpass permissions = %o, want 700", info.Mode().Perm())
	}
}

func TestManagedEnvironment(t *testing.T) {
	req := task.TaskRequest{
		ProviderAPIKeyEnv: "PROVIDER_TOKEN",
		McpEndpoints:      []task.MCPEndpoint{{BearerTokenEnv: "MCP_TOKEN"}},
	}
	for _, key := range []string{"PROVIDER_TOKEN", "OPENAI_API_KEY", "MCP_TOKEN", "CODEWHALE_CONFIG_PATH", GitHubCredentialURLEnv, GitHubCredentialTokenEnv, "CHETTER_RUNNER_AUTH_TOKEN"} {
		if !IsManagedEnv(key, req) {
			t.Errorf("IsManagedEnv(%q) = false, want true", key)
		}
	}
	if IsManagedEnv("CUSTOM_ENV", req) {
		t.Fatal("custom environment should not be managed")
	}
}

func TestGitAskpassBrokerRefreshAndFallback(t *testing.T) {
	dir := t.TempDir()
	if err := WriteGitAskpass(dir); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(dir, "count")
	captureFile := filepath.Join(dir, "curl-config")
	curlPath := filepath.Join(dir, "curl")
	curlScript := `#!/bin/sh
config="$(cat)"
printf '%s' "$config" > "$CURL_CONFIG_CAPTURE"
if [ "${CURL_FAIL:-}" = 1 ]; then exit 22; fi
n=0
if [ -f "$CURL_COUNT_FILE" ]; then n="$(cat "$CURL_COUNT_FILE")"; fi
n=$((n + 1))
printf '%s' "$n" > "$CURL_COUNT_FILE"
printf 'broker-token-%s' "$n"
`
	if err := os.WriteFile(curlPath, []byte(curlScript), 0700); err != nil {
		t.Fatal(err)
	}
	baseEnv := append(TaskProcessEnv(),
		"PATH="+dir+":"+os.Getenv("PATH"),
		GitHubCredentialURLEnv+"=http://runner/internal/github-credential",
		GitHubCredentialTokenEnv+"=capability-secret",
		"CURL_COUNT_FILE="+countFile,
		"CURL_CONFIG_CAPTURE="+captureFile,
	)
	run := func(prompt string, extra ...string) (string, error) {
		cmd := exec.Command(filepath.Join(dir, gitAskpassFilename), prompt)
		cmd.Env = append(baseEnv, extra...)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	username, err := run("Username for https://github.com")
	if err != nil || username != "x-access-token" {
		t.Fatalf("username = %q, err=%v", username, err)
	}
	first, err := run("Password for https://github.com")
	if err != nil || first != "broker-token-1" {
		t.Fatalf("first password = %q, err=%v", first, err)
	}
	second, err := run("Password for https://github.com")
	if err != nil || second != "broker-token-2" {
		t.Fatalf("second password = %q, err=%v", second, err)
	}
	config, err := os.ReadFile(captureFile)
	if err != nil || !strings.Contains(string(config), "Authorization: Bearer capability-secret") {
		t.Fatalf("curl config = %q, err=%v", config, err)
	}
	fallback, err := run("Password for https://github.com", "CURL_FAIL=1", "GITHUB_TOKEN=static-token")
	if err != nil || fallback != "static-token" {
		t.Fatalf("fallback = %q, err=%v", fallback, err)
	}
	if _, err := run("Password for https://github.com", "CURL_FAIL=1", "GITHUB_TOKEN="); err == nil {
		t.Fatal("askpass succeeded without broker or fallback token")
	}
}

func TestTaskProcessEnvRemovesRunnerRPCBearer(t *testing.T) {
	t.Setenv("CHETTER_RUNNER_AUTH_TOKEN", "runner-secret")
	t.Setenv("CHETTER_RUNNER_RPC_TOKEN", "runner-rpc-secret")
	t.Setenv("CUSTOM_TASK_BASE", "visible")
	joined := strings.Join(TaskProcessEnv(), "\n")
	if strings.Contains(joined, "runner-secret") || strings.Contains(joined, "runner-rpc-secret") {
		t.Fatal("runner RPC bearer leaked into task process environment")
	}
	if !strings.Contains(joined, "CUSTOM_TASK_BASE=visible") {
		t.Fatal("ordinary ambient environment was removed")
	}
}

func TestHostWorkspaceDir(t *testing.T) {
	t.Setenv("HOST_WORKSPACE_ROOT", "/host/runner")
	got, err := HostWorkspaceDir("/var/lib/chetter-runner/task-1/workspace", "/var/lib/chetter-runner")
	if err != nil {
		t.Fatalf("HostWorkspaceDir: %v", err)
	}
	if got != "/host/runner/task-1/workspace" {
		t.Fatalf("HostWorkspaceDir = %q", got)
	}

	t.Setenv("HOST_WORKSPACE_ROOT", "")
	got, err = HostWorkspaceDir("/workspace", "/")
	if err != nil {
		t.Fatalf("HostWorkspaceDir without root: %v", err)
	}
	if got != "/workspace" {
		t.Fatalf("HostWorkspaceDir without root = %q", got)
	}
}

func TestHostWorkspaceDirRejectsEscape(t *testing.T) {
	t.Setenv("HOST_WORKSPACE_ROOT", "/host/runner")
	if _, err := HostWorkspaceDir("/var/lib/other/task/workspace", "/var/lib/chetter-runner"); err == nil {
		t.Fatal("HostWorkspaceDir accepted workspace outside configured root")
	}
	if _, err := HostWorkspaceDir("relative/workspace", "/var/lib/chetter-runner"); err == nil {
		t.Fatal("HostWorkspaceDir accepted relative workspace")
	}
	if _, err := HostWorkspaceDir("/var/lib/chetter-runner/task/../outside", "/var/lib/chetter-runner"); err == nil {
		t.Fatal("HostWorkspaceDir accepted traversal workspace")
	}
}

func TestShellQuoteArgs(t *testing.T) {
	got := ShellQuoteArgs([]string{"opencode", "run", "hello world"})
	if !strings.Contains(got, "'hello world'") {
		t.Fatalf("ShellQuoteArgs = %q", got)
	}
}
