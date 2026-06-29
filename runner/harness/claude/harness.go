package claude

import (
	"context"
	"io"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type ClaudeCode struct{}

func New() *ClaudeCode {
	return &ClaudeCode{}
}

func (cc *ClaudeCode) Name() string { return "claude" }

func (cc *ClaudeCode) GenerateConfig(wsDir, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken, req, isLocal)
}

func (cc *ClaudeCode) ConfigFilePath(wsDir string) string {
	return wsDir + "/.claude/settings.json"
}

func (cc *ClaudeCode) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.claude/settings.local.json"
}

func (cc *ClaudeCode) Env(wsDir string, secret string, req task.TaskRequest) map[string]string {
	return claudeEnv(wsDir, req)
}

func (cc *ClaudeCode) ServeCommand(port int) []string {
	return claudeServeCommand(port)
}

func (cc *ClaudeCode) ServeArgsResume(port int) []string {
	return claudeServeArgsResume(port)
}

func (cc *ClaudeCode) ServerPassword() string {
	return generatePassword()
}

func (cc *ClaudeCode) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return waitForReady(ctx, baseURL, secret, timeout)
}

func (cc *ClaudeCode) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return createSession(ctx, baseURL, secret)
}

func (cc *ClaudeCode) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	return sendPrompt(ctx, baseURL, sessionID, secret, req, wsDir, timeout)
}

func (cc *ClaudeCode) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return abortSession(ctx, baseURL, sessionID, secret)
}

func (cc *ClaudeCode) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return exportSession(ctx, baseURL, sessionID, secret)
}

func (cc *ClaudeCode) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return readSessionExport(wsDir, sessionID)
}

func (cc *ClaudeCode) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	watchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn)
}

func (cc *ClaudeCode) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (cc *ClaudeCode) ResolvedModelID(req task.TaskRequest) string {
	return resolvedClaudeModelID(req)
}

func (cc *ClaudeCode) SupportsRpc() bool { return false }

func (cc *ClaudeCode) RpcCommand(req task.TaskRequest) []string { return nil }

func (cc *ClaudeCode) ServeArgs(port int) []string { return claudeServeCommand(port)[1:] }

func (cc *ClaudeCode) DockerConfigPath(wsDir string) string {
	return wsDir + "/.claude/mcp.json"
}
