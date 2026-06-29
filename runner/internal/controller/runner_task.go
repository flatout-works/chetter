package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	containerWorkspaceDir      = "/workspace"
	injectedGitHubTokenTaskEnv = "__chetter_github_token"
)

func (r *Runner) runTask(req task.TaskRequest) {
	defer func() { <-r.sem }()
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("runner panic", "taskID", req.TaskID, "panic", rec)
			r.publishStatusForRequest(req, "error", fmt.Sprintf("runner panic: %v", rec), nil)
			panic(rec)
		}
	}()

	parent := r.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(req.TimeoutSec)*time.Second)
	defer cancel()

	session := &task.TaskSession{
		TaskID:    req.TaskID,
		Request:   req,
		Cancel:    cancel,
		StartedAt: time.Now(),
	}
	r.mu.Lock()
	r.tasks[req.TaskID] = session
	r.totalStarted++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.tasks, req.TaskID)
		r.mu.Unlock()
	}()

	r.publishStatusForRequest(req, "running", "Preparing workspace...", nil)
	r.publishActivityEvent("agent", "Task Started", fmt.Sprintf("Task %s started", req.TaskID), "running", "", 0)

	h := r.harnessFor(req.Harness)

	if req.ResumeWorkspacePath != "" {
		mcpServer, err := r.startWorkspaceMCP(ctx, req.TaskID)
		if err != nil {
			r.publishStatusForRequest(req, "error", fmt.Sprintf("mcp server: %v", err), nil)
			return
		}
		defer mcpServer.Close()
		mcpURL := runnerMCPURL(r, mcpServer)
		r.runDockerAgentResume(ctx, session, req, mcpURL, h)
		return
	}

	wsDir, err := r.wsManager.Create(req.TaskID)
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
		if err := r.wsManager.Destroy(req.TaskID); err != nil {
			slog.Warn("cleanup error", "taskID", req.TaskID, "err", err)
		}
	}()

	if req.GitURL != "" {
		slog.Info("cloning", "taskID", req.TaskID, "url", req.GitURL)
		if err := os.RemoveAll(wsDir); err != nil {
			slog.Warn("removing stale workspace", "taskID", req.TaskID, "err", err)
		}
		if err := os.MkdirAll(wsDir, 0750); err != nil {
			r.publishStatusForRequest(req, "error", err.Error(), nil)
			return
		}
		gitURL := cloneURLForRequest(req, r.cfg.Git.PAT)
		cloneCmd := exec.CommandContext(ctx, "git", "clone")
		if req.GitRef != "" {
			cloneCmd.Args = append(cloneCmd.Args, "-b", req.GitRef)
		}
		cloneCmd.Args = append(cloneCmd.Args, gitURL, ".")
		cloneCmd.Dir = wsDir
		cloneCmd.Env = filteredHostEnv()
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

	mcpServer, err := r.startWorkspaceMCP(ctx, req.TaskID)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("mcp server: %v", err), nil)
		return
	}
	defer mcpServer.Close()
	mcpURL := runnerMCPURL(r, mcpServer)

	if err := h.GenerateConfig(wsDir, mcpURL, r.cfg.ChetterMCP.URL, r.cfg.ChetterMCP.AuthToken, req, isLocal); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness config: %v", err), nil)
		return
	}

	if req.AgentImage == "" {
		r.publishStatusForRequest(req, "error", "agent_image is required", nil)
		return
	}

	switch r.executionMode() {
	case "local":
		if h.SupportsRpc() {
			r.runRpcAgent(ctx, session, req, mcpURL, h)
			return
		}
		r.runLocalAgent(ctx, session, req, mcpURL, h)
	default:
		if h.SupportsRpc() {
			r.runDockerRpcAgent(ctx, session, req, mcpURL, h)
			return
		}
		r.runDockerAgent(ctx, session, req, mcpURL, h)
	}
}

func (r *Runner) startWorkspaceMCP(ctx context.Context, taskID string) (*mcp.Server, error) {
	mcpServer, err := mcp.NewServer()
	if err != nil {
		return nil, err
	}
	r.registerGitHubMCPTools(mcpServer, taskID)
	slog.Info("MCP server started", "taskID", taskID, "addr", mcpServer.Addr())
	return mcpServer, nil
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

func hostWorkspaceDir(containerPath string) string {
	if hostRoot := os.Getenv("HOST_WORKSPACE_ROOT"); hostRoot != "" {
		if after, found := strings.CutPrefix(containerPath, "/var/lib/chetter-runner"); found {
			return hostRoot + after
		}
	}
	return containerPath
}

func appendRunnerOwnedEnv(env []string, req task.TaskRequest) []string {
	for _, key := range runnerOwnedEnvKeys() {
		if value := runnerOwnedEnvValue(key, req); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func addRunnerOwnedEnv(env map[string]string, req task.TaskRequest) {
	for _, key := range runnerOwnedEnvKeys() {
		if value := runnerOwnedEnvValue(key, req); value != "" {
			env[key] = value
		}
	}
}

func filteredHostEnv() []string {
	hostEnv := os.Environ()
	env := make([]string, 0, len(hostEnv))
	for _, entry := range hostEnv {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !shouldForwardHostEnv(key) {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func shouldForwardHostEnv(key string) bool {
	if key == "" || key == injectedGitHubTokenTaskEnv || isRunnerOwnedEnv(key) {
		return false
	}
	switch key {
	case "PATH", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "TERM", "COLORTERM", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "SSL_CERT_FILE", "SSL_CERT_DIR":
		return true
	default:
		return strings.HasPrefix(key, "LC_")
	}
}

func appendRunnerOwnedDockerArgs(args []string, req task.TaskRequest) []string {
	for _, key := range runnerOwnedEnvKeys() {
		if val := runnerOwnedEnvValue(key, req); val != "" {
			args = append(args, "-e", key+"="+val)
		}
	}
	return args
}

func runnerOwnedEnvValue(key string, req task.TaskRequest) string {
	if key == "GITHUB_TOKEN" || key == "GH_TOKEN" {
		return trustedInjectedGitHubToken(req)
	}
	return os.Getenv(key)
}

func trustedInjectedGitHubToken(req task.TaskRequest) string {
	val := strings.TrimSpace(req.Env[injectedGitHubTokenTaskEnv])
	if val == "" || val == "[redacted]" {
		return ""
	}
	return val
}

func shouldForwardTaskEnv(key string) bool {
	return key != injectedGitHubTokenTaskEnv && !isRunnerOwnedEnv(key)
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
		"GH_TOKEN",
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
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "GH_TOKEN", "GITHUB_TOKEN", "MEM9_API_KEY", "MEM9_API_URL", "MEM9_DEBUG", "MEM9_HOME", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "OPENCODE_API_KEY", "SYNTHETIC_API_KEY", "ZAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "XAI_API_KEY":
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

func cloneURLForRequest(req task.TaskRequest, runnerPAT string) string {
	if req.GitURL == "" {
		return ""
	}
	if token := trustedInjectedGitHubToken(req); token != "" {
		return injectGitHubTokenIntoCloneURL(req.GitURL, token)
	}
	if runnerPAT != "" && strings.HasPrefix(req.GitURL, "https://") {
		return injectPATIntoURL(req.GitURL, runnerPAT)
	}
	return req.GitURL
}

func injectGitHubTokenIntoCloneURL(raw, token string) string {
	if token == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return raw
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return raw
	}
	parsed.User = url.UserPassword("x-access-token", token)
	return parsed.String()
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

func (r *Runner) runLocalAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, mcpURL string, h harness.Harness) {
	secret := h.ServerPassword()
	env := r.agentEnv(req, session.WorkspaceDir, secret, h)

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
			serveCmd.Process.Kill()
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

	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	var tokenUsage task.TokenUsage
	go h.WatchEvents(eventsCtx, req.TaskID, baseURL, secret, func(status, message string) {
		r.publishStatus(req.TaskID, status, message, nil)
	}, func(usage task.TokenUsage) {
		tokenUsage.InputTokens += usage.InputTokens
		tokenUsage.OutputTokens += usage.OutputTokens
		tokenUsage.CacheReadTokens += usage.CacheReadTokens
		tokenUsage.CacheWriteTokens += usage.CacheWriteTokens
		tokenUsage.ReasoningTokens += usage.ReasoningTokens
		tokenUsage.CostCents += usage.CostCents
	})

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	summary, err := h.SendPrompt(ctx, baseURL, sid, secret, req, session.WorkspaceDir, taskPromptTimeout(req.TimeoutSec))
	var sessionExport string
	if sid != "" {
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
	}
	if err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid, sessionExport, tokenUsage)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed (local)", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	slog.Info("agent completed", "taskID", req.TaskID)
	r.publishStatusWithMetadata(req, "done", truncateSummary(summary), nil, sid, sessionExport, task.TokenUsage{})
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (local)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) runDockerAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, mcpURL string, h harness.Harness) {
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
	containerName := "chetter-task-" + req.TaskID

	exec.Command("docker", "rm", "-f", containerName).Run()

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
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "--runtime", "runsc", "--dns", "8.8.8.8", "--dns", "8.8.4.4")
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
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
		"-e", "CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)
	if gvisor {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	} else {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	}

	if gvisor {
		runnerIP = hostIP(netName)
		dockerArgs = append(dockerArgs,
			"-e", "HTTP_PROXY=http://"+runnerIP+":18080",
			"-e", "HTTPS_PROXY=http://"+runnerIP+":18080",
			"-e", "http_proxy=http://"+runnerIP+":18080",
			"-e", "https_proxy=http://"+runnerIP+":18080",
			"-e", "CHETTER_PROXY="+runnerIP+":18080",
			"-e", "NO_PROXY=localhost,127.0.0.1,0.0.0.0,.local",
			"-e", "no_proxy=localhost,127.0.0.1,0.0.0.0,.local",
		)
	}

	for k, v := range h.Env("/workspace", secret, req) {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}

	for k, v := range req.Env {
		if !shouldForwardTaskEnv(k) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = appendRunnerOwnedDockerArgs(dockerArgs, req)

	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, serveArgs...)
	if gvisor {
		dockerArgs = append(dockerArgs, "--hostname", "0.0.0.0")
	}

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
		exec.Command("docker", "rm", "-f", containerName).Run()
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

	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	var tokenUsage task.TokenUsage
	go h.WatchEvents(eventsCtx, req.TaskID, baseURL, secret, func(status, message string) {
		r.publishStatus(req.TaskID, status, message, nil)
	}, func(usage task.TokenUsage) {
		tokenUsage.InputTokens += usage.InputTokens
		tokenUsage.OutputTokens += usage.OutputTokens
		tokenUsage.CacheReadTokens += usage.CacheReadTokens
		tokenUsage.CacheWriteTokens += usage.CacheWriteTokens
		tokenUsage.ReasoningTokens += usage.ReasoningTokens
		tokenUsage.CostCents += usage.CostCents
	})

	summary, err := h.SendPrompt(ctx, baseURL, sid, secret, req, session.WorkspaceDir, taskPromptTimeout(req.TimeoutSec))
	var sessionExport string
	if err != nil {
		workspacePath := ""
		if req.CheckpointAfterSuccess && classifyErrorCategory("error", fmt.Sprintf("prompt failed: %v", err)) == "timeout" {
			workspacePath = session.WorkspaceDir
			session.PreserveWorkspace = true
			slog.Info("preserving workspace for recoverable timed-out session", "taskID", req.TaskID, "workspace", workspacePath)
		}
		if sid != "" {
			slog.Info("aborting session before shutdown", "taskID", req.TaskID, "sessionID", sid)
			if abortErr := h.AbortSession(ctx, baseURL, sid, secret); abortErr != nil {
				slog.Warn("failed to abort session", "taskID", req.TaskID, "err", abortErr)
			}
			exec.Command("docker", "stop", containerName).Run()
			sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
		}
		r.publishStatusWithMetadataAndCheckpoint(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid, sessionExport, "", workspacePath, tokenUsage)
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
		exec.Command("docker", "stop", containerName).Run()
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
	}
	r.publishStatusWithMetadataAndCheckpoint(req, "done", truncateSummary(summary), nil, sid, sessionExport, "", workspacePath, tokenUsage)
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (docker)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) runDockerAgentResume(ctx context.Context, session *task.TaskSession, req task.TaskRequest, mcpURL string, h harness.Harness) {
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

	sid := req.ResumeHarnessSessionID
	if sid == "" {
		r.publishStatusForRequest(req, "error", "no harness session ID for resume", nil)
		return
	}

	r.publishStatusForRequest(req, "running", "Resuming agent session...", nil)

	mcpServer, err := r.startWorkspaceMCP(ctx, req.TaskID)
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
	containerName := "chetter-task-" + req.TaskID
	exec.Command("docker", "rm", "-f", containerName).Run()
	defer exec.Command("docker", "rm", "-f", containerName).Run()

	secret := h.ServerPassword()
	gvisor := r.cfg.Execution.UseGVisor
	netName := runcNetwork()
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
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "--runtime", "runsc", "--dns", "8.8.8.8", "--dns", "8.8.4.4")
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
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
		"-e", "CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)
	if gvisor {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	} else {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	}
	var runnerIP string
	if gvisor {
		runnerIP = hostIP(netName)
		dockerArgs = append(dockerArgs,
			"-e", "HTTP_PROXY=http://"+runnerIP+":18080",
			"-e", "HTTPS_PROXY=http://"+runnerIP+":18080",
			"-e", "http_proxy=http://"+runnerIP+":18080",
			"-e", "https_proxy=http://"+runnerIP+":18080",
			"-e", "CHETTER_PROXY="+runnerIP+":18080",
			"-e", "NO_PROXY=localhost,127.0.0.1,0.0.0.0,.local",
			"-e", "no_proxy=localhost,127.0.0.1,0.0.0.0,.local",
		)
	}
	for k, v := range h.Env("/workspace", secret, req) {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	for k, v := range req.Env {
		if !shouldForwardTaskEnv(k) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = appendRunnerOwnedDockerArgs(dockerArgs, req)

	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, serveArgs...)
	if gvisor {
		dockerArgs = append(dockerArgs, "--hostname", "0.0.0.0")
	}

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

	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	var tokenUsage task.TokenUsage
	go h.WatchEvents(eventsCtx, req.TaskID, baseURL, secret, func(status, message string) {
		r.publishStatus(req.TaskID, status, message, nil)
	}, func(usage task.TokenUsage) {
		tokenUsage.InputTokens += usage.InputTokens
		tokenUsage.OutputTokens += usage.OutputTokens
		tokenUsage.CacheReadTokens += usage.CacheReadTokens
		tokenUsage.CacheWriteTokens += usage.CacheWriteTokens
		tokenUsage.ReasoningTokens += usage.ReasoningTokens
		tokenUsage.CostCents += usage.CostCents
	})

	summary, err := h.SendPrompt(ctx, baseURL, sid, secret, req, workspaceDir, taskPromptTimeout(req.TimeoutSec))
	var sessionExport string
	if err != nil {
		workspacePath := ""
		if req.CheckpointAfterSuccess && classifyErrorCategory("error", fmt.Sprintf("prompt failed: %v", err)) == "timeout" {
			workspacePath = session.WorkspaceDir
			session.PreserveWorkspace = true
			slog.Info("preserving workspace for recoverable timed-out session", "taskID", req.TaskID, "workspace", workspacePath)
		}
		if sid != "" {
			slog.Info("aborting session before shutdown", "taskID", req.TaskID, "sessionID", sid)
			if abortErr := h.AbortSession(ctx, baseURL, sid, secret); abortErr != nil {
				slog.Warn("failed to abort session", "taskID", req.TaskID, "err", abortErr)
			}
			exec.Command("docker", "stop", containerName).Run()
			sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
		}
		r.publishStatusWithMetadataAndCheckpoint(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid, sessionExport, "", workspacePath, tokenUsage)
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
		exec.Command("docker", "stop", containerName).Run()
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid, h)
	}
	slog.Info("agent completed on resume", "taskID", req.TaskID)
	r.publishStatusWithMetadataAndCheckpoint(req, "done", truncateSummary(summary), nil, sid, sessionExport, "", workspacePath, tokenUsage)
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

func (r *Runner) readSessionExport(taskID, wsDir, sid string, h harness.Harness) string {
	if export, err := h.ReadSessionExport(wsDir, sid); err == nil {
		return export
	} else {
		slog.Warn("session export failed", "taskID", taskID, "err", err)
		r.publishEvent(taskID, fmt.Sprintf("session export: %v", err))
	}
	return ""
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
	summary       strings.Builder
	lastDetail    string
	lastPublished time.Time
	sessionID     string
	terminal      bool
	errorMessage  string
}

func (r *Runner) runRpcAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, mcpURL string, h harness.Harness) {
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

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = session.WorkspaceDir
	cmd.Env = r.agentEnv(req, session.WorkspaceDir, "", h)
	r.runRPCAgentCommand(ctx, session, req, h, cmd)
}

func (r *Runner) runDockerRpcAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, mcpURL string, h harness.Harness) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	args := h.RpcCommand(req)
	if len(args) == 0 {
		r.publishStatusForRequest(req, "error", "harness does not provide an RPC command", nil)
		return
	}

	containerName := "chetter-task-" + req.TaskID
	exec.Command("docker", "rm", "-f", containerName).Run()
	defer exec.Command("docker", "rm", "-f", containerName).Run()

	gvisor := r.cfg.Execution.UseGVisor
	netName := ""
	runnerIP := ""
	if gvisor {
		netName = runcNetwork()
		runnerIP = hostIP(netName)
	}

	dockerArgs := dockerRPCArgs(req, session.WorkspaceDir, containerName, h, args, gvisor, netName, runnerIP)
	name := h.Name()
	slog.Info("starting Docker RPC harness", "taskID", req.TaskID, "harness", name, "image", req.AgentImage, "args", args, "gvisor", gvisor)
	r.publishStatusForRequest(req, "running", "Starting dev container (RPC mode)...", nil)

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	r.runRPCAgentCommand(ctx, session, req, h, cmd)
}

func dockerRPCArgs(req task.TaskRequest, wsDir, containerName string, h harness.Harness, command []string, gvisor bool, netName, runnerIP string) []string {
	dockerArgs := []string{
		"run", "--rm", "-i",
		"--entrypoint", command[0],
		"--name", containerName,
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "--runtime", "runsc", "--dns", "8.8.8.8", "--dns", "8.8.4.4")
		dockerArgs = append(dockerArgs, "--network", netName)
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
		"-e", "CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)
	if gvisor {
		dockerArgs = append(dockerArgs,
			"-e", "HOME="+containerWorkspaceDir,
			"-e", "HTTP_PROXY=http://"+runnerIP+":18080",
			"-e", "HTTPS_PROXY=http://"+runnerIP+":18080",
			"-e", "http_proxy=http://"+runnerIP+":18080",
			"-e", "https_proxy=http://"+runnerIP+":18080",
			"-e", "CHETTER_PROXY="+runnerIP+":18080",
			"-e", "NO_PROXY=localhost,127.0.0.1,0.0.0.0,.local",
			"-e", "no_proxy=localhost,127.0.0.1,0.0.0.0,.local",
		)
	} else {
		dockerArgs = append(dockerArgs, "-e", "HOME=/opt/opencode")
	}

	for k, v := range h.Env(containerWorkspaceDir, "", req) {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	for k, v := range req.Env {
		if !shouldForwardTaskEnv(k) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = appendRunnerOwnedDockerArgs(dockerArgs, req)

	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, command[1:]...)
	return dockerArgs
}

func (r *Runner) runRPCAgentCommand(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.Harness, cmd *exec.Cmd) {
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
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	reader := bufio.NewReader(stdout)
	state := &rpcAgentState{lastPublished: time.Now()}

	readyCmd := map[string]any{"id": "ready", "type": "get_state"}
	if err := writeRPCCommand(stdin, readyCmd); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("write ready probe: %v", err), nil)
		return
	}
	readyResp, err := r.waitForRPCResponse(ctx, req, reader, stdin, "ready", state)
	if err != nil {
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
	if _, err := r.waitForRPCResponse(ctx, req, reader, stdin, "prompt", state); err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("%s prompt: %v", name, err), nil, state.sessionID, "", task.TokenUsage{})
		return
	}

	for !state.terminal {
		line, err := readRPCLine(ctx, reader)
		if err != nil {
			if ctx.Err() != nil {
				var sessionExport string
				msgCtx, msgCancel := context.WithTimeout(context.Background(), 10*time.Second)
				messagesCmd := map[string]any{"id": "messages", "type": "get_messages"}
				if err := writeRPCCommand(stdin, messagesCmd); err == nil {
					if resp, err := r.waitForRPCResponse(msgCtx, req, reader, stdin, "messages", state); err == nil {
						sessionExport = renderRPCMessages(resp)
						if err := writeRPCSessionExport(session.WorkspaceDir, sessionExport); err != nil {
							slog.Warn("pi session export write failed", "taskID", req.TaskID, "err", err)
						}
					}
				}
				msgCancel()
				r.abortRPC(ctx, req, stdin, reader, state)
				r.publishStatusWithMetadata(req, "error", fmt.Sprintf("%s timed out", name), nil, state.sessionID, sessionExport, task.TokenUsage{})
				return
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
		if resp, err := r.waitForRPCResponse(ctx, req, reader, stdin, "result", state); err == nil {
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
		if resp, err := r.waitForRPCResponse(ctx, req, reader, stdin, "messages", state); err == nil {
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

	_ = stdin.Close()
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("%s: %v", name, err), nil, state.sessionID, sessionExport, task.TokenUsage{})
		exited = true
		return
	}
	exited = true

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
	env := filteredHostEnv()
	for k, v := range req.Env {
		if !shouldForwardTaskEnv(k) {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = appendRunnerOwnedEnv(env, req)
	env = append(env,
		"GIT_AUTHOR_NAME=Chetter Runner",
		"GIT_AUTHOR_EMAIL=chetter@chetter.flatout.works",
		"GIT_COMMITTER_NAME=Chetter Runner",
		"GIT_COMMITTER_EMAIL=chetter@chetter.flatout.works",
		"CHETTER_AGENT_NAME="+req.Agent,
		"CHETTER_MODEL_ID="+h.ResolvedModelID(req),
		"CHETTER_TASK_ID="+req.TaskID,
		"CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
		"TASK_ID="+req.TaskID,
		"WORKSPACE="+wsDir,
		"HOME="+wsDir,
	)
	for k, v := range h.Env(wsDir, secret, req) {
		env = append(env, k+"="+v)
	}
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

func readRPCLine(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return []byte(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")), nil
}

func (r *Runner) waitForRPCResponse(ctx context.Context, req task.TaskRequest, reader *bufio.Reader, stdin io.Writer, id string, state *rpcAgentState) (map[string]any, error) {
	for {
		line, err := readRPCLine(ctx, reader)
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
			}
		}
	case "tool_execution_start":
		if toolName, _ := ev["toolName"].(string); toolName != "" {
			state.lastDetail = "tool: " + toolName
		}
	case "tool_execution_end":
		if isError, _ := ev["isError"].(bool); isError {
			if toolName, _ := ev["toolName"].(string); toolName != "" {
				state.lastDetail = "tool error: " + toolName
			}
		}
	case "auto_retry_start":
		if msg, _ := ev["errorMessage"].(string); msg != "" {
			state.lastDetail = "retrying: " + msg
		}
	case "auto_retry_end":
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
		if willRetry, _ := ev["willRetry"].(bool); !willRetry {
			state.terminal = true
		}
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

func (r *Runner) abortRPC(ctx context.Context, req task.TaskRequest, stdin io.Writer, reader *bufio.Reader, state *rpcAgentState) {
	_ = writeRPCCommand(stdin, map[string]any{"id": "abort", "type": "abort"})
	_, _ = r.waitForRPCResponse(ctx, req, reader, stdin, "abort", state)
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

func (r *Runner) publishEvent(taskID, detail string) {
	resp := task.TaskResponse{
		TaskID:  taskID,
		Status:  "running",
		Summary: detail,
	}
	r.decorateTaskResponse(&resp, nil, "")
	r.reportTaskResponse(resp)
}
