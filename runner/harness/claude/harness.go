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

func (cc *ClaudeCode) SupportsServe() bool { return false }

func (cc *ClaudeCode) SupportsRpc() bool { return false }

func (cc *ClaudeCode) GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, _ task.TaskRequest, isLocal bool) error {
	return GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken, isLocal)
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

func (cc *ClaudeCode) ServeArgs(port int) []string {
	return nil
}

func (cc *ClaudeCode) ServeArgsResume(port int) []string {
	return nil
}

func (cc *ClaudeCode) ServerPassword() string {
	return ""
}

func (cc *ClaudeCode) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return nil
}

func (cc *ClaudeCode) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return "", nil
}

func (cc *ClaudeCode) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	return "", nil
}

func (cc *ClaudeCode) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return nil
}

func (cc *ClaudeCode) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return "", nil
}

func (cc *ClaudeCode) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return "", nil
}

func (cc *ClaudeCode) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
}

func (cc *ClaudeCode) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (cc *ClaudeCode) RunBatchCommand(req task.TaskRequest) []string {
	return buildClaudeCommand(req)
}

func (cc *ClaudeCode) RpcCommand(req task.TaskRequest) []string { return nil }

func (cc *ClaudeCode) SummarizeBatchOutput(raw string) string {
	return summarizeStreamJSON(raw)
}

func (cc *ClaudeCode) ResolvedModelID(req task.TaskRequest) string {
	return resolvedClaudeModelID(req)
}
