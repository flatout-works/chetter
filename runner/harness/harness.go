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

	ConfigFilePath(wsDir string) string
	ConfigFilePathGlobal(wsDir string) string

	Env(wsDir string, secret string, req task.TaskRequest) map[string]string

	// Serve mode (local + Docker).
	ServeCommand(port int) []string
	ServeArgsResume(port int) []string
	ServerPassword() string
	WaitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error
	CreateSession(ctx context.Context, baseURL, secret string) (string, error)
	SendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error)
	AbortSession(ctx context.Context, baseURL, sessionID, secret string) error
	ExportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error)
	ReadSessionExport(wsDir, sessionID string) (string, error)
	WatchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage))

	// Output piping for serve mode stdout/stderr.
	PipeOutput(taskID, stream string, reader io.Reader)

	// ResolvedModelID returns the provider-qualified model identifier.
	ResolvedModelID(req task.TaskRequest) string

	// SupportsRpc returns true if the harness can be driven via a long-lived
	// stdin/stdout JSONL subprocess (RPC mode). When true, the runner uses
	// runRpcAgent instead of runLocalAgent.
	SupportsRpc() bool

	// RpcCommand returns the argv to start the harness in RPC mode.
	// Only called when SupportsRpc() returns true.
	RpcCommand(req task.TaskRequest) []string

	// DockerConfigPath returns the MCP config file path inside the container.
	// The runner rewrites MCP URLs in this file for Docker networking.
	DockerConfigPath(wsDir string) string
}
