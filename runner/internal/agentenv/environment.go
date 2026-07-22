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

const gitAskpassFilename = ".chetter-git-askpass"

// HostWorkspaceDir maps a runner container workspace path to its host bind
// mount path when the runner is itself containerized.
func HostWorkspaceDir(containerPath string) string {
	if hostRoot := os.Getenv("HOST_WORKSPACE_ROOT"); hostRoot != "" {
		if after, found := strings.CutPrefix(containerPath, "/var/lib/chetter-runner"); found {
			return hostRoot + after
		}
	}
	return containerPath
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
	return append(env, GitCredentialEnv(workspace)...)
}

// WriteGitAskpass writes the GitHub token askpass helper into workspace.
func WriteGitAskpass(workspace string) error {
	if err := os.WriteFile(filepath.Join(workspace, gitAskpassFilename), []byte("#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' x-access-token ;;\n  *) printf '%s\\n' \"$GITHUB_TOKEN\" ;;\nesac\n"), 0700); err != nil {
		return fmt.Errorf("write Git askpass helper: %w", err)
	}
	return nil
}

// GitCloneCredentialDir returns the directory used to scope clone credentials.
func GitCloneCredentialDir(workspace string) string {
	return filepath.Dir(workspace)
}

// GitCredentialEnv returns Git askpass variables when a GitHub token is set.
func GitCredentialEnv(workspace string) []string {
	if os.Getenv("GITHUB_TOKEN") == "" {
		return nil
	}
	return []string{"GIT_ASKPASS=" + filepath.Join(workspace, gitAskpassFilename), "GIT_TERMINAL_PROMPT=0"}
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
	if IsRunnerOwnedEnv(key) {
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
		"CODEWHALE_CONFIG_DIR", "CODEWHALE_OFFLINE", "CODEWHALE_RUNTIME_TOKEN", "CODEWHALE_PROVIDER", "CODEWHALE_MODEL", "DEEPSEEK_MCP_CONFIG",
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

// InjectPATIntoURL adds pat to an HTTPS clone URL.
func InjectPATIntoURL(raw, pat string) string {
	if !strings.HasPrefix(raw, "https://") || pat == "" {
		return raw
	}
	return "https://" + pat + "@" + raw[len("https://"):]
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
