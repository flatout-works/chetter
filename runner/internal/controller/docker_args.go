package controller

import (
	"fmt"
	"os"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/agentenv"
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
	if mem := r.cfg.Execution.ContainerMemory; mem != "" {
		dockerArgs = append(dockerArgs, "--memory", mem, "--memory-swap", mem)
	}
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
	for _, value := range agentenv.GitIdentityEnv(req, workspaceDir) {
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
