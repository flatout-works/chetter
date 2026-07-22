package claude

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type ClaudeCode struct {
	mu              sync.Mutex
	sessionID       string
	idleCh          <-chan struct{}
	onIdle          func()
	watchDone       chan struct{}
	terminalSummary string
}

func New() *ClaudeCode {
	return &ClaudeCode{}
}

func (cc *ClaudeCode) Name() string { return "claude" }

func (cc *ClaudeCode) GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken, req, isLocal)
}

func (cc *ClaudeCode) ConfigFilePath(wsDir string) string {
	return wsDir + "/.claude/settings.json"
}

func (cc *ClaudeCode) ConfigFilePathGlobal(wsDir string) string {
	return wsDir + "/.claude/settings.local.json"
}

func (cc *ClaudeCode) Env(wsDir string, secret string, req task.TaskRequest) map[string]string {
	return claudeEnv(wsDir, secret, req)
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
	cc.mu.Lock()
	idleCh := cc.idleCh
	watchDone := cc.watchDone
	cc.mu.Unlock()

	type promptResult struct {
		summary string
		err     error
	}
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	resultCh := make(chan promptResult, 1)
	go func() {
		summary, err := sendPrompt(requestCtx, baseURL, sessionID, secret, req, wsDir, timeout)
		resultCh <- promptResult{summary: summary, err: err}
	}()

	if idleCh == nil {
		result := <-resultCh
		return result.summary, result.err
	}

	select {
	case <-idleCh:
		cancelRequest()
		terminal, _ := cc.completedSummary(idleCh)
		return terminal, nil
	case result := <-resultCh:
		if result.err != nil {
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-idleCh:
				terminal, _ := cc.completedSummary(idleCh)
				return terminal, nil
			case <-ctx.Done():
				if terminal, ok := cc.completedSummary(idleCh); ok {
					return terminal, nil
				}
				return "", result.err
			case <-timer.C:
				return "", result.err
			}
		}

		// Wait until the watcher has consumed the terminal result event so token
		// usage is complete before the runner publishes terminal metadata.
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-idleCh:
		case <-watchDone:
		case <-timer.C:
		case <-ctx.Done():
			if !channelClosed(idleCh) {
				return "", ctx.Err()
			}
		}
		if terminal, ok := cc.completedSummary(idleCh); ok && result.summary == "" {
			return terminal, nil
		}
		return result.summary, nil
	case <-ctx.Done():
		if terminal, ok := cc.completedSummary(idleCh); ok {
			return terminal, nil
		}
		return "", ctx.Err()
	}
}

func channelClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
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
	cc.mu.Lock()
	sessionID := cc.sessionID
	onIdle := cc.onIdle
	watchDone := cc.watchDone
	cc.mu.Unlock()
	if watchDone != nil {
		defer close(watchDone)
	}
	watchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn, sessionID, func(summary string) {
		cc.mu.Lock()
		cc.terminalSummary = summary
		cc.mu.Unlock()
		if onIdle != nil {
			onIdle()
		}
	})
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

func (cc *ClaudeCode) SetCompletionContext(sessionID string, idleCh <-chan struct{}, onIdle func()) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.sessionID = sessionID
	cc.idleCh = idleCh
	cc.onIdle = onIdle
	cc.watchDone = make(chan struct{})
	cc.terminalSummary = ""
}

func (cc *ClaudeCode) completedSummary(idleCh <-chan struct{}) (string, bool) {
	if idleCh == nil {
		return "", false
	}
	select {
	case <-idleCh:
		cc.mu.Lock()
		defer cc.mu.Unlock()
		return cc.terminalSummary, true
	default:
		return "", false
	}
}
