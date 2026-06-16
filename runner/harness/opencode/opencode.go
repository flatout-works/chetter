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

func (oc *OpenCode) GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, isLocal bool) error {
	return GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken, true, isLocal)
}

func (oc *OpenCode) ConfigFilePath(wsDir string) string {
	return wsDir + "/.opencode.json"
}

func (oc *OpenCode) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.config/opencode/config.json"
}

func (oc *OpenCode) Env(wsDir string, secret string) map[string]string {
	return map[string]string{
		"OPENCODE_CONFIG":          wsDir + "/.opencode.json",
		"OPENCODE_SERVER_PASSWORD": secret,
	}
}

func (oc *OpenCode) ServeArgs(port int) []string {
	return opencodeServeArgs(port)
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

func (oc *OpenCode) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return exportSession(ctx, baseURL, sessionID, secret)
}

func (oc *OpenCode) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string)) {
	watchEvents(ctx, taskID, baseURL, secret, publishFn)
}

func (oc *OpenCode) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (oc *OpenCode) RunBatchCommand(req task.TaskRequest) []string {
	return resolveCommand(req)
}

func (oc *OpenCode) SummarizeBatchOutput(raw string) string {
	return summarizeJSONL(raw)
}

func (oc *OpenCode) ResolvedModelID(req task.TaskRequest) string {
	return resolvedChetterModelID(req)
}
