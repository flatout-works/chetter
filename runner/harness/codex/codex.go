package codex

import (
	"context"
	"io"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

// Codex drives the Codex App Server through codex-serve-proxy, which adapts
// its JSON-RPC transport to Chetter's common HTTP/SSE harness contract.
type Codex struct{}

func New() *Codex { return &Codex{} }

func (c *Codex) Name() string { return "codex" }

func (c *Codex) GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken, req)
}

func (c *Codex) ConfigFilePath(wsDir string) string {
	return wsDir + "/.codex/config.toml"
}

func (c *Codex) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.codex/config.toml"
}

func (c *Codex) Env(wsDir, secret string, req task.TaskRequest) map[string]string {
	return codexEnv(wsDir, secret)
}

func (c *Codex) ServeCommand(port int) []string { return codexServeCommand(port) }

func (c *Codex) ServeArgsResume(port int) []string { return codexServeCommand(port)[1:] }

func (c *Codex) ServerPassword() string { return generatePassword() }

func (c *Codex) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return waitForReady(ctx, baseURL, secret, timeout)
}

func (c *Codex) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return createSession(ctx, baseURL, secret)
}

func (c *Codex) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	return sendPrompt(ctx, baseURL, sessionID, secret, req, timeout)
}

func (c *Codex) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return abortSession(ctx, baseURL, sessionID, secret)
}

func (c *Codex) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return exportSession(ctx, baseURL, sessionID, secret)
}

func (c *Codex) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return readSessionExport(wsDir, sessionID)
}

func (c *Codex) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	watchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn)
}

func (c *Codex) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (c *Codex) ResolvedModelID(req task.TaskRequest) string { return resolvedModelID(req) }

func (c *Codex) SupportsRpc() bool { return false }

func (c *Codex) RpcCommand(req task.TaskRequest) []string { return nil }

func (c *Codex) DockerConfigPath(wsDir string) string {
	return wsDir + "/.codex/config.toml"
}
