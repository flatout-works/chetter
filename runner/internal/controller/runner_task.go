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

	"github.com/flatout-works/chetter/runner/internal/containerd"
	"github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/network"
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

	var taskNet *network.TaskNetwork
	if r.executionMode() == "kata" {
		taskNet, err = r.bridgeMgr.Setup(ctx, req.TaskID)
		if err != nil {
			slog.Error("bridge setup error", "taskID", req.TaskID, "err", err)
			r.publishStatusForRequest(req, "error", fmt.Sprintf("network isolation setup: %v", err), nil)
			return
		}
		slog.Info("network bridge ready", "taskID", req.TaskID, "bridge", taskNet.Bridge)
		defer func() {
			if err := r.bridgeMgr.Teardown(ctx, taskNet); err != nil {
				slog.Error("bridge teardown error", "taskID", req.TaskID, "err", err)
			}
		}()
	}

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
	case "docker":
		if !r.h.SupportsServe() {
			r.runBatchAgent(ctx, session, req, socketPath)
			return
		}
		r.runDockerAgent(ctx, session, req, socketPath)
	default:
		r.runKataAgent(ctx, session, req, socketPath, taskNet)
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

func (r *Runner) runKataAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, socketPath string, taskNet *network.TaskNetwork) {
	if taskNet == nil {
		r.publishStatusForRequest(req, "error", "network isolation is required in Kata mode", nil)
		return
	}

	slog.Info("pulling image", "taskID", req.TaskID, "image", req.AgentImage)
	if err := r.containerd.Pull(ctx, req.AgentImage); err != nil {
		slog.Warn("pull warning", "taskID", req.TaskID, "err", err)
	}

	env := map[string]string{
		"TASK_ID":         req.TaskID,
		"WORKSPACE":       "/workspace",
		"MCP_SOCKET_PATH": "/run/mcp/agent.sock",
		"HOME":            "/opt/opencode",
		"XDG_CONFIG_HOME": "/opt/opencode/.config",
		"XDG_DATA_HOME":   "/workspace/.local/share",
		"XDG_STATE_HOME":  "/workspace/.local/state",
		"XDG_CACHE_HOME":  "/workspace/.cache",
		"PATH":            "/workspace/.local/share/opencode/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	if taskNet != nil {
		proxyHost := taskNet.GatewayIP + r.cfg.Proxy.ListenAddr
		env["CHETTER_PROXY"] = proxyHost
		env["HTTP_PROXY"] = "http://" + proxyHost
		env["HTTPS_PROXY"] = "http://" + proxyHost
		env["NO_PROXY"] = "localhost,127.0.0.1,.local"
	}
	for k, v := range req.Env {
		if isRunnerOwnedEnv(k) {
			continue
		}
		env[k] = v
	}
	addRunnerOwnedEnv(env)
	env["CHETTER_AGENT_NAME"] = req.Agent
	env["CHETTER_MODEL_ID"] = r.h.ResolvedModelID(req)
	env["CHETTER_RUNNER_IMAGE"] = os.Getenv("CHETTER_RUNNER_IMAGE")
	env["CHETTER_RUNNER_IMAGE_DIGEST"] = os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST")
	resolvConfPath := session.WorkspaceDir + "/.chetter-resolv.conf"
	if err := os.WriteFile(resolvConfPath, []byte("nameserver "+taskNet.GatewayIP+"\noptions timeout:2 attempts:2\n"), 0644); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("write task resolv.conf: %v", err), nil)
		return
	}

	mounts := []containerd.Mount{
		{
			Type:        "bind",
			Source:      session.WorkspaceDir,
			Destination: "/workspace",
			Options:     []string{"rbind", "rw"},
		},
		{
			Type:        "bind",
			Source:      resolvConfPath,
			Destination: "/etc/resolv.conf",
			Options:     []string{"rbind", "ro"},
		},
		{
			Type:        "bind",
			Source:      session.WorkspaceDir + "/.config/opencode/config.json",
			Destination: "/opt/opencode/.config/opencode/config.json",
			Options:     []string{"rbind", "ro"},
		},
	}

	cmd := r.h.RunBatchCommand(req)
	if os.Getenv("CHETTER_KATA_PREFLIGHT") == "1" && len(req.Command) == 0 && req.Prompt != "" {
		mounts = append(mounts,
			containerd.Mount{Type: "bind", Source: "/usr/bin/strace", Destination: "/usr/bin/strace", Options: []string{"rbind", "ro"}},
			containerd.Mount{Type: "bind", Source: "/lib/x86_64-linux-gnu", Destination: "/lib/x86_64-linux-gnu", Options: []string{"rbind", "ro"}},
			containerd.Mount{Type: "bind", Source: "/lib64", Destination: "/lib64", Options: []string{"rbind", "ro"}},
			containerd.Mount{Type: "bind", Source: "/tmp", Destination: "/host-tmp", Options: []string{"rbind", "rw"}},
		)
		cmd = kataPreflightCommand(cmd)
	} else if len(req.Command) == 0 && req.Prompt != "" {
		cmd = kataRunCommand(cmd)
	}
	slog.Info("starting Kata container", "taskID", req.TaskID, "command", cmd)
	out, err := r.containerd.RunKata(ctx, req.TaskID, req.AgentImage, mounts, env, taskNet.NetNSPath, cmd)
	if err != nil {
		slog.Error("kata run error", "taskID", req.TaskID, "err", err)
		r.publishStatusForRequest(req, "error", fmt.Sprintf("kata run: %v\n%s", err, out), nil)
		return
	}

	slog.Info("kata container exited", "taskID", req.TaskID)
	summary := out
	if len(req.Command) == 0 && req.Prompt != "" {
		summary = r.h.SummarizeBatchOutput(out)
	}
	r.publishStatusForRequest(req, "done", truncateSummary(summary), nil)
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func kataRunCommand(cmd []string) []string {
	return []string{"sh", "-c", "cd /tmp && exec " + shellQuoteArgs(cmd) + " < /dev/null"}
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

	if err := r.h.WaitForReady(ctx, baseURL, secret, 15*time.Second); err != nil {
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
		if export, exportErr := r.h.ReadSessionExport(session.WorkspaceDir, sid); exportErr != nil {
			slog.Warn("session export failed", "taskID", req.TaskID, "err", exportErr)
			r.publishEvent(req.TaskID, fmt.Sprintf("session export: %v", exportErr))
		} else {
			sessionExport = export
		}
	}
	if err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid, sessionExport)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
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

	configPath := r.h.ConfigFilePathGlobal(session.WorkspaceDir)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
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

	dockerArgs := []string{
		"run", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, containerPort),
		"-v", session.WorkspaceDir + ":/workspace",
		"-v", socketPath + ":" + socketPath,
		"-v", configPath + ":/opt/opencode/.config/opencode/config.json:ro",
		"-w", "/workspace",
		"-e", "TASK_ID=" + req.TaskID,
		"-e", "WORKSPACE=/workspace",
		"-e", "MCP_SOCKET_PATH=" + socketPath,
		"-e", "HOME=/opt/opencode",
		"-e", "XDG_CONFIG_HOME=/opt/opencode/.config",
		"-e", "XDG_DATA_HOME=/workspace/.local/share",
		"-e", "XDG_STATE_HOME=/workspace/.local/state",
		"-e", "XDG_CACHE_HOME=/workspace/.cache",
		"-e", "CHETTER_AGENT_NAME=" + req.Agent,
		"-e", "CHETTER_MODEL_ID=" + r.h.ResolvedModelID(req),
		"-e", "CHETTER_RUNNER_IMAGE=" + os.Getenv("CHETTER_RUNNER_IMAGE"),
		"-e", "CHETTER_RUNNER_IMAGE_DIGEST=" + os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST"),
	}

	for k, v := range r.h.Env("/opt/opencode/.config/opencode", secret) {
		key := k
		switch key {
		case "OPENCODE_CONFIG":
			key = "OPENCODE_CONFIG"
			v = "/opt/opencode/.config/opencode/config.json"
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
	dockerArgs = append(dockerArgs, r.h.Name())
	dockerArgs = append(dockerArgs, r.h.ServeArgs(containerPort)...)

	slog.Info("starting Docker container", "taskID", req.TaskID, "image", req.AgentImage, "hostPort", hostPort)
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

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPort)

	if err := r.h.WaitForReady(ctx, baseURL, secret, 15*time.Second); err != nil {
		logs, _ := exec.Command("docker", "logs", containerName).CombinedOutput()
		slog.Error("harness serve not ready in container", "taskID", req.TaskID, "err", err, "logs", string(logs))
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
		if export, exportErr := r.h.ReadSessionExport(session.WorkspaceDir, sid); exportErr != nil {
			slog.Warn("session export failed", "taskID", req.TaskID, "err", exportErr)
			r.publishEvent(req.TaskID, fmt.Sprintf("session export: %v", exportErr))
		} else {
			sessionExport = export
		}
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
