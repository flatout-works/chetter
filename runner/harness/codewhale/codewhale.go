package codewhale

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type CodeWhale struct {
	mu             sync.Mutex
	turnByID       map[string]string
	sessionExport  map[string]string
	publishFn      func(status, message string)
	tokenFn        func(usage task.TokenUsage)
	callbacksReady chan struct{}
	callbacksOnce  sync.Once
}

func New() *CodeWhale {
	return &CodeWhale{
		turnByID:       make(map[string]string),
		sessionExport:  make(map[string]string),
		callbacksReady: make(chan struct{}),
	}
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
	return codewhaleEnv(wsDir, secret, req)
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
	return cw.sendPrompt(ctx, baseURL, sessionID, secret, req, wsDir, timeout)
}

func (cw *CodeWhale) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return cw.abortSession(ctx, baseURL, sessionID, secret)
}

func (cw *CodeWhale) ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	return exportSession(ctx, baseURL, sessionID, secret)
}

func (cw *CodeWhale) ReadSessionExport(wsDir, sessionID string) (string, error) {
	if export := cw.getSessionExport(sessionID); export != "" {
		return export, nil
	}
	return readSessionExport(wsDir, sessionID)
}

func (cw *CodeWhale) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	cw.setCallbacks(publishFn, tokenFn)
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

func (cw *CodeWhale) setTurnID(sessionID, turnID string) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.turnByID[sessionID] = turnID
}

func (cw *CodeWhale) turnID(sessionID string) string {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.turnByID[sessionID]
}

func (cw *CodeWhale) setCallbacks(publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	cw.mu.Lock()
	cw.publishFn = publishFn
	cw.tokenFn = tokenFn
	cw.mu.Unlock()
	cw.callbacksOnce.Do(func() { close(cw.callbacksReady) })
}

func (cw *CodeWhale) callbacks() (func(status, message string), func(usage task.TokenUsage)) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.publishFn, cw.tokenFn
}

func (cw *CodeWhale) waitForCallbacks(ctx context.Context) (func(status, message string), func(usage task.TokenUsage), error) {
	select {
	case <-cw.callbacksReady:
		publishFn, tokenFn := cw.callbacks()
		return publishFn, tokenFn, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

func (cw *CodeWhale) setSessionExport(sessionID, export string) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.sessionExport[sessionID] = export
}

func (cw *CodeWhale) getSessionExport(sessionID string) string {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.sessionExport[sessionID]
}
