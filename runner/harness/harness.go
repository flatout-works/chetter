package harness

import (
	"context"
	"io"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type Harness interface {
	Name() string

	GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error

	Env(wsDir string, secret string, req task.TaskRequest) map[string]string
	ResolvedModelID(req task.TaskRequest) string
}

// OutputPiper exposes harness process output to the runner event stream.
type OutputPiper interface {
	PipeOutput(taskID, stream string, reader io.Reader)
}

// ServeHarness is implemented by harnesses driven through an HTTP session.
type ServeHarness interface {
	Harness
	OutputPiper

	ServeCommand(port int) []string
	ServerPassword() string
	WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error
	CreateSession(ctx context.Context, baseURL, secret string) (string, error)
	SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error)
	AbortSession(ctx context.Context, baseURL, sessionID, secret string) error
	ReadSessionExport(wsDir, sessionID string) (string, error)
	WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage))
}

// RPCHarness is implemented by harnesses driven through a JSONL subprocess.
type RPCHarness interface {
	Harness
	OutputPiper

	RpcCommand(req task.TaskRequest) []string
}

// SessionContinuable is implemented by harnesses that can enqueue a follow-up
// prompt while the original request is still being monitored.
type SessionContinuable interface {
	ContinueSession(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string) error
}

// CompletionAwareHarness is implemented by harnesses whose WatchEvents can
// detect session completion via SSE events and signal it to the
// polling-based completion detection in SendPrompt. This breaks the
// single-point-of-failure in poll-only completion detection.
type CompletionAwareHarness interface {
	SetCompletionContext(sessionID string, idleCh <-chan struct{}, onIdle func())
}
