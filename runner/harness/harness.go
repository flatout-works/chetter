package harness

import (
	"context"
	"io"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

type Harness interface {
	Name() string

	GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error

	ConfigFilePath(wsDir string) string
	ConfigFilePathGlobal(wsDir string) string

	Env(wsDir string, secret string, req task.TaskRequest) map[string]string

	// Serve mode (local + Docker).
	ServeArgs(port int) []string
	ServeArgsResume(port int) []string
	ServerPassword() string
	WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error
	CreateSession(ctx context.Context, baseURL, secret string) (string, error)
	SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error)
	ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error)
	ReadSessionExport(wsDir, sessionID string) (string, error)
	WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string))

	// Output piping for serve mode stdout/stderr.
	PipeOutput(taskID, stream string, reader io.Reader)

	// Batch mode.
	RunBatchCommand(req task.TaskRequest) []string
	SummarizeBatchOutput(raw string) string

	// ResolvedModelID returns the provider-qualified model identifier.
	ResolvedModelID(req task.TaskRequest) string

	// SupportsServe returns true if the harness has an HTTP serve mode
	// (WaitForReady, CreateSession, SendPrompt, WatchEvents, ExportSession).
	// Harnesses without serve mode fall back to batch execution.
	SupportsServe() bool

	// SupportsRpc returns true if the harness can be driven via a long-lived
	// stdin/stdout JSONL subprocess (RPC mode). When true, the runner uses
	// runRpcAgent instead of runBatchAgent/runLocalAgent. See docs/HARNESSES.md.
	SupportsRpc() bool

	// RpcCommand returns the argv to start the harness in RPC mode.
	// Only called when SupportsRpc() returns true.
	RpcCommand(req task.TaskRequest) []string
}
