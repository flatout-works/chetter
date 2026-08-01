package controller

import (
	"fmt"
	"os"
	"strconv"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/agentenv"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func (r *Runner) dockerServeArgs(req task.TaskRequest, workspaceDir, containerName string, h harness.ServeHarness, serveCmd []string, bindAddr string, hostPort int, gvisor bool, netName, runnerIP, secret string) []string {
	entrypoint := serveCmd[0]
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
		dockerArgs = append(dockerArgs, "--dns", runnerIP)
		dockerArgs = append(dockerArgs, gvisorHostAliases()...)
	}
	dockerArgs = appendContainerLimits(dockerArgs, r.cfg.Execution)
	dockerArgs = append(dockerArgs, "--network", netName)
	dockerArgs = append(dockerArgs, "-p", fmt.Sprintf("%s:%d:%d", harnessPublishBindAddr(bindAddr, gvisor), hostPort, containerPortForServe))
	dockerArgs = append(dockerArgs,
		"-v", agentenv.HostWorkspaceDir(workspaceDir)+":/workspace",
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
	return append(dockerArgs, serveCmd[1:]...)
}

const containerPortForServe = 9999

// appendContainerLimits adds Docker resource-limit flags for the memory, CPU,
// and PID limits configured in exec. Each flag is only emitted when the
// corresponding value is set, so unset limits leave container behavior
// unchanged. The same limits are applied to serve, resume, and RPC containers
// so a single misbehaving task cannot exhaust the host.
func appendContainerLimits(args []string, exec config.ExecutionConfig) []string {
	if mem := exec.ContainerMemory; mem != "" {
		args = append(args, "--memory", mem, "--memory-swap", mem)
	}
	if cpu := exec.ContainerCPU; cpu > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(cpu, 'f', -1, 64))
	}
	if pids := exec.ContainerPIDs; pids > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(pids))
	}
	return args
}
