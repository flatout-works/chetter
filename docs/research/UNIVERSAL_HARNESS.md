# Universal Harness Architecture

## Goal

Supported serve-mode harnesses (OpenCode, Claude Code, CodeWhale) run inside Docker/gVisor containers
with per-task isolation. Each exposes the same HTTP API. The runner has one execution
path: `runDockerAgent` / `runLocalAgent`.

Batch mode and RPC mode are removed from the harness interface. Harnesses that lack
a native HTTP serve mode get a thin **serve-proxy** Go binary
that wraps the CLI and implements the HTTP API.

## Architecture

```
┌───────────────────────────────────────────────┐
│            Runner (runDockerAgent)             │
│  HTTP API: /session, /message, /event (SSE),  │
│           /abort, /export                      │
└─────────────────┬─────────────────────────────┘
                  │
     ┌────────────┼────────────┐
     │            │            │
┌────▼─────┐ ┌───▼──────┐ ┌───▼──────┐
│ opencode │ │ claude-  │ │  pi-     │
│  serve   │ │ serve-   │ │ serve-   │
│ (native) │ │ proxy    │ │ proxy    │
└──────────┘ └────┬─────┘ └────┬─────┘
                  │            │
           ┌──────▼──────┐ ┌──▼────────┐
           │  claude -p  │ │ pi --mode │
           │  subprocess │ │ rpc       │
           └─────────────┘ └───────────┘
```

## Simplified Harness Interface

```go
type Harness interface {
    Name() string
    GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req TaskRequest, isLocal bool) error
    ConfigFilePath(wsDir string) string
    ConfigFilePathGlobal(wsDir string) string
    Env(wsDir string, secret string, req TaskRequest) map[string]string

    // Serve mode
    ServeCommand(port int) []string  // [binary, arg, arg...]
    ServeArgsResume(port int) []string
    ServerPassword() string
    WaitForReady(ctx, baseURL, secret, timeout) error
    CreateSession(ctx, baseURL, secret) (string, error)
    SendPrompt(ctx, baseURL, sessionID, secret, req, wsDir, timeout) (string, error)
    AbortSession(ctx, baseURL, sessionID, secret) error
    ExportSession(ctx, baseURL, sessionID, secret) (string, error)
    ReadSessionExport(wsDir, sessionID string) (string, error)
    WatchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn)
    PipeOutput(taskID, stream string, reader io.Reader)
    ResolvedModelID(req TaskRequest) string

    // Docker
    DockerConfigPath(wsDir string) string
}
```

**Removed from interface:**
- `RunBatchCommand()` / `SummarizeBatchOutput()` — batch mode is gone
- `SupportsServe()` / `SupportsRpc()` / `RpcCommand()` — all harnesses serve
- `ServeArgs()` — replaced by `ServeCommand()` which includes the binary

**Added:**
- `ServeCommand(port int) []string` — returns `[binary, arg...]` so the runner
  knows the Docker entrypoint (element 0) independently of the harness name.
- `DockerConfigPath(wsDir string) string` — tells the runner where the MCP config
  file lives (replaces hardcoded `".opencode.json"`).

## Unified HTTP API

All serve-proxy binaries implement the same endpoints:

| Endpoint | Purpose |
|----------|---------|
| `GET /health` | Liveness check (WaitForReady polls this) |
| `POST /session` | Create session, returns `{"session_id": "..."}` |
| `POST /session/{id}/message` | Send prompt, starts agent, returns initial response |
| `GET /event` | SSE stream of progress events (text, tool_use, token_usage, error) |
| `POST /session/{id}/abort` | Abort running session |
| `GET /session/{id}/export` | Export session transcript as markdown |

## Serve-Proxy Pattern

Each proxy is a standalone Go binary in `runner/cmd/<name>-serve-proxy/main.go`.
It:

1. Starts an HTTP server on the given port
2. Translates incoming HTTP requests into CLI invocations
3. Streams CLI output as SSE events
4. Reads session files for export

### claude-serve-proxy

- `POST /session/{id}/message` → runs `claude -p "<prompt>" --output-format stream-json --verbose --include-partial-messages ...` in a goroutine
- `GET /event` → parses stream-json lines from claude stdout, emits SSE
- `POST /session/{id}/abort` → SIGINT → SIGTERM escalation on claude process
- `GET /session/{id}/export` → reads `.claude/projects/**/session-*.jsonl`, renders markdown
- Resume: body includes `resume_session_id` → `claude --resume <id> -p "..."`

### pi-serve-proxy (future)

- Translates HTTP ↔ Pi RPC JSONL commands
- `POST /session/{id}/message` → sends `prompt` RPC command
- `GET /event` → translates Pi RPC events to SSE
- `POST /session/{id}/abort` → sends `abort` RPC command
- `GET /session/{id}/export` → sends `get_messages` RPC command
- Future: `POST /session/{id}/steer`, `POST /session/{id}/set-model`

## Implementation Steps

1. Add `ServeCommand()` and `DockerConfigPath()` to Harness interface
2. Clean up Docker hardcoding in `runner_task.go` (entrypoint, config path, env overrides, `opencode.LogMCPStatus`, `opencode.db` search)
3. Update OpenCode harness (`ServeCommand`, `DockerConfigPath`, remove batch/RPC stubs)
4. Build `claude-serve-proxy` binary (`runner/cmd/claude-serve-proxy/main.go`)
5. Rewrite Claude harness for serve mode (HTTP methods, remove batch)
6. Remove batch mode from runner (`runBatchAgent`, `readBatchOutput`, dispatch)
7. Update Pi harness (keep RPC for now, add `ServeCommand` stub, remove batch stubs)
8. Install `claude-serve-proxy` in runner Docker image

## What Stays the Same

- OpenCode harness works exactly as today (`ServeCommand` prepends `"opencode"`)
- Pi harness keeps working via RPC mode, no immediate change
- Runner config, task submission, trigger system — unchanged
- `runLocalAgent` and `runRpcAgent` remain for local/Pi execution during migration

## What Gets Removed

- `RunBatchCommand()` and `SummarizeBatchOutput()` from interface and all harnesses
- `SupportsServe()`, `SupportsRpc()`, `RpcCommand()` from interface
- `runBatchAgent()`, `readBatchOutput()`, `eventDetail()` from runner
- `runDockerRpcAgent()` from runner (when Pi moves to serve)
- Hardcoded `opencode` assumptions in `runDockerAgent`
- All `--bare`, `--permission-mode bypassPermissions`, `--max-turns` CLI flags
  (moved into claude-serve-proxy)

## Execution Dispatch (Final State)

```
execution mode == "docker" → runDockerAgent
execution mode == "local"  → runLocalAgent
```

No `SupportsRpc()`, `SupportsServe()`, or batch fallbacks.
