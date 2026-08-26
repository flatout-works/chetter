package controller

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/agentenv"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
)

// hostWorkspaceDirForContainer maps the runner-side workspace directory to the
// path the docker daemon must bind-mount. It also warns when the mapped host
// path does not exist, which indicates a HOST_WORKSPACE_ROOT misconfiguration:
// docker silently creates missing mount paths as empty directories, so the
// task container would see an empty /workspace with none of the runner-written
// config files.
func hostWorkspaceDirForContainer(workspaceRoot, workspaceDir string) (string, error) {
	hostWorkspaceDir, err := agentenv.HostWorkspaceDir(workspaceDir, workspaceRoot)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(hostWorkspaceDir); statErr != nil || !info.IsDir() {
		slog.Warn("host workspace mount path does not exist; container will see an empty /workspace",
			"workspace_dir", workspaceDir, "host_workspace_dir", hostWorkspaceDir,
			"hint", "check HOST_WORKSPACE_ROOT: it must be the host path of RUNNER_WORKSPACE_ROOT")
	}
	return hostWorkspaceDir, nil
}

func (r *Runner) dockerServeArgs(req task.TaskRequest, workspaceDir, containerName string, h harness.ServeHarness, serveCmd []string, bindAddr string, hostPort int, gvisor bool, netName, runnerIP, secret string) ([]string, error) {
	hostWorkspaceDir, err := hostWorkspaceDirForContainer(r.cfg.Runner.WorkspaceRoot, workspaceDir)
	if err != nil {
		return nil, err
	}
	entrypoint := serveCmd[0]
	dockerArgs := []string{
		"run", "-d",
		"--entrypoint", entrypoint,
		"--name", containerName,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--label", "chetter.runner_id=" + r.runnerID,
		"--label", "chetter.task_id=" + req.TaskID,
		"--label", "chetter.execution_id=" + executionKey(req),
		"--label", "chetter.agent_session_id=" + req.AgentSessionID,
		"--label", "chetter.user_prompt_id=" + req.UserPromptID,
	}
	if gvisor {
		dockerArgs = append(dockerArgs, "--runtime", "runsc")
		dockerArgs = append(dockerArgs, "--dns", runnerIP)
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
	}
	dockerArgs = appendContainerLimits(dockerArgs, r.cfg.Execution, req)
	dockerArgs = append(dockerArgs, "--network", netName)
	dockerArgs = append(dockerArgs, "-p", fmt.Sprintf("%s:%d:%d", harnessPublishBindAddr(bindAddr, gvisor), hostPort, containerPortForServe))
	dockerArgs = append(dockerArgs,
		"-v", hostWorkspaceDir+":/workspace",
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
	for _, value := range agentenv.GitIdentityEnv(req, "/workspace") {
		dockerArgs = append(dockerArgs, "-e", value)
	}
	dockerArgs = append(dockerArgs, "-e", "HOME=/workspace")
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
		if agentenv.IsManagedEnv(k, req) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	dockerArgs = agentenv.AppendDockerManagedEnvironment(dockerArgs, req)
	if gvisor {
		dockerArgs = append(dockerArgs, "--hostname", "0.0.0.0")
	}
	if shouldPullAgentImage(req.AgentImage) {
		dockerArgs = append(dockerArgs, "--pull=always")
	}
	dockerArgs = append(dockerArgs, req.AgentImage)
	return append(dockerArgs, serveCmd[1:]...), nil
}

const containerPortForServe = 9999

// appendContainerLimits adds Docker resource-limit flags for the memory, CPU,
// and PID limits. Runner-level limits are hard safety caps; per-task limits can
// only tighten them. Each flag is only emitted when the corresponding value is
// set, so unset limits leave container behavior unchanged. The same limits are
// applied to serve, resume, and RPC containers so a single misbehaving task
// cannot exhaust the host.
func appendContainerLimits(args []string, exec config.ExecutionConfig, req task.TaskRequest) []string {
	mem := exec.ContainerMemory
	if req.MaxMemoryMB > 0 {
		taskMem := fmt.Sprintf("%dm", req.MaxMemoryMB)
		configuredBytes, _ := config.ParseMemoryBytes(mem)
		taskBytes := int64(req.MaxMemoryMB) << 20
		if configuredBytes == 0 || taskBytes < configuredBytes {
			mem = taskMem
		}
	}
	if mem != "" {
		args = append(args, "--memory", mem, "--memory-swap", mem)
	}
	cpu := exec.ContainerCPU
	if req.MaxCPU > 0 && (cpu == 0 || float64(req.MaxCPU) < cpu) {
		cpu = float64(req.MaxCPU)
	}
	if cpu > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(cpu, 'f', -1, 64))
	}
	if pids := exec.ContainerPIDs; pids > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(pids))
	}
	return args
}
