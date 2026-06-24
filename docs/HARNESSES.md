# Runner Harnesses

The Chetter runner drives AI coding agents inside containers. Each agent CLI is
wrapped by a **harness** - a Go strategy object that knows how to configure,
start, and communicate with that specific agent.

## Harness Interface

All harnesses implement `harness.Harness` in `runner/harness/harness.go`:

```go
type Harness interface {
    Name() string
    GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req TaskRequest, isLocal bool) error
    ConfigFilePath(wsDir string) string
    ConfigFilePathGlobal(wsDir string) string
    Env(wsDir string, secret string, req TaskRequest) map[string]string

    // Serve mode (HTTP API)
    ServeCommand(port int) []string
    ServeArgsResume(port int) []string
    ServerPassword() string
    WaitForReady(ctx, baseURL, secret, timeout) error
    CreateSession(ctx, baseURL, secret) (string, error)
    SendPrompt(ctx, baseURL, sessionID, secret, req, wsDir, timeout) (string, error)
    AbortSession(ctx, baseURL, sessionID, secret) error
    ExportSession(ctx, baseURL, sessionID, secret) (string, error)
    ReadSessionExport(wsDir, sessionID) (string, error)
    WatchEvents(ctx, taskID, baseURL, secret, publishFn, tokenFn)
    PipeOutput(taskID, stream string, reader io.Reader)

    ResolvedModelID(req TaskRequest) string

    // RPC mode (Pi only — kept for transitional RPC → serve migration)
    SupportsRpc() bool
    RpcCommand(req TaskRequest) []string

    // Docker
    DockerConfigPath(wsDir string) string
}
```

**Key changes from the old interface:**
- `ServeCommand(port)` replaces `ServeArgs(port)` — returns `[binary, arg...]` so the
  runner knows the Docker entrypoint independently of the harness name.
- `RunBatchCommand()`, `SummarizeBatchOutput()`, `SupportsServe()` removed — batch
  mode is gone; all harnesses use serve mode (HTTP API).
- `DockerConfigPath()` added — each harness tells the runner where its MCP config
  file lives (no more hardcoded `.opencode.json`).
- `SupportsRpc()` / `RpcCommand()` remain for Pi's RPC mode (will be removed
  when Pi gets its own serve-proxy).

## Execution Models

Two dispatch paths exist in `runner_task.go`:

| Flag | Method | How the runner talks to the agent |
|------|--------|-----------------------------------|
| `SupportsRpc()` | `runRpcAgent` / `runDockerRpcAgent` | stdin/stdout JSONL subprocess (Pi only) |
| (default) | `runLocalAgent` / `runDockerAgent` | HTTP API (start serve, poll ready, create session, send prompt, watch SSE) |

Dispatch order: **RPC → Serve**. All harnesses without RPC use serve mode.

Per-task Docker isolation (gVisor, separate containers) is the standard execution
model. RPC mode runs as a subprocess of the runner (no gVisor), available only for Pi.

## Selection

Harness can be set at two levels:

### Runner Default

The runner's YAML config sets a default harness for tasks that don't specify
one explicitly:

```yaml
# runner.yaml
execution:
  harness: pi  # opencode (default), claude-code, pi
```

In Docker, the entrypoint reads `CHETTER_HARNESS` and writes the YAML.

### Per-Task Override

Tasks submitted via MCP can override the harness per-task:

```
chetter_submit_task prompt="..." harness="pi"
chetter_create_trigger name="..." trigger_type="cron" cron_expr="@daily" harness="pi" ...
```

The `harness` field is optional. When omitted or empty, the runner's
`execution.harness` config is used as the default.

**How it flows:**
- Server receives `harness` in the MCP input -> embeds it as
  `__chetter_harness` in the task's env JSON
- Runner claims task -> proto `Task.Harness` field -> `harnessFor(req.Harness)`
  selects the right harness strategy
- Each task picks its harness independently; concurrent tasks can use different
  harnesses on the same runner

The `selectHarnessByName()` function in `runner/internal/controller/runner.go`
maps the string to a constructor.

## OpenCode (default)

**Binary:** `opencode` (installed via opencode.ai/install)
**Execution model:** Serve (HTTP API on localhost)

OpenCode runs as a local HTTP server. The runner starts `opencode serve`,
polls `/config` for readiness, creates a session, sends the prompt via
`POST /session/{id}/message`, watches an SSE event stream, and reads the
session export from the on-disk SQLite database (`opencode.db`).

### Why chosen

OpenCode is the original harness Chetter was built around. Its serve mode
provides the richest integration: SSE streaming, session persistence, and
per-task Docker container isolation with gVisor.

### Pros

- Full HTTP API with session management
- SSE streaming events for live progress
- Per-task Docker isolation with gVisor (strongest sandboxing)
- Session export from SQLite DB (full conversation history)
- MCP support built-in (runner-bridge + chetter MCP)
- Configurable providers, agents, skills, permissions
- Active development

### Cons

- Complex to maintain: serve lifecycle, HTTP client, SQLite reader
- No steering (cannot inject information mid-task)
- No abort command (must kill the process)
- Provider set is smaller than Pi
- System prompt overhead (bloated instructions noted by community)

### When to use

Default for most workloads. Best when per-task Docker isolation or the HTTP
session API is needed.

## Claude Code

**Binary:** `claude` (npm: `@anthropic-ai/claude-code`)
**Execution model:** Serve (HTTP API via claude-serve-proxy)

Claude Code runs via a **serve-proxy** — a thin Go HTTP server (`claude-serve-proxy`)
that wraps Claude's headless CLI mode. The proxy starts as the Docker entrypoint,
accepts HTTP requests from the runner, and delegates to `claude -p ...` in a
subprocess. Claude's `--output-format stream-json` output is parsed and streamed
as SSE events. Sessions persist as JSONL files in the workspace (bind-mounted),
enabling resume via `claude --resume`.

### Why chosen

Claude Code is Anthropic's official CLI. The serve-proxy brings it to parity with
OpenCode's serve mode: per-task Docker isolation with gVisor, live progress via
SSE, session export, and resume support.

### Pros

- Official Anthropic CLI, well-maintained
- Per-task Docker isolation with gVisor (via serve-proxy)
- SSE streaming events for live progress
- Session resume support (`--resume`)
- Session export from JSONL files
- Clean stream-json output format
- Permission system (allow/deny lists in settings.json)
- MCP support built-in (.claude/mcp.json)

### Cons

- Anthropic-only (no other providers)
- No mid-task steering or follow-up
- No mid-session model switching
- Requires serve-proxy binary (extra maintenance)
- Abort is SIGINT→SIGTERM escalation (no graceful HTTP abort in Claude CLI)

### When to use

When you need Claude models with full Docker/gVisor isolation. Matches OpenCode's
serve-mode capabilities.

## Pi

**Binary:** `pi` (npm: `@earendil-works/pi-coding-agent`)
**Execution model:** RPC (long-lived stdin/stdout JSONL subprocess)

Pi runs as a long-lived subprocess in RPC mode (`pi --mode rpc`). The runner
communicates via bidirectional JSONL: sends commands on stdin (prompt, abort,
set_model, get_state, get_messages), reads events on stdout (message_update,
tool_execution, agent_end, extension_ui_request).

### Why chosen

Pi's RPC mode is the most capable non-HTTP integration of any coding agent
CLI. It provides streaming events, abort, steering, model switching, and
session queries - all over a simple stdin/stdout pipe. Pi also supports 30+
providers including ZAI (Chetter's default GLM model), and its MIT license
and supply-chain rigor make it suitable for production.

### Pros

- **30+ providers** including ZAI, DeepSeek, Google, OpenAI, Anthropic,
  Groq, Cerebras, xAI, OpenRouter, regional China providers (Xiaomi MiMo,
  MiniMax, Moonshot/Kimi, Ant Ling). Best provider breadth of any harness.
- **RPC mode** gives full lifecycle control: streaming text deltas, tool
  execution events, abort, steering (inject info mid-task), follow-up
  (chain instructions), model switching mid-session
- **Thinking level control** (`off/minimal/low/medium/high/xhigh`) maps
  to Chetter's `variant_id`
- **No built-in permission system** - relies on containerization, which
  Chetter already provides. No `bypassPermissions` hack needed.
- **MCP via pi-mcp-adapter** - reads standard `.mcp.json` format (same
  as Claude Code), supports stdio transport for the chetter mcp-bridge
- **Session export** via `get_messages` command - full conversation
  including thinking blocks and tool results
- **`--offline` mode** - clean container behavior, no version checks
  or telemetry
- **Self-extensible** - extensions, skills (agentskills.io standard),
  prompt templates. Skills can be fed via `--skill` flag.
- **MIT license** with supply-chain rigor (pinned deps, shrinkwrap,
  OIDC trusted publishing)
- **Session tree model** - JSONL with branching/forking. Future: task
  retry from a previous session branch.

### Cons

- **No per-task Docker isolation** - runs as subprocess of the runner,
  not in a separate gVisor container (same as Claude Code)
- **Third-party MCP dependency** - `pi-mcp-adapter` (MIT, 99k downloads)
  must be pre-installed in the image. If abandoned, we'd fork or replace.
- **No built-in subagents** - available via `pi-subagents` extension
  (spawns child Pi processes, resource-intensive in containers)
- **Node >= 22.19.0** required - base image needs Node 22 (Claude and
  OpenCode work with Node 18+, so upgrade is safe)
- **No startup event in RPC mode** - readiness must be probed via
  `get_state` command (adds one round-trip)
- **Extension UI requests** can block the agent - must auto-respond
  with `cancelled:true` in headless mode
- **JSONL framing caveat** - must split on `\n` only, not use
  readline-style splitting (U+2028/U+2029 are valid in JSON strings)

### When to use

When you need provider flexibility (especially ZAI/GLM, DeepSeek, or
regional providers), streaming control, or steering. The best harness
for long-running tasks that may need course correction.

## Comparison

| Feature | OpenCode | Claude Code | Pi |
|---------|----------|-------------|-----|
| Execution model | Serve (HTTP) | Serve (proxy) | RPC (subprocess) |
| Streaming | SSE events | SSE events | JSONL events |
| Abort | Kill process | SIGINT→SIGTERM | `abort` command |
| Steering | No | No | `steer` / `follow_up` |
| Model switching | Per-session config | Per-task flag | `set_model` mid-session |
| MCP support | Built-in | Built-in | via pi-mcp-adapter |
| Session export | SQLite DB | JSONL files | `get_messages` → markdown |
| Per-task Docker isolation | Yes (gVisor) | Yes (gVisor) | No |
| Provider breadth | Multiple | Anthropic only | 30+ |
| Permission system | Config-based | Settings-based | None (container-reliant) |
| Thinking levels | N/A | N/A | off/minimal/low/medium/high/xhigh |
| Per-task selection | Yes (harness field) | Yes (harness field) | Yes (harness field) |
| License | Apache 2.0 | Proprietary (CLI) | MIT |

## Adding a New Harness

1. If the harness binary has a native HTTP serve mode (like OpenCode):
   - Create `runner/harness/<name>/` with a struct implementing `Harness`.
   - Implement all serve-mode methods directly.
2. If the harness is CLI-only (no HTTP serve mode):
   - Build a serve-proxy binary in `runner/cmd/<name>-serve-proxy/main.go` that
     wraps the CLI behind the standard HTTP API (see `claude-serve-proxy` for reference).
   - Create `runner/harness/<name>/` with HTTP client methods that talk to the proxy.
3. Add `case "<name>": return <pkg>.New()` in `selectHarnessByName()` in
   `runner/internal/controller/runner.go`.
4. If the harness needs env var passthrough, add keys to
   `runnerOwnedEnvKeys()` and `isRunnerOwnedEnv()` in
   `runner/internal/controller/runner_task.go`.
5. Install the harness binary in `runner/Dockerfile.chetter-base`, and the
   serve-proxy binary (if applicable).
6. Add `Harness` to MCP input schemas in `internal/service/tools.go`
   (`SubmitTaskInput`, `CreateTriggerInput`, `UpdateTriggerInput`).
7. Add `Harness` to `store.ScheduleInput` and `store.ScheduleRecord`
   in `internal/store/store.go`.
8. Wire the field through `CreateTrigger`, `UpdateTrigger`, and
   `runSchedule` in `internal/service/service.go`.
9. Update `docs/HARNESSES.md` with the new harness section.
10. Run `make check` in `runner/` (vet + lint + test).

## Future Harness Candidates

| Harness | License | CLI | Notes |
|---------|---------|-----|-------|
| Aider | Apache 2.0 | `aider --message "..." --yes` | Model-agnostic, git-native, pip install. Simplest possible harness. |
| Goose | Apache 2.0 | `goose session ...` | Rust single binary, 15+ providers, 70+ MCP extensions. Linux Foundation governed. |
| Codex | Closed | `codex` | OpenAI's CLI. Already stubbed in `selectHarness()`. Needs non-interactive mode investigation. |
| Reasonix | MIT | `reasonix run "task"` | DeepSeek cost-optimization specialist. Go static binary, reads `.mcp.json` natively. DeepSeek-only. Pin `@next` never `@latest`. |
