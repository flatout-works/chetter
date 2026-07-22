package codex

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/task"
)

// Codex drives the Codex App Server through codex-serve-proxy, which adapts
// its JSON-RPC transport to Chetter's common HTTP/SSE harness contract.
type Codex struct {
	mu              sync.Mutex
	idleCh          <-chan struct{}
	onIdle          func()
	terminalSummary string
	terminalErr     error
	terminalSet     bool
}

var _ harness.ServeHarness = (*Codex)(nil)

func New() *Codex { return &Codex{} }

func (c *Codex) Name() string { return "codex" }

func (c *Codex) GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken, req)
}

func (c *Codex) Env(wsDir, secret string, req task.TaskRequest) map[string]string {
	return codexEnv(wsDir, secret)
}

func (c *Codex) ServeCommand(port int) []string { return codexServeCommand(port) }

func (c *Codex) ServerPassword() string { return generatePassword() }

func (c *Codex) WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return waitForReady(ctx, baseURL, secret, timeout)
}

func (c *Codex) CreateSession(ctx context.Context, baseURL, secret string) (string, error) {
	return createSession(ctx, baseURL, secret)
}

func (c *Codex) SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	idleCh := c.idleCh
	c.mu.Unlock()
	return sendPrompt(ctx, baseURL, sessionID, secret, req, timeout, idleCh, c.completionResult)
}

func (c *Codex) AbortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	return abortSession(ctx, baseURL, sessionID, secret)
}

func (c *Codex) ReadSessionExport(wsDir, sessionID string) (string, error) {
	return readSessionExport(wsDir, sessionID)
}

func (c *Codex) WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	c.mu.Lock()
	onIdle := c.onIdle
	c.mu.Unlock()
	watchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn, func(summary string, err error) {
		c.mu.Lock()
		c.terminalSummary = summary
		c.terminalErr = err
		c.terminalSet = true
		c.mu.Unlock()
		if onIdle != nil {
			onIdle()
		}
	})
}

func (c *Codex) SetCompletionContext(_ string, idleCh <-chan struct{}, onIdle func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.idleCh = idleCh
	c.onIdle = onIdle
	c.terminalSummary = ""
	c.terminalErr = nil
	c.terminalSet = false
}

func (c *Codex) completionResult() (string, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.terminalSet {
		return "", nil, false
	}
	if c.terminalErr != nil {
		return c.terminalSummary, fmt.Errorf("codex terminal event: %w", c.terminalErr), true
	}
	return c.terminalSummary, nil, true
}

func (c *Codex) PipeOutput(taskID, stream string, reader io.Reader) {
	pipeOutput(taskID, stream, reader)
}

func (c *Codex) ResolvedModelID(req task.TaskRequest) string { return resolvedModelID(req) }
