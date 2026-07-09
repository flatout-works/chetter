package opencode

import (
	"context"
	"io"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type OpenCode struct{}

func New() *OpenCode {
	return &OpenCode{}
}

func (oc *OpenCode) Name() string { return "opencode" }

func (oc *OpenCode) GenerateConfig(wsDir, runnerMCPURL string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfigForTask(wsDir, runnerMCPURL, true, req, isLocal)
}

func (oc *OpenCode) ConfigFilePath(wsDir string) string {
	return wsDir + "/.opencode.json"
}

func (oc *OpenCode) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.config/opencode/config.json"
}

func (oc *OpenCode) Env(wsDir string, secret string, _ task.TaskRequest) map[string]string {
	return map[string]string{
		"OPENCODE_CONFIG":          wsDir + "/.opencode.json",
		"OPENCODE_SERVER_PASSWORD": secret,
	}
}

func (oc *OpenCode) ServeCommand(port int) []string {
	return opencodeServeCommand(port)
}

func (oc *OpenCode) ServeArgs(port int) []string {
	return opencodeServeArgs(port)
}

func (oc *OpenCode) ServeArgsResume(port int) []string {
	return opencodeServeArgsResume(port)
}

func (oc *OpenCode) ServerPassword() string {
	return generatePassword()
}

func (oc *OpenCode) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return waitForServeReady(ctx, baseURL, secret, timeout)
}

func (oc *OpenCode) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return createOpenCodeSession(ctx, baseURL, secret)
}

func (oc *OpenCode) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	return sendPromptAndWait(ctx, baseURL, sessionID, secret, req, wsDir, timeout)
}

func (oc *OpenCode) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return abortSession(ctx, baseURL, sessionID, secret)
}

func (oc *OpenCode) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return exportSession(ctx, baseURL, sessionID, secret)
}

func (oc *OpenCode) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	watchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn)
}

func (oc *OpenCode) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (oc *OpenCode) SupportsRpc() bool { return false }

func (oc *OpenCode) RpcCommand(req task.TaskRequest) []string { return nil }

func (oc *OpenCode) ResolvedModelID(req task.TaskRequest) string {
	return resolvedChetterModelID(req)
}

func (oc *OpenCode) DockerConfigPath(wsDir string) string {
	return wsDir + "/.opencode.json"
}
