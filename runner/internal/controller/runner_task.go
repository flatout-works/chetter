package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	containerWorkspaceDir   = "/workspace"
	containerCleanupTimeout = 30 * time.Second
	sessionExportTimeout    = 30 * time.Second
)

func executionKey(req task.TaskRequest) string {
	return req.ExecutionID
}

func containerNameForRequest(req task.TaskRequest) string {
	key := executionKey(req)
	return "chetter-task-" + req.TaskID + "-" + key
}

func (r *Runner) runTask(req task.TaskRequest) {
	defer func() { <-r.sem }()
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("runner panic", "taskID", req.TaskID, "panic", rec)
			r.publishStatusForRequest(req, "error", fmt.Sprintf("runner panic: %v", rec), nil)
		}
	}()
	if req.ExecutionID == "" {
		r.publishStatusForRequest(req, "error", "execution_id is required", nil)
		return
	}

	parent := r.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(req.TimeoutSec)*time.Second)
	defer cancel()

	session := &task.TaskSession{
		TaskID:      req.TaskID,
		ExecutionID: req.ExecutionID,
		Request:     req,
		Cancel:      cancel,
		StartedAt:   time.Now(),
	}
	r.mu.Lock()
	key := executionKey(req)
	r.tasks[key] = session
	r.totalStarted++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.tasks, key)
		if r.tasksChanged == nil {
			r.tasksChanged = make(chan struct{})
		}
		close(r.tasksChanged)
		r.tasksChanged = make(chan struct{})
		r.mu.Unlock()
	}()

	r.publishStatusForRequest(req, "running", "Preparing workspace...", nil)
	r.publishActivityEvent("agent", "Task Started", fmt.Sprintf("Task %s started", req.TaskID), "running", "", 0)

	h := r.harnessFor(req.Harness)

	if err := validateEndpointTokenEnvironment(req.McpEndpoints); err != nil {
		message := fmt.Sprintf("prepare MCP endpoints: %v", err)
		r.publishStatusForRequest(req, "error", message, nil)
		r.publishActivityEvent("agent", "Task Failed", message, "failed", message, time.Since(session.StartedAt).Milliseconds())
		return
	}

	if req.ResumeWorkspacePath != "" {
		serveHarness, ok := h.(harness.ServeHarness)
		if !ok {
			r.publishStatusForRequest(req, "error", fmt.Sprintf("harness %s cannot resume HTTP sessions", h.Name()), nil)
			return
		}
		mcpServer, err := r.startWorkspaceMCP(ctx, req.TaskID, req.ExecutionID)
		if err != nil {
			r.publishStatusForRequest(req, "error", fmt.Sprintf("mcp server: %v", err), nil)
			return
		}
		defer mcpServer.Close()
		mcpURL := runnerMCPURL(r, mcpServer)
		if err := h.GenerateConfig(req.ResumeWorkspacePath, mcpURL, r.taskChetterMCPURL(), r.taskChetterMCPToken(), req, false); err != nil {
			r.publishStatusForRequest(req, "error", fmt.Sprintf("generate resume harness config: %v", err), nil)
			return
		}
		r.runDockerAgentResume(ctx, session, req, serveHarness)
		return
	}

	wsDir, err := r.wsManager.Create(req.TaskID, executionKey(req))
	if err != nil {
		r.publishStatusForRequest(req, "error", err.Error(), nil)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Workspace creation failed: %v", err), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	session.WorkspaceDir = wsDir

	defer func() {
		if session.PreserveWorkspace {
			slog.Info("preserving workspace for checkpointed session", "taskID", req.TaskID, "workspace", session.WorkspaceDir)
			return
		}
		if err := r.wsManager.Destroy(req.TaskID, key); err != nil {
			slog.Warn("cleanup error", "taskID", req.TaskID, "err", err)
		}
	}()

	gitURL := req.GitURL
	if req.GitURL != "" {
		slog.Info("cloning", "taskID", req.TaskID, "url", req.GitURL)
		credentialDir := gitCloneCredentialDir(wsDir)
		if err := os.RemoveAll(wsDir); err != nil {
			slog.Warn("removing stale workspace", "taskID", req.TaskID, "err", err)
		}
		if err := os.MkdirAll(wsDir, 0750); err != nil {
			r.publishStatusForRequest(req, "error", err.Error(), nil)
			return
		}
		if err := writeGitAskpass(credentialDir); err != nil {
			r.publishStatusForRequest(req, "error", fmt.Sprintf("prepare Git credentials: %v", err), nil)
			return
		}
		if r.cfg.Git.PAT != "" && strings.HasPrefix(req.GitURL, "https://") {
			gitURL = injectPATIntoURL(req.GitURL, r.cfg.Git.PAT)
		}
		cloneCmd := exec.CommandContext(ctx, "git", "clone")
		if req.GitRef != "" {
			cloneCmd.Args = append(cloneCmd.Args, "-b", req.GitRef)
		}
		cloneCmd.Args = append(cloneCmd.Args, gitURL, ".")
		cloneCmd.Dir = wsDir
		cloneCmd.Env = append(os.Environ(), gitCredentialEnv(credentialDir)...)
		if r.cfg.Git.SSHKeyPath != "" {
			cloneCmd.Env = append(cloneCmd.Env, "GIT_SSH_COMMAND=ssh -i "+r.cfg.Git.SSHKeyPath+" -o StrictHostKeyChecking=no")
		}
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			slog.Error("clone error", "taskID", req.TaskID, "err", err, "output", string(out))
			r.publishStatusForRequest(req, "error", fmt.Sprintf("git clone: %v\n%s", err, string(out)), nil)
			r.publishActivityEvent("repo", "Git Clone Failed", fmt.Sprintf("Failed to clone %s", req.GitURL), "failed", fmt.Sprintf("%v\n%s", err, string(out)), time.Since(session.StartedAt).Milliseconds())
			return
		}
	}
	if err := prepareGitWorkspace(ctx, wsDir, req); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("configure Git identity: %v", err), nil)
		return
	}

	isLocal := r.executionMode() == "local"

	if len(req.ExtraFiles) > 0 {
		for filename, content := range req.ExtraFiles {
			filePath := filepath.Join(wsDir, filename)
			if err := os.MkdirAll(filepath.Dir(filePath), 0750); err != nil {
				slog.Warn("extra file mkdir", "taskID", req.TaskID, "file", filename, "err", err)
				continue
			}
			if err := os.WriteFile(filePath, content, 0644); err != nil {
				slog.Warn("extra file write", "taskID", req.TaskID, "file", filename, "err", err)
			} else {
				slog.Info("extra file written", "taskID", req.TaskID, "file", filename, "size", len(content))
			}
		}
	}

	mcpServer, err := r.startWorkspaceMCP(ctx, req.TaskID, req.ExecutionID)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("mcp server: %v", err), nil)
		return
	}
	defer mcpServer.Close()
	mcpURL := runnerMCPURL(r, mcpServer)

	if err := h.GenerateConfig(wsDir, mcpURL, r.taskChetterMCPURL(), r.taskChetterMCPToken(), req, isLocal); err != nil {
		message := fmt.Sprintf("generate harness config: %v", err)
		slog.Error("harness config failed", "taskID", req.TaskID, "err", err)
		r.publishStatusForRequest(req, "error", message, nil)
		r.publishActivityEvent("agent", "Task Failed", message, "failed", message, time.Since(session.StartedAt).Milliseconds())
		return
	}

	if req.AgentImage == "" {
		r.publishStatusForRequest(req, "error", "agent_image is required", nil)
		return
	}

	if rpcHarness, ok := h.(harness.RPCHarness); ok {
		if r.executionMode() == "local" {
			r.runRpcAgent(ctx, session, req, rpcHarness)
		} else {
			r.runDockerRpcAgent(ctx, session, req, rpcHarness)
		}
		return
	}
	serveHarness, ok := h.(harness.ServeHarness)
	if !ok {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness %s has no supported execution mode", h.Name()), nil)
		return
	}
	if r.executionMode() == "local" {
		r.runLocalAgent(ctx, session, req, serveHarness)
	} else {
		r.runDockerAgent(ctx, session, req, serveHarness)
	}
}

func (r *Runner) startWorkspaceMCP(ctx context.Context, taskID, executionID string) (*mcp.Server, error) {
	mcpServer, err := mcp.NewServer()
	if err != nil {
		return nil, err
	}
	r.registerGitHubMCPTools(mcpServer, taskID, executionID)
	slog.Info("MCP server started", "taskID", taskID, "addr", mcpServer.Addr())
	return mcpServer, nil
}

func (r *Runner) watchHarnessProgress(ctx context.Context, h harness.ServeHarness, req task.TaskRequest, baseURL, sessionID, secret, wsDir string, onToken func(task.TokenUsage)) (context.Context, func(), *progressWatchdog) {
	agentCtx, cancelAgent := context.WithCancel(ctx)

	idleCh := make(chan struct{})
	var idleOnce sync.Once
	onIdle := func() {
		idleOnce.Do(func() { close(idleCh) })
	}
	isIdle := func() bool {
		select {
		case <-idleCh:
			return true
		default:
			return false
		}
	}

	if aware, ok := h.(harness.CompletionAwareHarness); ok {
		aware.SetCompletionContext(sessionID, idleCh, onIdle)
	}

	var nudge func(context.Context) error
	if continuable, ok := h.(harness.SessionContinuable); ok {
		nudge = func(nudgeCtx context.Context) error {
			return continuable.ContinueSession(nudgeCtx, baseURL, sessionID, secret, req, wsDir)
		}
	}
	watchdog := startProgressWatchdog(ctx, cancelAgent, nudge, func(message string) {
		r.publishStatusForRequest(req, "running", message, nil)
	}, isIdle)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		h.WatchEvents(agentCtx, req.TaskID, baseURL, secret, func(status, message string) {
			watchdog.record(message)
			r.publishStatusForRequest(req, status, message, nil)
		}, onToken)
	}()
	var stopOnce sync.Once
	return agentCtx, func() {
		stopOnce.Do(func() {
			watchdog.stop()
			cancelAgent()
			<-watchDone
		})
	}, watchdog
}

type tokenUsageAccumulator struct {
	mu    sync.Mutex
	usage task.TokenUsage
}

func (a *tokenUsageAccumulator) add(usage task.TokenUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.usage.InputTokens += usage.InputTokens
	a.usage.OutputTokens += usage.OutputTokens
	a.usage.CacheReadTokens += usage.CacheReadTokens
	a.usage.CacheWriteTokens += usage.CacheWriteTokens
	a.usage.ReasoningTokens += usage.ReasoningTokens
	a.usage.CostCents += usage.CostCents
}

func (a *tokenUsageAccumulator) snapshot() task.TokenUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usage
}

func runnerMCPURL(r *Runner, mcpServer *mcp.Server) string {
	_, port, _ := net.SplitHostPort(mcpServer.Addr())
	if r.executionMode() == "local" {
		return "http://127.0.0.1:" + port + "/mcp"
	}
	// For Docker: use the runner's own IP on the Docker network.
	// gVisor containers route through HTTP_PROXY (running on the runner)
	// which can reach this IP. Non-gVisor containers connect directly
	// via the shared Docker network.
	runnerIP := hostIP(runcNetwork())
	return "http://" + runnerIP + ":" + port + "/mcp"
}

func (r *Runner) taskChetterMCPURL() string {
	if r.cfg.ChetterMCP.URL == "" {
		return ""
	}
	if r.executionMode() == "local" || r.mcpRelay == nil {
		return r.cfg.ChetterMCP.URL
	}
	_, port, err := net.SplitHostPort(r.mcpRelay.Addr())
	if err != nil || port == "" {
		slog.Error("invalid Chetter MCP relay address", "addr", r.mcpRelay.Addr(), "err", err)
		return ""
	}
	return "http://" + hostIP(runcNetwork()) + ":" + port + "/mcp"
}

func (r *Runner) taskChetterMCPToken() string {
	if r.executionMode() == "local" || r.mcpRelay == nil {
		return r.cfg.ChetterMCP.AuthToken
	}
	return ""
}

func hostWorkspaceDir(containerPath string) string {
	if hostRoot := os.Getenv("HOST_WORKSPACE_ROOT"); hostRoot != "" {
		if after, found := strings.CutPrefix(containerPath, "/var/lib/chetter-runner"); found {
			return hostRoot + after
		}
	}
	return containerPath
}

func appendRunnerOwnedEnv(env []string) []string {
	for _, key := range runnerOwnedEnvKeys() {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func addRunnerOwnedEnv(env map[string]string) {
	for _, key := range runnerOwnedEnvKeys() {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
}

const gitAskpassFilename = ".chetter-git-askpass"

func prepareGitWorkspace(ctx context.Context, workspace string, req task.TaskRequest) error {
	if req.GitAuthorName == "" || req.GitAuthorEmail == "" {
		return fmt.Errorf("task has no resolved Git identity")
	}
	if err := writeGitAskpass(workspace); err != nil {
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

func gitIdentityEnv(req task.TaskRequest, workspace string) []string {
	env := []string{
		"GIT_AUTHOR_NAME=" + req.GitAuthorName,
		"GIT_AUTHOR_EMAIL=" + req.GitAuthorEmail,
		"GIT_COMMITTER_NAME=" + req.GitAuthorName,
		"GIT_COMMITTER_EMAIL=" + req.GitAuthorEmail,
	}
	return append(env, gitCredentialEnv(workspace)...)
}

func writeGitAskpass(workspace string) error {
	if err := os.WriteFile(filepath.Join(workspace, gitAskpassFilename), []byte("#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' x-access-token ;;\n  *) printf '%s\\n' \"$GITHUB_TOKEN\" ;;\nesac\n"), 0700); err != nil {
		return fmt.Errorf("write Git askpass helper: %w", err)
	}
	return nil
}

func gitCloneCredentialDir(workspace string) string {
	return filepath.Dir(workspace)
}

func gitCredentialEnv(workspace string) []string {
	if os.Getenv("GITHUB_TOKEN") == "" {
		return nil
	}
	return []string{"GIT_ASKPASS=" + filepath.Join(workspace, gitAskpassFilename), "GIT_TERMINAL_PROMPT=0"}
}

func providerCredentialEnv(req task.TaskRequest) []string {
	key := strings.TrimSpace(req.ProviderAPIKeyEnv)
	if key == "" {
		return nil
	}
	if value := os.Getenv(key); value != "" {
		return []string{key + "=" + value}
	}
	return nil
}

func isManagedEnv(key string, req task.TaskRequest) bool {
	if isRunnerOwnedEnv(key) {
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

func validateEndpointTokenEnvironment(endpoints []task.MCPEndpoint) error {
	for _, key := range endpointTokenEnvKeys(endpoints) {
		if isHarnessControlEnv(key) {
			return fmt.Errorf("MCP endpoint bearer_token_env %s conflicts with a reserved harness environment variable", key)
		}
		if value, ok := os.LookupEnv(key); !ok || value == "" {
			return fmt.Errorf("runner environment variable %s is required", key)
		}
	}
	return nil
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

func isHarnessControlEnv(key string) bool {
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

func appendDockerManagedEnvironment(args []string, req task.TaskRequest) []string {
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
	if _, isEndpointToken := selected[providerKey]; providerKey != "" && !isEndpointToken && !isRunnerOwnedEnv(providerKey) {
		if value := os.Getenv(providerKey); value != "" {
			args = append(args, "-e", providerKey+"="+value)
		}
	}
	for _, key := range endpointKeys {
		args = append(args, "-e", key)
	}
	return args
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

func isRunnerOwnedEnv(key string) bool {
	switch key {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_SERVE_PROXY_TOKEN", "CODEWHALE_CONFIG_DIR", "CODEWHALE_RUNTIME_TOKEN", "CHETTER_TASK_ID", "CHETTER_AGENT_SESSION_ID", "CHETTER_USER_PROMPT_ID", "CHETTER_EXECUTION_ID", "GITHUB_TOKEN", "MEM9_API_KEY", "MEM9_API_URL", "MEM9_DEBUG", "MEM9_HOME", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "OPENCODE_API_KEY", "SYNTHETIC_API_KEY", "ZAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "XAI_API_KEY":
		return true
	default:
		return false
	}
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
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

func injectPATIntoURL(raw, pat string) string {
	if !strings.HasPrefix(raw, "https://") || pat == "" {
		return raw
	}
	return "https://" + pat + "@" + raw[len("https://"):]
}

// runcNetwork returns the Docker network name for the runner container,
// used to attach gVisor agent containers to the same network.
func runcNetwork() string {
	out, _ := exec.Command("docker", "inspect", "-f", "{{range $k,$v := .NetworkSettings.Networks}}{{println $k}}{{end}}", os.Getenv("HOSTNAME")).CombinedOutput()
	if net := firstField(string(out)); net != "" {
		return net
	}
	return "bridge"
}

// hostIP returns the runner container's IP address on network.
func hostIP(network string) string {
	if ip := os.Getenv("RUNNER_HOST_IP"); ip != "" {
		return ip
	}
	if network != "" {
		format := fmt.Sprintf("{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}", network)
		out, _ := exec.Command("docker", "inspect", "-f", format, os.Getenv("HOSTNAME")).CombinedOutput()
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip
		}
	}
	out, _ := exec.Command("hostname", "-i").CombinedOutput()
	if ip := firstField(string(out)); ip != "" {
		return ip
	}
	return "127.0.0.1"
}

// dockerGatewayIP returns the Docker network gateway address. Runner containers
// can use it to reach ports published on the Docker host.
func dockerGatewayIP(network string) string {
	if ip := os.Getenv("RUNNER_DOCKER_GATEWAY_IP"); ip != "" {
		return ip
	}
	if network != "" {
		format := fmt.Sprintf("{{with index .NetworkSettings.Networks %q}}{{.Gateway}}{{end}}", network)
		out, _ := exec.Command("docker", "inspect", "-f", format, os.Getenv("HOSTNAME")).CombinedOutput()
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip
		}
	}
	return "172.17.0.1"
}

func harnessBaseURL(bindAddr string, hostPort int, gvisor bool, network string) string {
	if gvisor {
		return fmt.Sprintf("http://%s:%d", dockerGatewayIP(network), hostPort)
	}
	connectAddr := bindAddr
	if connectAddr == "" || connectAddr == "0.0.0.0" || connectAddr == "::" {
		connectAddr = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", connectAddr, hostPort)
}

func harnessPublishBindAddr(bindAddr string, gvisor bool) string {
	if !gvisor {
		return bindAddr
	}
	if addr := os.Getenv("RUNNER_PUBLISH_BIND_ADDR"); addr != "" {
		return addr
	}
	return "0.0.0.0"
}

func firstField(s string) string {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func gvisorHostAliases() []string {
	domains := []string{
		"opencode.ai",
		"api.deepseek.com",
		"api.openai.com",
		"api.anthropic.com",
		"api.synthetic.new",
	}
	aliases := make([]string, 0, len(domains)*2)
	for _, domain := range domains {
		ips, err := net.LookupIP(domain)
		if err != nil {
			slog.Warn("resolve gvisor host alias", "host", domain, "err", err)
			continue
		}
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				aliases = append(aliases, "--add-host", domain+":"+v4.String())
				break
			}
		}
	}
	return aliases
}

func (r *Runner) runLocalAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.ServeHarness) {
	env := os.Environ()
	for k, v := range req.Env {
		if isManagedEnv(k, req) {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = appendRunnerOwnedEnv(env)
	env = append(env, gitIdentityEnv(req, session.WorkspaceDir)...)
	env = append(env,
		"CHETTER_AGENT_NAME="+req.Agent,
		"CHETTER_MODEL_ID="+h.ResolvedModelID(req),
		"CHETTER_TASK_ID="+req.TaskID,
		"CHETTER_AGENT_SESSION_ID="+req.AgentSessionID,
		"CHETTER_USER_PROMPT_ID="+req.UserPromptID,
		"CHETTER_EXECUTION_ID="+req.ExecutionID,
		"CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)

	secret := h.ServerPassword()
	env = append(env,
		"TASK_ID="+req.TaskID,
		"WORKSPACE="+session.WorkspaceDir,
	)
	for k, v := range h.Env(session.WorkspaceDir, secret, req) {
		env = append(env, k+"="+v)
	}
	env = append(env, providerCredentialEnv(req)...)
	env = append(env, "HOME="+session.WorkspaceDir)

	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("allocate port: %v", err), nil)
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	serveCmdParts := h.ServeCommand(port)
	if len(serveCmdParts) == 0 {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness %s does not support serve mode", h.Name()), nil)
		return
	}
	serveCmd := exec.CommandContext(ctx, serveCmdParts[0], serveCmdParts[1:]...)
	configureProcess(serveCmd)
	serveCmd.Dir = session.WorkspaceDir
	serveCmd.Env = env
	stdout, err := serveCmd.StdoutPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("%s stdout pipe: %v", h.Name(), err), nil)
		return
	}
	stderr, err := serveCmd.StderrPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("%s stderr pipe: %v", h.Name(), err), nil)
		return
	}

	if err := serveCmd.Start(); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("start %s serve: %v", h.Name(), err), nil)
		return
	}
	go h.PipeOutput(req.TaskID, "stdout", stdout)
	go h.PipeOutput(req.TaskID, "stderr", stderr)

	defer func() {
		if serveCmd.Process != nil {
			_ = terminateProcess(serveCmd)
			serveCmd.Wait()
		}
	}()

	if err := h.WaitForReady(ctx, baseURL, secret, 120*time.Second); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness serve not ready: %v", err), nil)
		return
	}
	slog.Info("harness serve ready", "taskID", req.TaskID, "url", baseURL)

	sid, err := h.CreateSession(ctx, baseURL, secret)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("create session: %v", err), nil)
		return
	}
	slog.Info("session", "taskID", req.TaskID, "sessionID", sid)

	var tokenUsage tokenUsageAccumulator
	agentCtx, stopWatching, watchdog := r.watchHarnessProgress(ctx, h, req, baseURL, sid, secret, session.WorkspaceDir, tokenUsage.add)
	defer stopWatching()

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	summary, err := h.SendPrompt(agentCtx, baseURL, sid, secret, req, session.WorkspaceDir, taskPromptTimeout(req.TimeoutSec))
	stopWatching()
	if watchdog.isStuck() {
		err = fmt.Errorf("stuck harness: no progress")
	}
	var sessionExport string
	if sid != "" {
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
	}
	if err != nil {
		status, message := "error", fmt.Sprintf("prompt failed: %v", err)
		if ctx.Err() != nil && !watchdog.isStuck() {
			status, message = cancellationStatus(ctx, h.Name())
		}
		r.publishStatusWithMetadata(req, status, message, nil, sid, sessionExport, tokenUsage.snapshot())
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed (local)", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	slog.Info("agent completed", "taskID", req.TaskID)
	r.publishStatusWithMetadata(req, "done", truncateSummary(summary), nil, sid, sessionExport, tokenUsage.snapshot())
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (local)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) runDockerAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.ServeHarness) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	bindAddr := os.Getenv("RUNNER_BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}

	ln, err := net.Listen("tcp", bindAddr+":0")
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("allocate port: %v", err), nil)
		return
	}
	hostPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	const containerPort = 9999
	containerName := containerNameForRequest(req)

	removeTaskContainer(containerName)

	secret := h.ServerPassword()

	gvisor := r.cfg.Execution.UseGVisor
	netName := runcNetwork()
	runnerIP := ""
	serveCmd := h.ServeCommand(containerPort)
	if len(serveCmd) == 0 {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness %s does not support serve mode", h.Name()), nil)
		return
	}
	entrypoint := serveCmd[0]
	serveArgs := serveCmd[1:]

	dockerArgs := []string{
		"run", "-d",
		"--entrypoint", entrypoint,
		"--name", containerName,
		"--label", "chetter.runner_id=" + r.runnerID,
		"--label", "chetter.task_id=" + req.TaskID,
		"--label", "chetter.execution_id=" + executionKey(req),
		"--label", "chetter.agent_session_id=" + req.AgentSessionID,
		"--label", "chetter.user_prompt_id=" + req.UserPromptID,
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "--runtime", "runsc")
		runnerIP = hostIP(netName)
		dockerArgs = append(dockerArgs, "--dns", runnerIP)
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
	}
	if mem := r.cfg.Execution.ContainerMemory; mem != "" {
		dockerArgs = append(dockerArgs, "--memory", mem, "--memory-swap", mem)
	}
	// Put the dev container on the same network as the runner so it can
	// reach the runner's MCP server directly (non-gVisor) or via the
	// HTTP proxy (gVisor).
	dockerArgs = append(dockerArgs, "--network", netName)
	dockerArgs = append(dockerArgs, "-p", fmt.Sprintf("%s:%d:%d", harnessPublishBindAddr(bindAddr, gvisor), hostPort, containerPort))
	dockerArgs = append(dockerArgs,
		"-v", hostWorkspaceDir(session.WorkspaceDir)+":/workspace",
		"-w", "/workspace",
		"-e", "TASK_ID="+req.TaskID,
		"-e", "WORKSPACE=/workspace",
		"-e", "XDG_CONFIG_HOME=/workspace/.config",
		"-e", "XDG_DATA_HOME=/workspace/.local/share",
		"-e", "XDG_STATE_HOME=/workspace/.local/state",
		"-e", "XDG_CACHE_HOME=/workspace/.cache",
		"-e", "CHETTER_AGENT_NAME="+req.Agent,
		"-e", "CHETTER_MODEL_ID="+h.ResolvedModelID(req),
		"-e", "CHETTER_TASK_ID="+req.TaskID,
		"-e", "CHETTER_AGENT_SESSION_ID="+req.AgentSessionID,
		"-e", "CHETTER_USER_PROMPT_ID="+req.UserPromptID,
		"-e", "CHETTER_EXECUTION_ID="+req.ExecutionID,
		"-e", "CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)
	for _, value := range gitIdentityEnv(req, containerWorkspaceDir) {
		dockerArgs = append(dockerArgs, "-e", value)
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	} else {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	}

	if gvisor {
		dockerArgs = append(dockerArgs,
			"-e", "HTTP_PROXY=http://"+runnerIP+":18080",
			"-e", "HTTPS_PROXY=http://"+runnerIP+":18080",
			"-e", "http_proxy=http://"+runnerIP+":18080",
			"-e", "https_proxy=http://"+runnerIP+":18080",
			"-e", "CHETTER_PROXY="+runnerIP+":18080",
			"-e", "NODE_USE_ENV_PROXY=1",
			"-e", "NO_PROXY="+gvisorNoProxy(),
			"-e", "no_proxy="+gvisorNoProxy(),
		)
	}

	for k, v := range h.Env("/workspace", secret, req) {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}

	for k, v := range req.Env {
		if isManagedEnv(k, req) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = appendDockerManagedEnvironment(dockerArgs, req)

	if gvisor {
		dockerArgs = append(dockerArgs, "--hostname", "0.0.0.0")
	}
	if shouldPullAgentImage(req.AgentImage) {
		dockerArgs = append(dockerArgs, "--pull=always")
	}
	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, serveArgs...)

	slog.Info("starting Docker container", "taskID", req.TaskID, "image", req.AgentImage, "hostPort", hostPort, "gvisor", r.cfg.Execution.UseGVisor)
	r.publishStatusForRequest(req, "running", "Starting dev container...", nil)

	out, err := exec.CommandContext(ctx, "docker", dockerArgs...).CombinedOutput()
	if err != nil {
		slog.Error("docker run failed", "taskID", req.TaskID, "err", err, "output", string(out))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("docker run: %v\n%s", err, string(out)), nil)
		return
	}

	defer func() {
		if session.PreserveWorkspace {
			slog.Info("preserving container for checkpointed session", "taskID", req.TaskID, "container", containerName)
			return
		}
		removeTaskContainer(containerName)
	}()

	baseURL := harnessBaseURL(bindAddr, hostPort, gvisor, netName)

	if err := h.WaitForReady(ctx, baseURL, secret, 120*time.Second); err != nil {
		logs, _ := exec.Command("docker", "logs", containerName).CombinedOutput()
		inspectOut, _ := exec.Command("docker", "inspect", "-f", "{{json .NetworkSettings.Networks}}", containerName).CombinedOutput()
		selfCheckOut, _ := exec.Command("docker", "exec", containerName, "sh", "-lc", "curl -sS -o /dev/null -w 'http_code=%{http_code}' -m 2 http://127.0.0.1:9999/config || true").CombinedOutput()
		slog.Error("harness serve not ready in container", "taskID", req.TaskID, "err", err, "baseURL", baseURL, "networks", strings.TrimSpace(string(inspectOut)), "selfCheck", strings.TrimSpace(string(selfCheckOut)), "logs", string(logs))
		r.publishEvent(req.TaskID, fmt.Sprintf("container networks: %s", truncateSummary(strings.TrimSpace(string(inspectOut)))))
		r.publishEvent(req.TaskID, fmt.Sprintf("container self-check: %s", truncateSummary(strings.TrimSpace(string(selfCheckOut)))))
		r.publishEvent(req.TaskID, fmt.Sprintf("container logs: %s", truncateSummary(string(logs))))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("container harness serve not ready: %v", err), nil)
		return
	}
	slog.Info("container harness serve ready", "taskID", req.TaskID, "url", baseURL)

	sid, err := h.CreateSession(ctx, baseURL, secret)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("create session: %v", err), nil)
		return
	}
	slog.Info("session created", "taskID", req.TaskID, "sessionID", sid)

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)

	var tokenUsage tokenUsageAccumulator
	agentCtx, stopWatching, watchdog := r.watchHarnessProgress(ctx, h, req, baseURL, sid, secret, session.WorkspaceDir, tokenUsage.add)
	defer stopWatching()

	summary, err := h.SendPrompt(agentCtx, baseURL, sid, secret, req, session.WorkspaceDir, taskPromptTimeout(req.TimeoutSec))
	stopWatching()
	if watchdog.isStuck() {
		err = fmt.Errorf("stuck harness: no progress")
	}
	var sessionExport string
	if err != nil {
		workspacePath := ""
		errorMessage := fmt.Sprintf("prompt failed: %v", err)
		status, statusMessage := "error", errorMessage
		if ctx.Err() != nil && !watchdog.isStuck() {
			status, statusMessage = cancellationStatus(ctx, h.Name())
		}
		errorCategory := classifyErrorCategory("error", errorMessage)
		if errorCategory == "transport_error" {
			r.publishDockerPromptFailureDiagnostics(req.TaskID, containerName, baseURL, err)
			dumpContainerLogs(req.TaskID, containerName, session.WorkspaceDir)
		}
		if req.CheckpointAfterSuccess && shouldPreserveWorkspaceOnPromptError(errorCategory) {
			workspacePath = session.WorkspaceDir
			session.PreserveWorkspace = true
			slog.Info("preserving workspace for recoverable prompt failure", "taskID", req.TaskID, "workspace", workspacePath, "error_category", errorCategory)
		}
		if sid != "" {
			slog.Info("aborting session before shutdown", "taskID", req.TaskID, "sessionID", sid)
			if abortErr := h.AbortSession(ctx, baseURL, sid, secret); abortErr != nil {
				slog.Warn("failed to abort session", "taskID", req.TaskID, "err", abortErr)
			}
			stopTaskContainer(containerName)
			sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
		}
		r.publishStatusWithMetadataAndCheckpoint(req, status, statusMessage, nil, sid, sessionExport, "", workspacePath, tokenUsage.snapshot())
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	slog.Info("agent completed", "taskID", req.TaskID)
	workspacePath := ""
	if req.CheckpointAfterSuccess {
		workspacePath = session.WorkspaceDir
		session.PreserveWorkspace = true
		slog.Info("preserving workspace for resumable session", "taskID", req.TaskID, "workspace", workspacePath)
	}
	if sid != "" {
		stopTaskContainer(containerName)
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
	}
	r.publishStatusWithMetadataAndCheckpoint(req, "done", truncateSummary(summary), nil, sid, sessionExport, "", workspacePath, tokenUsage.snapshot())
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (docker)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) runDockerAgentResume(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.ServeHarness) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	workspaceDir := req.ResumeWorkspacePath
	if workspaceDir == "" {
		r.publishStatusForRequest(req, "error", "no workspace path for resume", nil)
		return
	}
	session.WorkspaceDir = workspaceDir
	if err := prepareGitWorkspace(ctx, workspaceDir, req); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("configure Git identity: %v", err), nil)
		return
	}

	sid := req.ResumeHarnessSessionID
	if sid == "" {
		r.publishStatusForRequest(req, "error", "no harness session ID for resume", nil)
		return
	}

	r.publishStatusForRequest(req, "running", "Resuming agent session...", nil)

	mcpServer, err := r.startWorkspaceMCP(ctx, req.TaskID, req.ExecutionID)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("mcp server: %v", err), nil)
		return
	}
	defer mcpServer.Close()

	bindAddr := os.Getenv("RUNNER_BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}

	ln, err := net.Listen("tcp", bindAddr+":0")
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("allocate port: %v", err), nil)
		return
	}
	hostPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	const containerPort = 9999
	containerName := containerNameForRequest(req)
	removeTaskContainer(containerName)
	defer removeTaskContainer(containerName)

	secret := h.ServerPassword()
	gvisor := r.cfg.Execution.UseGVisor
	netName := runcNetwork()
	runnerIP := ""
	serveCmd := h.ServeCommand(containerPort)
	if len(serveCmd) == 0 {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness %s does not support serve mode", h.Name()), nil)
		return
	}
	entrypoint := serveCmd[0]
	serveArgs := serveCmd[1:]

	dockerArgs := []string{
		"run", "-d",
		"--entrypoint", entrypoint,
		"--name", containerName,
		"--label", "chetter.runner_id=" + r.runnerID,
		"--label", "chetter.task_id=" + req.TaskID,
		"--label", "chetter.execution_id=" + executionKey(req),
		"--label", "chetter.agent_session_id=" + req.AgentSessionID,
		"--label", "chetter.user_prompt_id=" + req.UserPromptID,
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "--runtime", "runsc")
		runnerIP = hostIP(netName)
		dockerArgs = append(dockerArgs, "--dns", runnerIP)
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
	}
	if mem := r.cfg.Execution.ContainerMemory; mem != "" {
		dockerArgs = append(dockerArgs, "--memory", mem, "--memory-swap", mem)
	}
	// Put the dev container on the same network as the runner so it can
	// reach the runner's MCP server directly (non-gVisor) or via the
	// HTTP proxy (gVisor).
	dockerArgs = append(dockerArgs, "--network", netName)
	dockerArgs = append(dockerArgs, "-p", fmt.Sprintf("%s:%d:%d", harnessPublishBindAddr(bindAddr, gvisor), hostPort, containerPort))
	dockerArgs = append(dockerArgs,
		"-v", hostWorkspaceDir(session.WorkspaceDir)+":/workspace",
		"-w", "/workspace",
		"-e", "TASK_ID="+req.TaskID,
		"-e", "WORKSPACE=/workspace",
		"-e", "XDG_CONFIG_HOME=/workspace/.config",
		"-e", "XDG_DATA_HOME=/workspace/.local/share",
		"-e", "XDG_STATE_HOME=/workspace/.local/state",
		"-e", "XDG_CACHE_HOME=/workspace/.cache",
		"-e", "CHETTER_AGENT_NAME="+req.Agent,
		"-e", "CHETTER_MODEL_ID="+h.ResolvedModelID(req),
		"-e", "CHETTER_TASK_ID="+req.TaskID,
		"-e", "CHETTER_AGENT_SESSION_ID="+req.AgentSessionID,
		"-e", "CHETTER_USER_PROMPT_ID="+req.UserPromptID,
		"-e", "CHETTER_EXECUTION_ID="+req.ExecutionID,
		"-e", "CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)
	for _, value := range gitIdentityEnv(req, containerWorkspaceDir) {
		dockerArgs = append(dockerArgs, "-e", value)
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	} else {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	}
	if gvisor {
		dockerArgs = append(dockerArgs,
			"-e", "HTTP_PROXY=http://"+runnerIP+":18080",
			"-e", "HTTPS_PROXY=http://"+runnerIP+":18080",
			"-e", "http_proxy=http://"+runnerIP+":18080",
			"-e", "https_proxy=http://"+runnerIP+":18080",
			"-e", "CHETTER_PROXY="+runnerIP+":18080",
			"-e", "NODE_USE_ENV_PROXY=1",
			"-e", "NO_PROXY="+gvisorNoProxy(),
			"-e", "no_proxy="+gvisorNoProxy(),
		)
	}
	for k, v := range h.Env("/workspace", secret, req) {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	for k, v := range req.Env {
		if isManagedEnv(k, req) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = appendDockerManagedEnvironment(dockerArgs, req)

	if gvisor {
		dockerArgs = append(dockerArgs, "--hostname", "0.0.0.0")
	}
	if shouldPullAgentImage(req.AgentImage) {
		dockerArgs = append(dockerArgs, "--pull=always")
	}
	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, serveArgs...)

	slog.Info("starting resume Docker container", "taskID", req.TaskID, "image", req.AgentImage, "hostPort", hostPort, "workspace", workspaceDir)
	r.publishStatusForRequest(req, "running", "Starting dev container for resume...", nil)

	out, err := exec.CommandContext(ctx, "docker", dockerArgs...).CombinedOutput()
	if err != nil {
		slog.Error("docker run failed on resume", "taskID", req.TaskID, "err", err, "output", string(out))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("docker run: %v\n%s", err, string(out)), nil)
		return
	}

	baseURL := harnessBaseURL(bindAddr, hostPort, gvisor, netName)
	if err := h.WaitForReady(ctx, baseURL, secret, 120*time.Second); err != nil {
		logs, _ := exec.Command("docker", "logs", containerName).CombinedOutput()
		r.publishStatusForRequest(req, "error", fmt.Sprintf("container serve not ready: %v\n%s", err, string(logs)), nil)
		return
	}
	slog.Info("container harness serve ready for resume", "taskID", req.TaskID, "url", baseURL)

	r.publishStatusForRequest(req, "running", "Sending follow-up prompt to agent...", nil)

	var tokenUsage tokenUsageAccumulator
	agentCtx, stopWatching, watchdog := r.watchHarnessProgress(ctx, h, req, baseURL, sid, secret, workspaceDir, tokenUsage.add)
	defer stopWatching()

	summary, err := h.SendPrompt(agentCtx, baseURL, sid, secret, req, workspaceDir, taskPromptTimeout(req.TimeoutSec))
	stopWatching()
	if watchdog.isStuck() {
		err = fmt.Errorf("stuck harness: no progress")
	}
	var sessionExport string
	if err != nil {
		workspacePath := ""
		errorMessage := fmt.Sprintf("prompt failed: %v", err)
		status, statusMessage := "error", errorMessage
		if ctx.Err() != nil && !watchdog.isStuck() {
			status, statusMessage = cancellationStatus(ctx, h.Name())
		}
		errorCategory := classifyErrorCategory("error", errorMessage)
		if errorCategory == "transport_error" {
			r.publishDockerPromptFailureDiagnostics(req.TaskID, containerName, baseURL, err)
			dumpContainerLogs(req.TaskID, containerName, workspaceDir)
		}
		if req.CheckpointAfterSuccess && shouldPreserveWorkspaceOnPromptError(errorCategory) {
			workspacePath = session.WorkspaceDir
			session.PreserveWorkspace = true
			slog.Info("preserving workspace for recoverable prompt failure", "taskID", req.TaskID, "workspace", workspacePath, "error_category", errorCategory)
		}
		if sid != "" {
			slog.Info("aborting session before shutdown", "taskID", req.TaskID, "sessionID", sid)
			if abortErr := h.AbortSession(ctx, baseURL, sid, secret); abortErr != nil {
				slog.Warn("failed to abort session", "taskID", req.TaskID, "err", abortErr)
			}
			stopTaskContainer(containerName)
			sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
		}
		r.publishStatusWithMetadataAndCheckpoint(req, status, statusMessage, nil, sid, sessionExport, "", workspacePath, tokenUsage.snapshot())
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed on resume", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	workspacePath := ""
	if req.CheckpointAfterSuccess {
		workspacePath = session.WorkspaceDir
		session.PreserveWorkspace = true
		slog.Info("preserving workspace for resumable session", "taskID", req.TaskID, "workspace", workspacePath)
	}
	if sid != "" {
		stopTaskContainer(containerName)
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
	}
	slog.Info("agent completed on resume", "taskID", req.TaskID)
	r.publishStatusWithMetadataAndCheckpoint(req, "done", truncateSummary(summary), nil, sid, sessionExport, "", workspacePath, tokenUsage.snapshot())
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (docker resume)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) publishStatusWithMetadataAndCheckpoint(req task.TaskRequest, status, message string, artifacts []string, sessionID, sessionExport, checkpointPath, workspacePath string, tokenUsage task.TokenUsage) {
	resp := task.TaskResponse{
		TaskID:         req.TaskID,
		Status:         status,
		Artifacts:      artifacts,
		SessionExport:  sessionExport,
		CheckpointPath: checkpointPath,
		WorkspacePath:  workspacePath,
		TokenUsage:     tokenUsage,
	}
	r.decorateTaskResponseForRequest(&resp, req, sessionID)
	if isTerminalStatus(status) {
		resp.EndedAt = time.Now()
	}
	if status == "error" || status == "cancelled" {
		resp.Error = message
		if resp.ErrorCategory == "" {
			resp.ErrorCategory = classifyErrorCategory(status, message)
		}
	} else {
		resp.Summary = message
	}
	r.publishTaskResponse(resp)
}

func (r *Runner) readSessionExport(taskID, wsDir, sid string, h harness.ServeHarness) string {
	type result struct {
		export string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		export, err := h.ReadSessionExport(wsDir, sid)
		done <- result{export: export, err: err}
	}()
	select {
	case result := <-done:
		if result.err == nil {
			return result.export
		}
		slog.Warn("session export failed", "taskID", taskID, "err", result.err)
		r.publishEvent(taskID, fmt.Sprintf("session export: %v", result.err))
	case <-time.After(sessionExportTimeout):
		slog.Warn("session export timed out", "taskID", taskID)
		r.publishEvent(taskID, "session export timed out")
	}
	return ""
}

func stopTaskContainer(containerName string) {
	ctx, cancel := context.WithTimeout(context.Background(), containerCleanupTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "stop", containerName).CombinedOutput(); err != nil {
		slog.Warn("failed to stop task container", "container", containerName, "err", err, "output", strings.TrimSpace(string(out)))
	}
}

func removeTaskContainer(containerName string) {
	ctx, cancel := context.WithTimeout(context.Background(), containerCleanupTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", containerName).CombinedOutput(); err != nil && ctx.Err() != nil {
		slog.Warn("timed out removing task container", "container", containerName, "err", err, "output", strings.TrimSpace(string(out)))
	}
}

func shouldPreserveWorkspaceOnPromptError(errorCategory string) bool {
	return errorCategory == "timeout" || errorCategory == "transport_error"
}

func dumpContainerLogs(taskID, containerName, workspaceDir string) {
	logPath := filepath.Join(workspaceDir, "docker-container.log")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "logs", "--tail", "500", "--timestamps", containerName).CombinedOutput()
	if err != nil {
		slog.Warn("failed to dump container logs", "taskID", taskID, "container", containerName, "err", err)
		if len(out) > 0 {
			os.WriteFile(logPath, out, 0644)
		}
		return
	}
	if len(out) == 0 {
		return
	}
	if err := os.WriteFile(logPath, out, 0644); err != nil {
		slog.Warn("failed to write container logs", "taskID", taskID, "path", logPath, "err", err)
		return
	}
	slog.Info("dumped container logs", "taskID", taskID, "path", logPath, "bytes", len(out))
}

func (r *Runner) publishDockerPromptFailureDiagnostics(taskID, containerName, baseURL string, promptErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	state := runDiagnosticCommand(ctx, "docker", "inspect", "-f", "status={{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} error={{.State.Error}} started={{.State.StartedAt}} finished={{.State.FinishedAt}}", containerName)
	health := probeHTTP(ctx, baseURL+"/config")
	logs := runDiagnosticCommand(ctx, "docker", "logs", "--tail", "200", containerName)

	slog.Warn("opencode prompt transport failure", "taskID", taskID, "err", promptErr, "container", containerName, "container_state", state, "http_probe", health, "logs_tail", logs)
	r.publishEvent(taskID, fmt.Sprintf("opencode prompt transport failure: %v", promptErr))
	r.publishEvent(taskID, fmt.Sprintf("opencode container state: %s", truncateSummary(state)))
	r.publishEvent(taskID, fmt.Sprintf("opencode /config probe: %s", truncateSummary(health)))
	r.publishEvent(taskID, fmt.Sprintf("opencode container logs tail: %s", truncateSummary(logs)))
}

func runDiagnosticCommand(ctx context.Context, name string, args ...string) string {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			return err.Error()
		}
		return fmt.Sprintf("%s: %s", err, text)
	}
	if text == "" {
		return "ok"
	}
	return text
}

func probeHTTP(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err.Error()
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Sprintf("status=%d", resp.StatusCode)
	}
	return fmt.Sprintf("status=%d body=%s", resp.StatusCode, text)
}

func (r *Runner) publishStatusWithMetadata(req task.TaskRequest, status, message string, artifacts []string, sessionID, sessionExport string, tokenUsage task.TokenUsage) {
	resp := task.TaskResponse{
		TaskID:        req.TaskID,
		Status:        status,
		Artifacts:     artifacts,
		SessionExport: sessionExport,
		TokenUsage:    tokenUsage,
	}
	r.decorateTaskResponseForRequest(&resp, req, sessionID)
	if isTerminalStatus(status) {
		resp.EndedAt = time.Now()
	}
	if status == "error" || status == "cancelled" {
		resp.Error = message
		if resp.ErrorCategory == "" {
			resp.ErrorCategory = classifyErrorCategory(status, message)
		}
	} else {
		resp.Summary = message
	}
	r.publishTaskResponse(resp)
}

func taskPromptTimeout(timeoutSec int) time.Duration {
	if timeoutSec <= 0 {
		timeoutSec = 3600
	}
	return time.Duration(timeoutSec) * time.Second
}

type rpcAgentState struct {
	summary         strings.Builder
	lastDetail      string
	lastPublished   time.Time
	sessionID       string
	terminal        bool
	errorMessage    string
	finalAssistant  bool
	retrying        bool
	activeTools     map[string]struct{}
	activeToolCount int
}

func (s *rpcAgentState) completeOnEOF() bool {
	return s.finalAssistant && !s.retrying && s.activeToolCount == 0 && s.errorMessage == ""
}

func (r *Runner) runRpcAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.RPCHarness) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	args := h.RpcCommand(req)
	if len(args) == 0 {
		r.publishStatusForRequest(req, "error", "harness does not provide an RPC command", nil)
		return
	}

	name := h.Name()
	slog.Info("starting RPC harness", "taskID", req.TaskID, "harness", name, "args", args)
	r.publishStatusForRequest(req, "running", "Starting agent (RPC mode)...", nil)

	cmd := exec.Command(args[0], args[1:]...)
	configureProcess(cmd)
	cmd.Dir = session.WorkspaceDir
	cmd.Env = r.agentEnv(req, session.WorkspaceDir, "", h)
	r.runRPCAgentCommand(ctx, session, req, h, cmd)
}

func (r *Runner) runDockerRpcAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.RPCHarness) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	args := h.RpcCommand(req)
	if len(args) == 0 {
		r.publishStatusForRequest(req, "error", "harness does not provide an RPC command", nil)
		return
	}

	containerName := containerNameForRequest(req)
	removeTaskContainer(containerName)
	defer removeTaskContainer(containerName)

	gvisor := r.cfg.Execution.UseGVisor
	netName := ""
	runnerIP := ""
	if gvisor {
		netName = runcNetwork()
		runnerIP = hostIP(netName)
	}

	dockerArgs := dockerRPCArgs(req, r.runnerID, session.WorkspaceDir, containerName, h, args, gvisor, netName, runnerIP)
	name := h.Name()
	slog.Info("starting Docker RPC harness", "taskID", req.TaskID, "harness", name, "image", req.AgentImage, "args", args, "gvisor", gvisor)
	r.publishStatusForRequest(req, "running", "Starting dev container (RPC mode)...", nil)

	cmd := exec.Command("docker", dockerArgs...)
	configureProcess(cmd)
	r.runRPCAgentCommand(ctx, session, req, h, cmd)
}

func dockerRPCArgs(req task.TaskRequest, runnerID, wsDir, containerName string, h harness.RPCHarness, command []string, gvisor bool, netName, runnerIP string) []string {
	dockerArgs := []string{
		"run", "--rm", "-i",
		"--entrypoint", command[0],
		"--name", containerName,
		"--label", "chetter.runner_id=" + runnerID,
		"--label", "chetter.task_id=" + req.TaskID,
		"--label", "chetter.execution_id=" + executionKey(req),
		"--label", "chetter.agent_session_id=" + req.AgentSessionID,
		"--label", "chetter.user_prompt_id=" + req.UserPromptID,
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "--runtime", "runsc")
		dockerArgs = append(dockerArgs, "--network", netName)
		dockerArgs = append(dockerArgs, "--dns", runnerIP)
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
	}
	dockerArgs = append(dockerArgs,
		"-v", hostWorkspaceDir(wsDir)+":"+containerWorkspaceDir,
		"-w", containerWorkspaceDir,
		"-e", "TASK_ID="+req.TaskID,
		"-e", "WORKSPACE="+containerWorkspaceDir,
		"-e", "XDG_CONFIG_HOME="+containerWorkspaceDir+"/.config",
		"-e", "XDG_DATA_HOME="+containerWorkspaceDir+"/.local/share",
		"-e", "XDG_STATE_HOME="+containerWorkspaceDir+"/.local/state",
		"-e", "XDG_CACHE_HOME="+containerWorkspaceDir+"/.cache",
		"-e", "CHETTER_AGENT_NAME="+req.Agent,
		"-e", "CHETTER_MODEL_ID="+h.ResolvedModelID(req),
		"-e", "CHETTER_TASK_ID="+req.TaskID,
		"-e", "CHETTER_AGENT_SESSION_ID="+req.AgentSessionID,
		"-e", "CHETTER_USER_PROMPT_ID="+req.UserPromptID,
		"-e", "CHETTER_EXECUTION_ID="+req.ExecutionID,
		"-e", "CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)
	for _, value := range gitIdentityEnv(req, containerWorkspaceDir) {
		dockerArgs = append(dockerArgs, "-e", value)
	}
	if gvisor {
		dockerArgs = append(dockerArgs,
			"-e", "HOME="+containerWorkspaceDir,
			"-e", "HTTP_PROXY=http://"+runnerIP+":18080",
			"-e", "HTTPS_PROXY=http://"+runnerIP+":18080",
			"-e", "http_proxy=http://"+runnerIP+":18080",
			"-e", "https_proxy=http://"+runnerIP+":18080",
			"-e", "CHETTER_PROXY="+runnerIP+":18080",
			"-e", "NODE_USE_ENV_PROXY=1",
			"-e", "NO_PROXY="+gvisorNoProxy(),
			"-e", "no_proxy="+gvisorNoProxy(),
		)
	} else {
		dockerArgs = append(dockerArgs, "-e", "HOME=/opt/opencode")
	}

	for k, v := range h.Env(containerWorkspaceDir, "", req) {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	for k, v := range req.Env {
		if isManagedEnv(k, req) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = appendDockerManagedEnvironment(dockerArgs, req)

	if shouldPullAgentImage(req.AgentImage) {
		dockerArgs = append(dockerArgs, "--pull=always")
	}
	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, command[1:]...)
	return dockerArgs
}

func shouldPullAgentImage(image string) bool {
	return strings.HasPrefix(image, "ghcr.io/")
}

func (r *Runner) runRPCAgentCommand(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.RPCHarness, cmd *exec.Cmd) {
	name := h.Name()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("stdin pipe: %v", err), nil)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("stdout pipe: %v", err), nil)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("stderr pipe: %v", err), nil)
		return
	}

	if err := cmd.Start(); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("start %s: %v", name, err), nil)
		return
	}
	go h.PipeOutput(req.TaskID, "stderr", stderr)

	exited := false
	defer func() {
		if !exited && cmd.Process != nil {
			_ = terminateProcess(cmd)
			_ = cmd.Wait()
		}
	}()

	lineCtx, stopLines := context.WithCancel(context.Background())
	defer stopLines()
	lines := streamRPCLines(lineCtx, stdout)
	state := &rpcAgentState{lastPublished: time.Now(), activeTools: make(map[string]struct{})}

	readyCmd := map[string]any{"id": "ready", "type": "get_state"}
	if err := writeRPCCommand(stdin, readyCmd); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("write ready probe: %v", err), nil)
		return
	}
	readyResp, err := r.waitForRPCResponse(ctx, req, lines, stdin, "ready", state)
	if err != nil {
		if ctx.Err() != nil {
			status, message := cancellationStatus(ctx, name)
			r.publishStatusForRequest(req, status, message, nil)
			return
		}
		r.publishStatusForRequest(req, "error", fmt.Sprintf("%s ready: %v", name, err), nil)
		return
	}
	state.sessionID = rpcSessionID(readyResp)

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	promptCmd := map[string]any{"id": "prompt", "type": "prompt", "message": rpcPrompt(req)}
	if err := writeRPCCommand(stdin, promptCmd); err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("write prompt: %v", err), nil, state.sessionID, "", task.TokenUsage{})
		return
	}
	if _, err := r.waitForRPCResponse(ctx, req, lines, stdin, "prompt", state); err != nil {
		if ctx.Err() != nil {
			sessionExport := r.cleanupRPCSession(req, session.WorkspaceDir, stdin, lines, state)
			status, message := cancellationStatus(ctx, name)
			r.publishStatusWithMetadata(req, status, message, nil, state.sessionID, sessionExport, task.TokenUsage{})
			return
		}
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("%s prompt: %v", name, err), nil, state.sessionID, "", task.TokenUsage{})
		return
	}

	for !state.terminal {
		line, err := readRPCLine(ctx, lines)
		if err != nil {
			if ctx.Err() != nil {
				sessionExport := r.cleanupRPCSession(req, session.WorkspaceDir, stdin, lines, state)
				status, message := cancellationStatus(ctx, name)
				r.publishStatusWithMetadata(req, status, message, nil, state.sessionID, sessionExport, task.TokenUsage{})
				return
			}
			if errors.Is(err, io.EOF) && state.completeOnEOF() {
				state.terminal = true
				break
			}
			r.publishStatusWithMetadata(req, "error", fmt.Sprintf("%s output: %v", name, err), nil, state.sessionID, "", task.TokenUsage{})
			return
		}
		if err := r.handleRPCLine(req, stdin, line, state); err != nil {
			r.publishStatusWithMetadata(req, "error", fmt.Sprintf("%s event: %v", name, err), nil, state.sessionID, "", task.TokenUsage{})
			return
		}
	}

	resultText := strings.TrimSpace(state.summary.String())
	resultCmd := map[string]any{"id": "result", "type": "get_last_assistant_text"}
	if err := writeRPCCommand(stdin, resultCmd); err == nil {
		if resp, err := r.waitForRPCResponse(ctx, req, lines, stdin, "result", state); err == nil {
			if text := rpcLastAssistantText(resp); text != "" {
				resultText = text
			}
		} else {
			r.publishEvent(req.TaskID, fmt.Sprintf("%s result: %v", name, err))
		}
	} else {
		r.publishEvent(req.TaskID, fmt.Sprintf("%s result write: %v", name, err))
	}

	var sessionExport string
	messagesCmd := map[string]any{"id": "messages", "type": "get_messages"}
	if err := writeRPCCommand(stdin, messagesCmd); err == nil {
		if resp, err := r.waitForRPCResponse(ctx, req, lines, stdin, "messages", state); err == nil {
			sessionExport = renderRPCMessages(resp)
			if err := writeRPCSessionExport(session.WorkspaceDir, sessionExport); err != nil {
				slog.Warn("pi session export write failed", "taskID", req.TaskID, "err", err)
				r.publishEvent(req.TaskID, fmt.Sprintf("session export: %v", err))
			}
		} else {
			r.publishEvent(req.TaskID, fmt.Sprintf("%s messages: %v", name, err))
		}
	} else {
		r.publishEvent(req.TaskID, fmt.Sprintf("%s messages write: %v", name, err))
	}
	if ctx.Err() != nil {
		sessionExport = r.cleanupRPCSession(req, session.WorkspaceDir, stdin, lines, state)
		status, message := cancellationStatus(ctx, name)
		r.publishStatusWithMetadata(req, status, message, nil, state.sessionID, sessionExport, task.TokenUsage{})
		return
	}

	_ = stdin.Close()
	waitErr := waitForRPCProcess(ctx, cmd)
	exited = true
	if ctx.Err() != nil {
		status, message := cancellationStatus(ctx, name)
		r.publishStatusWithMetadata(req, status, message, nil, state.sessionID, sessionExport, task.TokenUsage{})
		return
	}
	if waitErr != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("%s: %v", name, waitErr), nil, state.sessionID, sessionExport, task.TokenUsage{})
		return
	}

	if state.errorMessage != "" {
		r.publishStatusWithMetadata(req, "error", state.errorMessage, nil, state.sessionID, sessionExport, task.TokenUsage{})
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s failed", req.TaskID), "failed", state.errorMessage, time.Since(session.StartedAt).Milliseconds())
		return
	}
	if resultText == "" {
		resultText = "Pi completed without assistant text."
	}
	slog.Info("RPC agent completed", "taskID", req.TaskID)
	r.publishStatusWithMetadata(req, "done", truncateSummary(resultText), nil, state.sessionID, sessionExport, task.TokenUsage{})
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (rpc)", req.TaskID), "success", truncateSummary(resultText), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) agentEnv(req task.TaskRequest, wsDir, secret string, h harness.Harness) []string {
	env := os.Environ()
	for k, v := range req.Env {
		if isManagedEnv(k, req) {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = appendRunnerOwnedEnv(env)
	env = append(env, gitIdentityEnv(req, wsDir)...)
	env = append(env,
		"CHETTER_AGENT_NAME="+req.Agent,
		"CHETTER_MODEL_ID="+h.ResolvedModelID(req),
		"CHETTER_TASK_ID="+req.TaskID,
		"CHETTER_AGENT_SESSION_ID="+req.AgentSessionID,
		"CHETTER_USER_PROMPT_ID="+req.UserPromptID,
		"CHETTER_EXECUTION_ID="+req.ExecutionID,
		"CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
		"TASK_ID="+req.TaskID,
		"WORKSPACE="+wsDir,
		"HOME="+wsDir,
	)
	for k, v := range h.Env(wsDir, secret, req) {
		env = append(env, k+"="+v)
	}
	env = append(env, providerCredentialEnv(req)...)
	return env
}

func writeRPCCommand(w io.Writer, cmd map[string]any) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

type rpcLine struct {
	data []byte
	err  error
}

func streamRPCLines(ctx context.Context, reader io.Reader) <-chan rpcLine {
	lines := make(chan rpcLine)
	go func() {
		defer close(lines)
		send := func(line rpcLine) bool {
			select {
			case lines <- line:
				return true
			case <-ctx.Done():
				return false
			}
		}
		br := bufio.NewReader(reader)
		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 {
				if !send(rpcLine{data: []byte(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))}) {
					return
				}
			}
			if err != nil {
				send(rpcLine{err: err})
				return
			}
		}
	}()
	return lines
}

func readRPCLine(ctx context.Context, lines <-chan rpcLine) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case line, ok := <-lines:
		if !ok {
			return nil, io.EOF
		}
		return line.data, line.err
	}
}

func (r *Runner) waitForRPCResponse(ctx context.Context, req task.TaskRequest, lines <-chan rpcLine, stdin io.Writer, id string, state *rpcAgentState) (map[string]any, error) {
	for {
		line, err := readRPCLine(ctx, lines)
		if err != nil {
			return nil, err
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if typ, _ := ev["type"].(string); typ == "response" {
			if respID, _ := ev["id"].(string); respID == id {
				if success, _ := ev["success"].(bool); !success {
					if msg, _ := ev["error"].(string); msg != "" {
						return nil, fmt.Errorf("%s", msg)
					}
					return nil, fmt.Errorf("RPC command %s failed", id)
				}
				return ev, nil
			}
		}
		if err := r.handleRPCEvent(req, stdin, ev, state); err != nil {
			return nil, err
		}
	}
}

func (r *Runner) handleRPCLine(req task.TaskRequest, stdin io.Writer, line []byte, state *rpcAgentState) error {
	var ev map[string]any
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil
	}
	return r.handleRPCEvent(req, stdin, ev, state)
}

func (r *Runner) handleRPCEvent(req task.TaskRequest, stdin io.Writer, ev map[string]any, state *rpcAgentState) error {
	typ, _ := ev["type"].(string)
	switch typ {
	case "message_update":
		if ame, ok := ev["assistantMessageEvent"].(map[string]any); ok {
			switch eventType, _ := ame["type"].(string); eventType {
			case "text_delta":
				if delta, _ := ame["delta"].(string); delta != "" {
					state.summary.WriteString(delta)
					state.lastDetail = delta
				}
			case "error":
				if reason, _ := ame["reason"].(string); reason != "" {
					state.errorMessage = reason
				}
			case "done":
				reason, _ := ame["reason"].(string)
				state.finalAssistant = reason == "stop" || reason == "length"
			}
		}
	case "message_end":
		if message, ok := ev["message"].(map[string]any); ok {
			role, _ := message["role"].(string)
			stopReason, _ := message["stopReason"].(string)
			if role == "assistant" {
				state.finalAssistant = stopReason == "stop" || stopReason == "length"
			}
		}
	case "tool_execution_start":
		if id, _ := ev["toolCallId"].(string); id != "" {
			if _, exists := state.activeTools[id]; !exists {
				state.activeTools[id] = struct{}{}
				state.activeToolCount++
			}
		} else {
			state.activeToolCount++
		}
		if toolName, _ := ev["toolName"].(string); toolName != "" {
			state.lastDetail = "tool: " + toolName
		}
	case "tool_execution_end":
		if id, _ := ev["toolCallId"].(string); id != "" {
			if _, exists := state.activeTools[id]; exists {
				delete(state.activeTools, id)
				state.activeToolCount--
			}
		} else if state.activeToolCount > 0 {
			state.activeToolCount--
		}
		if isError, _ := ev["isError"].(bool); isError {
			if toolName, _ := ev["toolName"].(string); toolName != "" {
				state.lastDetail = "tool error: " + toolName
			}
		}
	case "auto_retry_start":
		state.retrying = true
		state.finalAssistant = false
		if msg, _ := ev["errorMessage"].(string); msg != "" {
			state.lastDetail = "retrying: " + msg
		}
	case "auto_retry_end":
		state.retrying = false
		if success, _ := ev["success"].(bool); !success {
			if msg, _ := ev["finalError"].(string); msg != "" {
				state.errorMessage = msg
			}
		}
	case "extension_error":
		if msg, _ := ev["error"].(string); msg != "" {
			state.lastDetail = "extension error: " + msg
		}
	case "extension_ui_request":
		if resp := rpcUIResponse(ev); resp != nil {
			return writeRPCCommand(stdin, resp)
		}
	case "agent_end":
		willRetry, _ := ev["willRetry"].(bool)
		state.retrying = willRetry
		if !willRetry {
			state.terminal = true
		}
	case "agent_settled":
		state.retrying = false
		state.terminal = true
	}
	if time.Since(state.lastPublished) >= 3*time.Second && state.lastDetail != "" {
		r.publishEvent(req.TaskID, "pi: "+state.lastDetail)
		state.lastPublished = time.Now()
	}
	return nil
}

func rpcUIResponse(ev map[string]any) map[string]any {
	method, _ := ev["method"].(string)
	switch method {
	case "select", "confirm", "input", "editor":
		id, _ := ev["id"].(string)
		if id == "" {
			return nil
		}
		return map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true}
	default:
		return nil
	}
}

func (r *Runner) abortRPC(ctx context.Context, req task.TaskRequest, stdin io.Writer, lines <-chan rpcLine, state *rpcAgentState) {
	_ = writeRPCCommand(stdin, map[string]any{"id": "abort", "type": "abort"})
	_, _ = r.waitForRPCResponse(ctx, req, lines, stdin, "abort", state)
}

func (r *Runner) cleanupRPCSession(req task.TaskRequest, wsDir string, stdin io.Writer, lines <-chan rpcLine, state *rpcAgentState) string {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var sessionExport string
	if err := writeRPCCommand(stdin, map[string]any{"id": "messages", "type": "get_messages"}); err == nil {
		if resp, err := r.waitForRPCResponse(cleanupCtx, req, lines, stdin, "messages", state); err == nil {
			sessionExport = renderRPCMessages(resp)
			if err := writeRPCSessionExport(wsDir, sessionExport); err != nil {
				slog.Warn("pi session export write failed", "taskID", req.TaskID, "err", err)
			}
		}
	}
	abortCtx, abortCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer abortCancel()
	r.abortRPC(abortCtx, req, stdin, lines, state)
	return sessionExport
}

func cancellationStatus(ctx context.Context, name string) (string, string) {
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		return "error", fmt.Sprintf("%s timed out", name)
	}
	return "cancelled", fmt.Sprintf("%s cancelled", name)
}

func waitForRPCProcess(ctx context.Context, cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = terminateProcess(cmd)
		}
		<-done
		return ctx.Err()
	}
}

func rpcPrompt(req task.TaskRequest) string {
	if len(req.Skills) == 0 {
		return req.Prompt
	}
	return fmt.Sprintf("You have access to the following skills: %s. Use them when relevant.\n\n%s", strings.Join(req.Skills, ", "), req.Prompt)
}

func rpcSessionID(resp map[string]any) string {
	data, _ := resp["data"].(map[string]any)
	sessionID, _ := data["sessionId"].(string)
	return sessionID
}

func rpcLastAssistantText(resp map[string]any) string {
	data, _ := resp["data"].(map[string]any)
	text, _ := data["text"].(string)
	return strings.TrimSpace(text)
}

func renderRPCMessages(resp map[string]any) string {
	data, _ := resp["data"].(map[string]any)
	messages, _ := data["messages"].([]any)
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Pi Session Export\n\n")
	for i, raw := range messages {
		msg, _ := raw.(map[string]any)
		role, _ := msg["role"].(string)
		if role == "" {
			role = "message"
		}
		b.WriteString(fmt.Sprintf("## %d. %s\n\n", i+1, role))
		text := rpcMessageText(msg)
		if text == "" {
			if data, err := json.MarshalIndent(msg, "", "  "); err == nil {
				text = "```json\n" + string(data) + "\n```"
			}
		}
		b.WriteString(strings.TrimSpace(text))
		b.WriteString("\n\n")
	}
	return b.String()
}

func rpcMessageText(msg map[string]any) string {
	content := msg["content"]
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, raw := range v {
			block, _ := raw.(map[string]any)
			if text, _ := block["text"].(string); text != "" {
				parts = append(parts, text)
				continue
			}
			if text, _ := block["content"].(string); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

func writeRPCSessionExport(wsDir, export string) error {
	if export == "" {
		return nil
	}
	path := filepath.Join(wsDir, ".pi", "session-export.md")
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(export), 0644)
}

func gvisorNoProxy() string {
	return "localhost,127.0.0.1,0.0.0.0,.local"
}

func (r *Runner) publishEvent(taskID, detail string) {
	resp := task.TaskResponse{
		TaskID:  taskID,
		Status:  "running",
		Summary: detail,
	}
	r.decorateTaskResponse(&resp, nil, "")
	r.reportTaskResponse(resp)
}
