# Runner Harnesses

The Chetter runner drives AI coding agents inside containers. Each agent CLI is
wrapped by a **harness** - a Go strategy object that knows how to configure,
start, and communicate with that specific agent.

## Harness Interface

All harnesses implement `harness.Harness` in `runner/harness/harness.go`:

```go
type Harness interface {
    Name() string
    GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, isLocal bool) error
    ConfigFilePath(wsDir string) string
    ConfigFilePathGlobal(wsDir string) string
    Env(wsDir string, secret string) map[string]string

    // Serve mode (HTTP)
    ServeArgs(port int) []string
    ServerPassword() string
    WaitForReady(ctx, baseURL, secret, timeout) error
    CreateSession(ctx, baseURL, secret) (string, error)
    SendPrompt(ctx, baseURL, sessionID, secret, req, wsDir, timeout) (string, error)
    ExportSession(ctx, baseURL, sessionID, secret) (string, error)
    ReadSessionExport(wsDir, sessionID) (string, error)
    WatchEvents(ctx, taskID, baseURL, secret, publishFn)

    // Output piping
    PipeOutput(taskID, stream, reader)

    // Batch mode (one-shot subprocess)
    RunBatchCommand(req) []string
    SummarizeBatchOutput(raw) string

    // RPC mode (long-lived stdin/stdout JSONL subprocess)
    SupportsRpc() bool
    RpcCommand(req) []string

    ResolvedModelID(req) string
    SupportsServe() bool
}
```

## Execution Models

Three dispatch paths exist in `runner_task.go`, selected via capability flags:

| Flag | Method | How the runner talks to the agent |
|------|--------|-----------------------------------|
| `SupportsServe()` | `runLocalAgent` / `runDockerAgent` | HTTP API (start serve, poll ready, create session, send prompt, watch SSE) |
| `SupportsRpc()` | `runRpcAgent` | stdin/stdout JSONL subprocess (send commands, read events) |
| neither | `runBatchAgent` | One-shot subprocess (capture stdout, parse, exit) |

Dispatch order: **RPC -> Serve -> Batch**. A harness that supports both RPC and
serve will use RPC.

Per-task Docker isolation (gVisor, separate containers) is only available in
serve mode. RPC and batch harnesses run as subprocesses of the runner itself,
relying on the runner container for isolation.

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
**Execution model:** Batch (one-shot subprocess, stream-json output)

Claude Code runs as a one-shot CLI invocation. The runner builds a command
like `claude --bare -p <prompt> --output-format stream-json --model <model>
--permission-mode bypassPermissions --max-turns 100`, captures stdout, and
parses stream-json lines for text deltas.

### Why chosen

Claude Code is Anthropic's official CLI. It was the first alternative harness
added to Chetter, providing Claude model access without OpenCode.

### Pros

- Official Anthropic CLI, well-maintained
- Clean stream-json output format
- Simple integration (no HTTP server, no SQLite)
- Permission system (allow/deny lists in settings.json)
- MCP support built-in (.claude/mcp.json)
- System prompt override via `--system-prompt`

### Cons

- Anthropic-only (no other providers)
- No session persistence (batch mode only)
- No session export (returns empty string)
- No steering or follow-up
- No abort command (must kill process)
- Requires `--permission-mode bypassPermissions` hack for autonomy
- No per-task Docker isolation (subprocess of runner)

### When to use

When you need Claude models specifically. Simpler than OpenCode but less
capable for long-running or interactive tasks.

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
| Execution model | Serve (HTTP) | Batch (one-shot) | RPC (subprocess) |
| Streaming | SSE events | stream-json lines | JSONL events |
| Abort | Kill process | Kill process | `abort` command |
| Steering | No | No | `steer` / `follow_up` |
| Model switching | Per-session config | Per-task flag | `set_model` mid-session |
| MCP support | Built-in | Built-in | via pi-mcp-adapter |
| Session export | SQLite DB | None | `get_messages` -> markdown |
| Per-task Docker isolation | Yes (gVisor) | No | No |
| Provider breadth | Multiple | Anthropic only | 30+ |
| Permission system | Config-based | `bypassPermissions` | None (container-reliant) |
| Thinking levels | N/A | N/A | off/minimal/low/medium/high/xhigh |
| Per-task selection | Yes (harness field) | Yes (harness field) | Yes (harness field) |
| License | Apache 2.0 | Proprietary (CLI) | MIT |

## Adding a New Harness

1. Create `runner/harness/<name>/` with a struct implementing all
   `Harness` interface methods. Use Claude's `harness.go` as a template
   for batch harnesses, or Pi's for RPC harnesses.
2. Add `case "<name>": return <pkg>.New()` in `selectHarnessByName()` in
   `runner/internal/controller/runner.go`.
3. If the harness needs env var passthrough, add keys to
   `runnerOwnedEnvKeys()` and `isRunnerOwnedEnv()` in
   `runner/internal/controller/runner_task.go`.
4. Install the harness binary in `runner/Dockerfile.chetter-base`, the final
   runner image layer, and `runner/images/minimal/Dockerfile` if applicable.
5. Add `Harness` to MCP input schemas in `internal/service/tools.go`
   (`SubmitTaskInput`, `CreateTriggerInput`, `UpdateTriggerInput`).
6. Add `Harness` to `store.ScheduleInput` and `store.ScheduleRecord`
   in `internal/store/store.go`.
7. Wire the field through `CreateTrigger`, `UpdateTrigger`, and
   `runSchedule` in `internal/service/service.go`.
8. Add harness column to `chetter_schedules` via `ensureScheduleMetadataColumns`
   in `internal/store/store.go`.
9. Update `docs/HARNESSES.md` with the new harness's section.
6. Run `make check` in `runner/` (vet + lint + test).

## Future Harness Candidates

| Harness | License | CLI | Notes |
|---------|---------|-----|-------|
| Aider | Apache 2.0 | `aider --message "..." --yes` | Model-agnostic, git-native, pip install. Simplest possible harness. |
| Goose | Apache 2.0 | `goose session ...` | Rust single binary, 15+ providers, 70+ MCP extensions. Linux Foundation governed. |
| Codex | Closed | `codex` | OpenAI's CLI. Already stubbed in `selectHarness()`. Needs non-interactive mode investigation. |
| Reasonix | MIT | `reasonix run "task"` | DeepSeek cost-optimization specialist. Go static binary, reads `.mcp.json` natively. DeepSeek-only. Pin `@next` never `@latest`. |
