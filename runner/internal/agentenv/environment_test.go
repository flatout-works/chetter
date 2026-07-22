package agentenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestInjectPATIntoURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		pat  string
		want string
	}{
		{name: "https", raw: "https://github.com/owner/repo.git", pat: "secret", want: "https://secret@github.com/owner/repo.git"},
		{name: "empty pat", raw: "https://github.com/owner/repo.git", want: "https://github.com/owner/repo.git"},
		{name: "ssh", raw: "git@github.com:owner/repo.git", pat: "secret", want: "git@github.com:owner/repo.git"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := InjectPATIntoURL(tc.raw, tc.pat); got != tc.want {
				t.Fatalf("InjectPATIntoURL(%q, %q) = %q, want %q", tc.raw, tc.pat, got, tc.want)
			}
		})
	}
}

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
	for _, key := range []string{"PROVIDER_TOKEN", "OPENAI_API_KEY", "MCP_TOKEN"} {
		if !IsManagedEnv(key, req) {
			t.Errorf("IsManagedEnv(%q) = false, want true", key)
		}
	}
	if IsManagedEnv("CUSTOM_ENV", req) {
		t.Fatal("custom environment should not be managed")
	}
}

func TestHostWorkspaceDir(t *testing.T) {
	t.Setenv("HOST_WORKSPACE_ROOT", "/host/runner")
	got := HostWorkspaceDir("/var/lib/chetter-runner/task-1/workspace")
	if got != "/host/runner/task-1/workspace" {
		t.Fatalf("HostWorkspaceDir = %q", got)
	}

	t.Setenv("HOST_WORKSPACE_ROOT", "")
	if got := HostWorkspaceDir("/workspace"); got != "/workspace" {
		t.Fatalf("HostWorkspaceDir without root = %q", got)
	}
}

func TestShellQuoteArgs(t *testing.T) {
	got := ShellQuoteArgs([]string{"opencode", "run", "hello world"})
	if !strings.Contains(got, "'hello world'") {
		t.Fatalf("ShellQuoteArgs = %q", got)
	}
}
