// Package agentenv builds the runner-owned environment and Git setup for agent
// processes and containers.
package agentenv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	gitAskpassFilename       = ".chetter-git-askpass"
	GitHubCredentialURLEnv   = "CHETTER_GITHUB_CREDENTIAL_URL"
	GitHubCredentialTokenEnv = "CHETTER_GITHUB_CREDENTIAL_TOKEN"
)

const gitAskpassScript = `#!/bin/sh
case "${1:-}" in
  *Username*) printf '%s\n' x-access-token; exit 0 ;;
esac
if [ -n "${CHETTER_GITHUB_CREDENTIAL_URL:-}" ] && [ -n "${CHETTER_GITHUB_CREDENTIAL_TOKEN:-}" ]; then
  token="$(
    printf 'request = "POST"\nheader = "Authorization: Bearer %s"\n' "$CHETTER_GITHUB_CREDENTIAL_TOKEN" |
      curl --fail --silent --show-error --config - "$CHETTER_GITHUB_CREDENTIAL_URL" 2>/dev/null
  )" || token=""
  if [ -n "$token" ]; then
    printf '%s\n' "$token"
    exit 0
  fi
fi
if [ -n "${GITHUB_TOKEN:-}" ]; then
  printf '%s\n' "$GITHUB_TOKEN"
  exit 0
fi
exit 1
`

// HostWorkspaceDir maps a manager-owned runner workspace path to its host bind
// mount path when the runner is itself containerized. Both the source path and
// configured host root must be absolute, and the source must remain beneath
// workspaceRoot.
func HostWorkspaceDir(containerPath, workspaceRoot string) (string, error) {
	if containsTraversal(containerPath) || containsTraversal(workspaceRoot) {
		return "", fmt.Errorf("workspace path and root must not contain traversal")
	}
	containerPath = filepath.Clean(containerPath)
	workspaceRoot = filepath.Clean(workspaceRoot)
	if !filepath.IsAbs(containerPath) || !filepath.IsAbs(workspaceRoot) {
		return "", fmt.Errorf("workspace path and root must be absolute: path=%q root=%q", containerPath, workspaceRoot)
	}
	rel, err := filepath.Rel(workspaceRoot, containerPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace path %q is not beneath root %q", containerPath, workspaceRoot)
	}
	hostRoot := os.Getenv("HOST_WORKSPACE_ROOT")
	if hostRoot == "" {
		return containerPath, nil
	}
	hostRoot = filepath.Clean(hostRoot)
	if !filepath.IsAbs(hostRoot) {
		return "", fmt.Errorf("HOST_WORKSPACE_ROOT %q must be absolute", hostRoot)
	}
	mapped := filepath.Join(hostRoot, rel)
	mappedRel, err := filepath.Rel(hostRoot, mapped)
	if err != nil || mappedRel == ".." || strings.HasPrefix(mappedRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("mapped host workspace path escapes HOST_WORKSPACE_ROOT")
	}
	return mapped, nil
}

func containsTraversal(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// AppendRunnerOwnedEnv appends non-empty runner-owned environment values.
func AppendRunnerOwnedEnv(env []string) []string {
	for _, key := range runnerOwnedEnvKeys() {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// TaskProcessEnv removes runner-control credentials from the ambient process
// environment before it is inherited by a local task process.
func TaskProcessEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, value := range env {
		key, _, _ := strings.Cut(value, "=")
		if isRunnerPrivateEnv(key) {
			continue
		}
		out = append(out, value)
	}
	return out
}

// AddRunnerOwnedEnv adds non-empty runner-owned environment values to env.
func AddRunnerOwnedEnv(env map[string]string) {
	for _, key := range runnerOwnedEnvKeys() {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
}

// PrepareGitWorkspace writes the Git credential helper and configures the
// resolved author identity when workspace is a Git repository.
func PrepareGitWorkspace(ctx context.Context, workspace string, req task.TaskRequest) error {
	if req.GitAuthorName == "" || req.GitAuthorEmail == "" {
		return fmt.Errorf("task has no resolved Git identity")
	}
	if err := WriteGitAskpass(workspace); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(workspace, ".git")); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect Git workspace: %w", err)
	}
	for _, args := range [][]string{{"config", "--local", "user.name", req.GitAuthorName}, {"config", "--local", "user.email", req.GitAuthorEmail}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workspace
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// GitIdentityEnv returns author, committer, and credential-helper variables.
func GitIdentityEnv(req task.TaskRequest, workspace string) []string {
	env := []string{
		"GIT_AUTHOR_NAME=" + req.GitAuthorName,
		"GIT_AUTHOR_EMAIL=" + req.GitAuthorEmail,
		"GIT_COMMITTER_NAME=" + req.GitAuthorName,
		"GIT_COMMITTER_EMAIL=" + req.GitAuthorEmail,
	}
	if req.GitHubCredentialURL != "" && req.GitHubCredentialToken != "" {
		env = append(env,
			GitHubCredentialURLEnv+"="+req.GitHubCredentialURL,
			GitHubCredentialTokenEnv+"="+req.GitHubCredentialToken,
		)
	}
	return append(env, GitCredentialEnv(workspace)...)
}

// WriteGitAskpass writes the GitHub token askpass helper into workspace.
func WriteGitAskpass(workspace string) error {
	if err := os.WriteFile(filepath.Join(workspace, gitAskpassFilename), []byte(gitAskpassScript), 0700); err != nil {
		return fmt.Errorf("write Git askpass helper: %w", err)
	}
	return nil
}

// GitCloneCredentialDir returns the directory used to scope clone credentials.
func GitCloneCredentialDir(workspace string) string {
	return filepath.Dir(workspace)
}

// GitCredentialEnv returns the no-secret Git askpass configuration.
func GitCredentialEnv(workspace string) []string {
	return []string{"GIT_ASKPASS=" + filepath.Join(workspace, gitAskpassFilename), "GIT_ASKPASS_REQUIRE=force", "GIT_TERMINAL_PROMPT=0"}
}

// ProviderCredentialEnv returns the resolved provider credential environment.
func ProviderCredentialEnv(req task.TaskRequest) []string {
	key := strings.TrimSpace(req.ProviderAPIKeyEnv)
	if key == "" {
		return nil
	}
	if value := os.Getenv(key); value != "" {
		return []string{key + "=" + value}
	}
	return nil
}

// IsManagedEnv reports whether key is owned by the runner and must not be
// overridden by task-provided environment values.
func IsManagedEnv(key string, req task.TaskRequest) bool {
	if IsRunnerOwnedEnv(key) || isRunnerPrivateEnv(key) || key == GitHubCredentialURLEnv || key == GitHubCredentialTokenEnv {
		return true
	}
	credKey := strings.TrimSpace(req.ProviderAPIKeyEnv)
	if credKey != "" && key == credKey {
		return true
	}
	for _, endpointKey := range endpointTokenEnvKeys(req.McpEndpoints) {
		if key == endpointKey {
			return true
		}
	}
	return false
}

func isRunnerPrivateEnv(key string) bool {
	switch key {
	case "CHETTER_RUNNER_AUTH_TOKEN", "CHETTER_RUNNER_RPC_TOKEN", "MCP_AUTH_TOKEN", "CHETTER_MCP_AUTH_TOKEN", GitHubCredentialURLEnv, GitHubCredentialTokenEnv:
		return true
	default:
		return false
	}
}

// ValidateEndpointTokenEnvironment verifies that endpoint bearer credentials
// exist and do not collide with harness control variables.
func ValidateEndpointTokenEnvironment(endpoints []task.MCPEndpoint) error {
	for _, key := range endpointTokenEnvKeys(endpoints) {
		if IsHarnessControlEnv(key) {
			return fmt.Errorf("MCP endpoint bearer_token_env %s conflicts with a reserved harness environment variable", key)
		}
		if value, ok := os.LookupEnv(key); !ok || value == "" {
			return fmt.Errorf("runner environment variable %s is required", key)
		}
	}
	return nil
}

// IsHarnessControlEnv reports whether key controls runner-managed harness
// behavior and therefore cannot be used for an endpoint token.
func IsHarnessControlEnv(key string) bool {
	switch key {
	case "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_ATTRIBUTION_HEADER", "CLAUDE_SERVE_PROXY_TOKEN",
		"CODEWHALE_CONFIG_DIR", "CODEWHALE_CONFIG_PATH", "CODEWHALE_OFFLINE", "CODEWHALE_RUNTIME_TOKEN", "CODEWHALE_PROVIDER", "CODEWHALE_MODEL", "DEEPSEEK_MCP_CONFIG",
		"OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_SERVER_PASSWORD",
		"PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "PI_OFFLINE", "PI_SKIP_VERSION_CHECK", "PI_TELEMETRY":
		return true
	default:
		return false
	}
}

// AppendDockerManagedEnvironment appends runner-owned variables to Docker
// arguments while preserving endpoint token indirection.
func AppendDockerManagedEnvironment(args []string, req task.TaskRequest) []string {
	endpointKeys := endpointTokenEnvKeys(req.McpEndpoints)
	selected := make(map[string]struct{}, len(endpointKeys))
	for _, key := range endpointKeys {
		selected[key] = struct{}{}
	}
	for _, key := range runnerOwnedEnvKeys() {
		if _, isEndpointToken := selected[key]; isEndpointToken {
			continue
		}
		if value := os.Getenv(key); value != "" {
			args = append(args, "-e", key+"="+value)
		}
	}
	providerKey := strings.TrimSpace(req.ProviderAPIKeyEnv)
	if _, isEndpointToken := selected[providerKey]; providerKey != "" && !isEndpointToken && !IsRunnerOwnedEnv(providerKey) {
		if value := os.Getenv(providerKey); value != "" {
			args = append(args, "-e", providerKey+"="+value)
		}
	}
	for _, key := range endpointKeys {
		args = append(args, "-e", key)
	}
	return args
}

// IsRunnerOwnedEnv reports whether key is reserved for runner credentials or
// harness control.
func IsRunnerOwnedEnv(key string) bool {
	for _, owned := range runnerOwnedEnvKeys() {
		if key == owned {
			return true
		}
	}
	return false
}

// ShellQuoteArgs quotes arguments for diagnostic shell commands.
func ShellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, ShellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

// ShellQuoteArg quotes one argument for a POSIX shell.
func ShellQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	for _, c := range arg {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' && c != '/' && c != ':' && c != '@' && c != '+' {
			return `'` + strings.ReplaceAll(arg, `'`, `'\''`) + `'`
		}
	}
	return arg
}

func endpointTokenEnvKeys(endpoints []task.MCPEndpoint) []string {
	keys := make([]string, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		key := strings.TrimSpace(endpoint.BearerTokenEnv)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func runnerOwnedEnvKeys() []string {
	return []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"CLAUDE_CODE_SUBAGENT_MODEL",
		"CLAUDE_SERVE_PROXY_TOKEN",
		"CODEWHALE_CONFIG_DIR",
		"CODEWHALE_CONFIG_PATH",
		"CODEWHALE_RUNTIME_TOKEN",
		"CHETTER_TASK_ID",
		"CHETTER_AGENT_SESSION_ID",
		"CHETTER_USER_PROMPT_ID",
		"CHETTER_EXECUTION_ID",
		"GITHUB_TOKEN",
		"MEM9_API_KEY",
		"MEM9_API_URL",
		"MEM9_DEBUG",
		"MEM9_HOME",
		"OPENAI_API_KEY",
		"DEEPSEEK_API_KEY",
		"OPENCODE_API_KEY",
		"SYNTHETIC_API_KEY",
		"ZAI_API_KEY",
		"GEMINI_API_KEY",
		"GROQ_API_KEY",
		"XAI_API_KEY",
	}
}
