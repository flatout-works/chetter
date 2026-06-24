package pi

import (
	"context"
	"io"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type Pi struct{}

func New() *Pi {
	return &Pi{}
}

func (p *Pi) Name() string { return "pi" }

func (p *Pi) SupportsServe() bool { return false }

func (p *Pi) SupportsRpc() bool { return true }

func (p *Pi) GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, _ task.TaskRequest, isLocal bool) error {
	return GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken, isLocal)
}

func (p *Pi) ConfigFilePath(wsDir string) string {
	return wsDir + "/.pi/settings.json"
}

func (p *Pi) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.pi/agent/settings.json"
}

func (p *Pi) Env(wsDir string, secret string, _ task.TaskRequest) map[string]string {
	return map[string]string{
		"PI_CODING_AGENT_DIR":         wsDir + "/.pi/agent",
		"PI_CODING_AGENT_SESSION_DIR": wsDir + "/.pi/sessions",
		"PI_OFFLINE":                  "1",
		"PI_SKIP_VERSION_CHECK":       "1",
		"PI_TELEMETRY":                "0",
	}
}

func (p *Pi) ServeArgs(port int) []string       { return nil }
func (p *Pi) ServeArgsResume(port int) []string { return nil }

func (p *Pi) ServerPassword() string { return "" }

func (p *Pi) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return nil
}

func (p *Pi) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return "", nil
}

func (p *Pi) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	return "", nil
}

func (p *Pi) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return nil
}

func (p *Pi) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return "", nil
}

func (p *Pi) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return readSessionExport(wsDir)
}

func (p *Pi) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
}

func (p *Pi) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (p *Pi) RunBatchCommand(req task.TaskRequest) []string { return nil }

func (p *Pi) SummarizeBatchOutput(raw string) string { return "" }

func (p *Pi) RpcCommand(req task.TaskRequest) []string { return buildRPCCommand(req) }

func (p *Pi) ResolvedModelID(req task.TaskRequest) string { return resolvedModelID(req) }
