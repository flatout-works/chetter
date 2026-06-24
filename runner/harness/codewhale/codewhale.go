package codewhale

import (
	"context"
	"io"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type CodeWhale struct{}

func New() *CodeWhale {
	return &CodeWhale{}
}

func (cw *CodeWhale) Name() string { return "codewhale" }

func (cw *CodeWhale) GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken, req, isLocal)
}

func (cw *CodeWhale) ConfigFilePath(wsDir string) string {
	return wsDir + "/.codewhale/config.toml"
}

func (cw *CodeWhale) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.codewhale/settings.json"
}

func (cw *CodeWhale) Env(wsDir string, secret string, req task.TaskRequest) map[string]string {
	return codewhaleEnv(wsDir, req)
}

func (cw *CodeWhale) ServeCommand(port int) []string {
	return codewhaleServeCommand(port)
}

func (cw *CodeWhale) ServeArgsResume(port int) []string {
	return codewhaleServeArgsResume(port)
}

func (cw *CodeWhale) ServerPassword() string {
	return generatePassword()
}

func (cw *CodeWhale) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return waitForReady(ctx, baseURL, secret, timeout)
}

func (cw *CodeWhale) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return createSession(ctx, baseURL, secret)
}

func (cw *CodeWhale) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	return sendPrompt(ctx, baseURL, sessionID, secret, req, wsDir, timeout)
}

func (cw *CodeWhale) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return abortSession(ctx, baseURL, sessionID, secret)
}

func (cw *CodeWhale) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return exportSession(ctx, baseURL, sessionID, secret)
}

func (cw *CodeWhale) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return readSessionExport(wsDir, sessionID)
}

func (cw *CodeWhale) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	watchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn)
}

func (cw *CodeWhale) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (cw *CodeWhale) ResolvedModelID(req task.TaskRequest) string {
	provider, model := codewhaleModelFields(req)
	return provider + "/" + model
}

func (cw *CodeWhale) SupportsRpc() bool { return false }

func (cw *CodeWhale) RpcCommand(req task.TaskRequest) []string { return nil }

func (cw *CodeWhale) ServeArgs(port int) []string { return codewhaleServeCommand(port)[1:] }

func (cw *CodeWhale) DockerConfigPath(wsDir string) string {
	return wsDir + "/.codewhale/mcp.json"
}
