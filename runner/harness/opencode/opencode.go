package opencode

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type OpenCode struct {
	mu                   sync.Mutex
	sessID               string
	idleCh               <-chan struct{}
	onIdle               func()
	chetterMCPConfigJSON string
}

func New() *OpenCode {
	return &OpenCode{}
}

func (oc *OpenCode) Name() string { return "opencode" }

func (oc *OpenCode) GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	if err := GenerateConfigForTask(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken, true, req, isLocal); err != nil {
		return err
	}
	// Project configuration is loaded after OPENCODE_CONFIG. Re-apply the
	// runner-owned Chetter entry through the final config source so a cloned
	// repository cannot redirect the agent to another MCP server.
	oc.chetterMCPConfigJSON = chetterMCPConfigContent(chetterMCPURL, chetterMCPToken)
	return nil
}

func (oc *OpenCode) ConfigFilePath(wsDir string) string {
	return wsDir + "/.opencode.json"
}

func (oc *OpenCode) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.config/opencode/config.json"
}

func (oc *OpenCode) Env(wsDir string, secret string, _ task.TaskRequest) map[string]string {
	env := map[string]string{
		"OPENCODE_CONFIG":          wsDir + "/.opencode.json",
		"OPENCODE_SERVER_PASSWORD": secret,
	}
	if oc.chetterMCPConfigJSON != "" {
		env["OPENCODE_CONFIG_CONTENT"] = oc.chetterMCPConfigJSON
	}
	return env
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
	oc.mu.Lock()
	idleCh := oc.idleCh
	oc.mu.Unlock()
	return sendPromptAndWait(ctx, baseURL, sessionID, secret, req, wsDir, timeout, idleCh)
}

func (oc *OpenCode) ContinueSession(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string) error {
	return continueSession(ctx, baseURL, sessionID, secret, req, wsDir)
}

func (oc *OpenCode) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return abortSession(ctx, baseURL, sessionID, secret)
}

func (oc *OpenCode) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return exportSession(ctx, baseURL, sessionID, secret)
}

func (oc *OpenCode) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	oc.mu.Lock()
	sessionID := oc.sessID
	onIdle := oc.onIdle
	oc.mu.Unlock()
	watchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn, sessionID, onIdle)
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

func (oc *OpenCode) SetCompletionContext(sessionID string, idleCh <-chan struct{}, onIdle func()) {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	oc.sessID = sessionID
	oc.idleCh = idleCh
	oc.onIdle = onIdle
}
