package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/task"
	"github.com/flatout-works/chetter/runner/internal/tools"
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

	wsDir, err := r.wsManager.Create(req.TaskID)
	if err != nil {
		r.publishStatusForRequest(req, "error", err.Error(), nil)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Workspace creation failed: %v", err), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	session.WorkspaceDir = wsDir

	defer func() {
		if err := r.wsManager.Destroy(req.TaskID); err != nil {
			slog.Warn("cleanup error", "taskID", req.TaskID, "err", err)
		}
	}()

	gitURL := req.GitURL
	if req.GitURL != "" {
		slog.Info("cloning", "taskID", req.TaskID, "url", req.GitURL)
		if err := os.RemoveAll(wsDir); err != nil {
			slog.Warn("removing stale workspace", "taskID", req.TaskID, "err", err)
		}
		if err := os.MkdirAll(wsDir, 0750); err != nil {
			r.publishStatusForRequest(req, "error", err.Error(), nil)
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
		if r.cfg.Git.SSHKeyPath != "" {
			cloneCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND=ssh -i "+r.cfg.Git.SSHKeyPath+" -o StrictHostKeyChecking=no")
		}
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			slog.Error("clone error", "taskID", req.TaskID, "err", err, "output", string(out))
			r.publishStatusForRequest(req, "error", fmt.Sprintf("git clone: %v\n%s", err, string(out)), nil)
			r.publishActivityEvent("repo", "Git Clone Failed", fmt.Sprintf("Failed to clone %s", req.GitURL), "failed", fmt.Sprintf("%v\n%s", err, string(out)), time.Since(session.StartedAt).Milliseconds())
			return
		}
	}

	socketPath := r.wsManager.SocketPath(req.TaskID)

	isLocal := r.executionMode() == "local"
	bridgeCmd := r.mcpBridgePath()
	if r.executionMode() == "docker" {
		bridgeCmd = "/usr/local/bin/mcp-bridge"
	}
	if err := r.h.GenerateConfig(wsDir, socketPath, bridgeCmd, r.cfg.ChetterMCP.URL, r.cfg.ChetterMCP.AuthToken, isLocal); err != nil {
		slog.Warn("harness config warning", "taskID", req.TaskID, "err", err)
	}

	mcpServer, err := mcp.NewServer(socketPath)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("mcp server: %v", err), nil)
		return
	}
	defer mcpServer.Close()

	ws := tools.NewWorkspace(wsDir)
	git := tools.NewGit(wsDir, r.cfg.Git.SSHKeyPath, r.cfg.Git.PAT)
	deploy := tools.NewDeploy(
		wsDir,
		tools.DeployProvider(r.cfg.Deploy.Provider),
		req.TaskID,
		r.cfg.Deploy.Registry,
		r.cfg.Deploy.ChetterURL,
	)

	mcpServer.RegisterTool("workspace_read_file", ws.ReadFile)
	mcpServer.RegisterTool("workspace_write_file", ws.WriteFile)
	mcpServer.RegisterTool("workspace_list_directory", ws.ListDirectory)
	mcpServer.RegisterTool("workspace_bash", ws.Bash)
	mcpServer.RegisterTool("git_status", git.Status)
	mcpServer.RegisterTool("git_pull", git.Pull)
	mcpServer.RegisterTool("git_push", git.Push)
	mcpServer.RegisterTool("git_commit", git.Commit)
	mcpServer.RegisterTool("fetch_url", tools.Fetch)
	mcpServer.RegisterTool("deploy_build", deploy.Build)
	mcpServer.RegisterTool("deploy_push", deploy.Push)
	mcpServer.RegisterTool("deploy_run", deploy.Run)
	mcpServer.RegisterTool("deploy_status", deploy.Status)
	mcpServer.RegisterTool("deploy_stop", deploy.Stop)
	mcpServer.RegisterTool("deploy_logs", deploy.Logs)
	mcpServer.RegisterTool("deploy_list", deploy.ListContainers)
	mcpServer.RegisterTool("deploy_versions", deploy.ListVersions)
	mcpServer.RegisterTool("deploy_rollback", deploy.Rollback)

	go mcpServer.Serve(ctx)
	slog.Info("MCP server started", "taskID", req.TaskID, "socket", socketPath)

	if req.AgentImage == "" {
		r.publishStatusForRequest(req, "error", "agent_image is required", nil)
		return
	}

	switch r.executionMode() {
	case "local":
		if !r.h.SupportsServe() {
			r.runBatchAgent(ctx, session, req, socketPath)
			return
		}
		r.runLocalAgent(ctx, session, req, socketPath)
	default:
		if !r.h.SupportsServe() {
			r.runBatchAgent(ctx, session, req, socketPath)
			return
		}
		r.runDockerAgent(ctx, session, req, socketPath)
	}
}

func (r *Runner) mcpBridgePath() string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "mcp-bridge")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "mcp-bridge"
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

func runnerOwnedEnvKeys() []string {
	return []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "MEM9_API_KEY", "MEM9_API_URL", "MEM9_DEBUG", "MEM9_HOME", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "OPENCODE_API_KEY", "SYNTHETIC_API_KEY"}
}

func isRunnerOwnedEnv(key string) bool {
	switch key {
	case "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "MEM9_API_KEY", "MEM9_API_URL", "MEM9_DEBUG", "MEM9_HOME", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "OPENCODE_API_KEY", "SYNTHETIC_API_KEY":
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

func (r *Runner) runLocalAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, socketPath string) {
	env := os.Environ()
	for k, v := range req.Env {
		if isRunnerOwnedEnv(k) {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = appendRunnerOwnedEnv(env)
	env = append(env,
		"GIT_AUTHOR_NAME=Chetter Runner",
		"GIT_AUTHOR_EMAIL=chetter@chetter.flatout.works",
		"GIT_COMMITTER_NAME=Chetter Runner",
		"GIT_COMMITTER_EMAIL=chetter@chetter.flatout.works",
		"CHETTER_AGENT_NAME="+req.Agent,
		"CHETTER_MODEL_ID="+r.h.ResolvedModelID(req),
		"CHETTER_TASK_ID="+req.TaskID,
		"CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)

	secret := r.h.ServerPassword()
	env = append(env,
		"TASK_ID="+req.TaskID,
		"WORKSPACE="+session.WorkspaceDir,
		"MCP_SOCKET_PATH="+socketPath,
	)
	for k, v := range r.h.Env(session.WorkspaceDir, secret) {
		env = append(env, k+"="+v)
	}
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
	serveCmd := exec.CommandContext(ctx, r.h.Name(), r.h.ServeArgs(port)...)
	serveCmd.Dir = session.WorkspaceDir
	serveCmd.Env = env
	stdout, err := serveCmd.StdoutPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("opencode stdout pipe: %v", err), nil)
		return
	}
	stderr, err := serveCmd.StderrPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("opencode stderr pipe: %v", err), nil)
		return
	}

	if err := serveCmd.Start(); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("start opencode serve: %v", err), nil)
		return
	}
	go r.h.PipeOutput(req.TaskID, "stdout", stdout)
	go r.h.PipeOutput(req.TaskID, "stderr", stderr)

	defer func() {
		if serveCmd.Process != nil {
			serveCmd.Process.Kill()
			serveCmd.Wait()
		}
	}()

	if err := r.h.WaitForReady(ctx, baseURL, secret, 120*time.Second); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness serve not ready: %v", err), nil)
		return
	}
	slog.Info("harness serve ready", "taskID", req.TaskID, "url", baseURL)

	sid, err := r.h.CreateSession(ctx, baseURL, secret)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("create session: %v", err), nil)
		return
	}
	slog.Info("session", "taskID", req.TaskID, "sessionID", sid)

	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go r.h.WatchEvents(eventsCtx, req.TaskID, baseURL, secret, func(status, message string) {
		r.publishStatus(req.TaskID, status, message, nil)
	})

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	summary, err := r.h.SendPrompt(ctx, baseURL, sid, secret, req, session.WorkspaceDir, taskPromptTimeout(req.TimeoutSec))
	var sessionExport string
	if sid != "" {
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid)
	}
	if err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid, sessionExport)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed (local)", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	slog.Info("agent completed", "taskID", req.TaskID)
	r.publishStatusWithMetadata(req, "done", truncateSummary(summary), nil, sid, sessionExport)
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (local)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) runDockerAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, socketPath string) {
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

	secret := r.h.ServerPassword()

	gvisor := r.cfg.Execution.UseGVisor
	netName := ""
	runnerIP := ""
	dockerArgs := []string{
		"run", "-d",
		"--entrypoint", "/usr/local/bin/opencode",
		"--name", containerName,
	}
	if gvisor {
		netName = runcNetwork()
		dockerArgs = append(dockerArgs, "--runtime", "runsc", "--dns", "8.8.8.8", "--dns", "8.8.4.4")
		dockerArgs = append(dockerArgs, "--network", netName)
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
	} else {
		dockerArgs = append(dockerArgs, "-p", fmt.Sprintf("%s:%d:%d", bindAddr, hostPort, containerPort))
	}
	dockerArgs = append(dockerArgs,
		"-v", session.WorkspaceDir+":/workspace",
		"-v", socketPath+":/workspace/.chetter.sock",
		"-w", "/workspace",
		"-e", "TASK_ID="+req.TaskID,
		"-e", "WORKSPACE=/workspace",
		"-e", "MCP_SOCKET_PATH=/workspace/.chetter.sock",
		"-e", "XDG_CONFIG_HOME=/workspace/.config",
		"-e", "XDG_DATA_HOME=/workspace/.local/share",
		"-e", "XDG_STATE_HOME=/workspace/.local/state",
		"-e", "XDG_CACHE_HOME=/workspace/.cache",
		"-e", "CHETTER_AGENT_NAME="+req.Agent,
		"-e", "CHETTER_MODEL_ID="+r.h.ResolvedModelID(req),
		"-e", "CHETTER_TASK_ID="+req.TaskID,
		"-e", "CHETTER_RUNNER_IMAGE="+os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST="+os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	)
	if gvisor {
		dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
	} else {
		dockerArgs = append(dockerArgs, "-e", "HOME=/opt/opencode")
	}

	if gvisor {
		runnerIP = hostIP(netName)
		dockerArgs = append(dockerArgs,
			"-e", "CHETTER_PROXY="+runnerIP+":18080",
			"-e", "NO_PROXY=localhost,127.0.0.1,.local,chetter-mcp",
			"-e", "no_proxy=localhost,127.0.0.1,.local,chetter-mcp",
		)
	}

	for k, v := range r.h.Env("/workspace", secret) {
		key := k
		switch key {
		case "OPENCODE_CONFIG":
			key = "OPENCODE_CONFIG"
			v = "/workspace/.opencode.json"
		case "OPENCODE_SERVER_PASSWORD":
			v = secret
		}
		dockerArgs = append(dockerArgs, "-e", key+"="+v)
	}

	for k, v := range req.Env {
		if isRunnerOwnedEnv(k) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	for _, key := range runnerOwnedEnvKeys() {
		if val := os.Getenv(key); val != "" {
			dockerArgs = append(dockerArgs, "-e", key+"="+val)
		}
	}

	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, r.h.ServeArgs(containerPort)...)
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
		exec.Command("docker", "rm", "-f", containerName).Run()
	}()

	baseURL := fmt.Sprintf("http://%s:%d", bindAddr, hostPort)
	if gvisor {
		ipOut, _ := exec.Command("docker", "inspect", "-f", "{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}} {{end}}", containerName).CombinedOutput()
		containerIP := ""
		for _, ip := range strings.Fields(strings.TrimSpace(string(ipOut))) {
			if ip != "" && ip != "127.0.0.1" {
				containerIP = ip
				break
			}
		}
		if containerIP != "" {
			baseURL = fmt.Sprintf("http://%s:%d", containerIP, containerPort)
		}
	}

	if err := r.h.WaitForReady(ctx, baseURL, secret, 120*time.Second); err != nil {
		logs, _ := exec.Command("docker", "logs", containerName).CombinedOutput()
		slog.Error("harness serve not ready in container", "taskID", req.TaskID, "err", err, "logs", string(logs))
		r.publishEvent(req.TaskID, fmt.Sprintf("container logs: %s", truncateSummary(string(logs))))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("container harness serve not ready: %v", err), nil)
		return
	}
	slog.Info("container harness serve ready", "taskID", req.TaskID, "url", baseURL)

	sid, err := r.h.CreateSession(ctx, baseURL, secret)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("create session: %v", err), nil)
		return
	}
	slog.Info("session created", "taskID", req.TaskID, "sessionID", sid)

	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go r.h.WatchEvents(eventsCtx, req.TaskID, baseURL, secret, func(status, message string) {
		r.publishStatus(req.TaskID, status, message, nil)
	})

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	summary, err := r.h.SendPrompt(ctx, baseURL, sid, secret, req, session.WorkspaceDir, taskPromptTimeout(req.TimeoutSec))
	var sessionExport string
	if sid != "" {
		sessionExport = r.readSessionExport(req.TaskID, session.WorkspaceDir, sid)
	}
	if err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid, sessionExport)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	slog.Info("agent completed", "taskID", req.TaskID)
	r.publishStatusWithMetadata(req, "done", truncateSummary(summary), nil, sid, sessionExport)
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (docker)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) readSessionExport(taskID, wsDir, sid string) string {
	if export, err := r.h.ReadSessionExport(wsDir, sid); err == nil {
		return export
	} else {
		slog.Warn("session export failed", "taskID", taskID, "err", err)
		r.publishEvent(taskID, fmt.Sprintf("session export: %v", err))
	}
	return ""
}

func (r *Runner) publishStatusWithMetadata(req task.TaskRequest, status, message string, artifacts []string, sessionID, sessionExport string) {
	resp := task.TaskResponse{
		TaskID:        req.TaskID,
		Status:        status,
		Artifacts:     artifacts,
		SessionExport: sessionExport,
	}
	r.decorateTaskResponseForRequest(&resp, req, sessionID)
	if isTerminalStatus(status) {
		resp.EndedAt = time.Now()
	}
	if status == "error" || status == "cancelled" {
		resp.Error = message
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

func (r *Runner) runBatchAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, socketPath string) {
	args := r.h.RunBatchCommand(req)
	name := r.h.Name()
	slog.Info("starting batch harness", "taskID", req.TaskID, "harness", name, "args", args)
	r.publishStatusForRequest(req, "running", "Starting agent (batch mode)...", nil)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = session.WorkspaceDir

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

	go r.h.PipeOutput(req.TaskID, "stderr", stderr)

	var summary string
	readCtx, readCancel := context.WithCancel(ctx)
	defer readCancel()
	if out, err := readBatchOutput(readCtx, stdout, req.TaskID, func(detail string) {
		r.publishEvent(req.TaskID, fmt.Sprintf("%s: %s", name, detail))
	}); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("%s: %v", name, err), nil)
		return
	} else {
		summary = out
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			r.publishStatusForRequest(req, "error", fmt.Sprintf("%s timed out", name), nil)
			return
		}
		r.publishStatusForRequest(req, "error", fmt.Sprintf("%s: %v\n%s", name, err, truncateSummary(summary)), nil)
		return
	}

	slog.Info("batch agent completed", "taskID", req.TaskID)
	r.publishStatusForRequest(req, "done", truncateSummary(summary), nil)
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

func readBatchOutput(ctx context.Context, reader io.Reader, taskID string, onEvent func(detail string)) (string, error) {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	var lastDetail string
	lastPublished := time.Now()

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return buf.String(), ctx.Err()
		default:
		}
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')

		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if detail := eventDetail(ev); detail != "" {
			lastDetail = detail
		}
		if time.Since(lastPublished) >= 3*time.Second && lastDetail != "" {
			onEvent(lastDetail)
			lastPublished = time.Now()
		}
	}
	return buf.String(), scanner.Err()
}

func eventDetail(ev map[string]any) string {
	typ, _ := ev["type"].(string)
	if typ == "system" {
		sub, _ := ev["subtype"].(string)
		return "system." + sub
	}
	if typ == "stream_event" {
		if event, ok := ev["event"].(map[string]any); ok {
			if delta, ok := event["delta"].(map[string]any); ok {
				if t, _ := delta["type"].(string); t == "text_delta" {
					if text, _ := delta["text"].(string); text != "" {
						return text
					}
				}
			}
			return "stream_event"
		}
	}
	if typ == "user" {
		if msg, ok := ev["message"].(map[string]any); ok {
			if text, _ := msg["text"].(string); text != "" {
				return text
			}
		}
	}
	return ""
}
