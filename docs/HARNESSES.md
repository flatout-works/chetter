# Runner Harnesses

The Chetter runner drives AI coding agents inside containers. Each agent CLI is
wrapped by a **harness** - a Go strategy object that knows how to configure,
start, and communicate with that specific agent.

## Completion Detection

Each harness has a different way of knowing when an agent has finished. This is
the most fragile part of the runner — a missed completion signal causes the task
to hang until timeout.

### How Each Harness Detects Completion

| Harness | Mechanism | Vulnerable to livelock? |
|---------|-----------|--------------------------|
| **OpenCode** | Polls `/session/status` for idle + SSE `session.status` idle signal + heuristic fallbacks | Was — fixed |
| **Claude Code** | Synchronous HTTP response + successful terminal SSE result | No |
| **Codex** | Synchronous HTTP response + non-lossy terminal SSE signal | No |
| **CodeWhale** | Watches `GET /v1/threads/{id}/events` until `turn.completed`, with cursor reconnect | No (no continuation prompts) |
| **Pi** | `agent_settled`/`agent_end` events + guarded final-assistant EOF fallback | No |

### OpenCode Completion (multi-layered)

OpenCode is the only harness that uses **async prompt + status polling**, which
makes it the only harness vulnerable to completion detection failure. The fix
adds three layers of detection:

1. **SSE-based idle signal (primary)**: `WatchEvents` parses `session.status` SSE
   events. When the session transitions to idle/complete, it closes an `idleCh`
   that `waitForSessionIdle` also waits on — immediate notification without
   relying on the poll endpoint.

2. **Heuristic completion fallback**: Accepts multiple idle-like status values
   (`"idle"`, `"completed"`, `"finished"`, `"done"`). After 5 consecutive poll
   errors (~10s), treats the session as complete since the server may have
   cleaned it up.

3. **Watchdog livelock prevention**: The progress watchdog checks `isIdle()`
   before sending continuation prompts. If the session is already idle, it
   neither nudges nor fails — this breaks the livelock where continuation
   prompts kept the agent alive after it had finished.

The `CompletionAwareHarness` interface is implemented by OpenCode, Claude Code,
and Codex. It makes their terminal SSE signals independent of the primary HTTP
completion path, so a completed turn can survive a lost or hanging response.

### The Livelock Bug (Fixed)

Before the fix, if `/session/status` never reported `"idle"` — due to a status
field mismatch, persistent HTTP errors, or the async prompt handler not
updating session status — `waitForSessionIdle` blocked indefinitely. The
progress watchdog then sent continuation prompts every 2 minutes, and the
agent's responses reset the watchdog timer, creating a livelock that prevented
both natural timeout and completion detection. The task would burn tokens for
~16 minutes until the context deadline expired.

See task `task_dcdb6b26` for a real-world example: the agent completed its work,
created a PR, but the runner never detected completion.

### CodeWhale

CodeWhale waits for `turn.completed` on the thread SSE stream and does **not**
implement `SessionContinuable`. The waiter reconnects with the last observed
sequence cursor, deduplicates replayed events, and accepts only an explicit
`completed` terminal status. Exhausted reconnects fail clearly rather than
silently reporting success.

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

## Task MCP Endpoints

MCP endpoints let tasks connect to remote HTTP or SSE MCP servers. Each endpoint is defined as a YAML file in the config repo (see [MCP Endpoints](CONFIGURATION.md#mcp-endpoints)). At claim time the server resolves endpoint names to full definitions and sends them to the runner; the runner validates the bearer-token environment variable, prevents task env from overriding it, and imports it into the task container with `-e NAME` (bare name, no `=value`) so the value never appears in Docker arguments.

### Harness bearer token reference syntax

Each harness uses its native environment-variable reference syntax for the Authorization header:

| Harness | Bearer environment reference | Config format |
|---|---|---|
| OpenCode | `Bearer {env:NAME}` | `.opencode.json` `mcp` map |
| Claude Code | `Bearer ${NAME}` | `.mcp.json` `mcpServers` map |
| CodeWhale | `bearer_token_env_var: NAME` | `.codewhale/mcp.json` `servers` map |
| Pi | Native `bearerTokenEnv: NAME` | `.mcp.json` `mcpServers` map |
| Codex | Native `bearer_token_env_var: NAME` | `.codex/config.toml` `[mcp_servers.NAME]` |

CodeWhale and Pi use native fields (`bearer_token_env_var` / `bearerTokenEnv`) instead of header interpolation. OpenCode and Claude Code use their respective env-reference syntaxes in the `Authorization` header value.

### How endpoints are resolved

1. **Submit time**: the AgentSession configuration snapshot stores endpoint names (e.g. `["context"]`) as JSON in `mcp_endpoints`. The server validates that each name resolves to an active endpoint definition in the task's scope (global + team).
2. **Agent frontmatter**: if the selected agent definition declares `mcp_endpoints` in frontmatter, those names are merged into the AgentSession snapshot.
3. **Claim time**: the server loads the full endpoint definitions (URL, transport, headers, `bearer_token_env`) and sends them via the runner protobuf. The token value is never sent — only the environment-variable name.
4. **Runner startup**: the runner validates that each `bearer_token_env` variable is set in the runner environment, rejects task env overrides for those keys, and imports the values into the Docker container.
5. **Harness config generation**: the harness config writer (`runner/harness/mcpconfig`) generates native MCP server entries for each endpoint. HTTP profiles produce `type: "http"` (or no type for CodeWhale); SSE profiles produce `type: "sse"` or `transport: "sse"` depending on the harness.

Chetter does not proxy remote MCP traffic, translate HTTP/SSE, or reimplement MCP sessions. Harnesses connect directly to the configured remote endpoint.

### Scoping and isolation

MCP endpoints support global and team scope. Team-scoped endpoints are only available to tasks owned by that team. In Docker/gVisor mode, only the selected task's endpoint tokens are imported into the container, so a shared runner cannot see other teams' tokens.

**Local and plain Docker modes are single-trust:** in local (non-Docker) execution, all runner environment variables are visible to every task process. Neither local nor plain Docker execution is a task security boundary. Use gVisor for multi-team or untrusted-workload isolation.

### Pinned harness versions

The agent base image pins `codewhale@0.9.11` and `pi-mcp-adapter@2.27.0` because the MCP endpoint implementation relies on those verified config contracts (`bearer_token_env_var` for CodeWhale, `bearerTokenEnv` for Pi).

## Execution Models

Two dispatch paths exist in `runner_task.go`:

| Flag | Method | How the runner talks to the agent |
|------|--------|-----------------------------------|
| `SupportsRpc()` | `runRpcAgent` / `runDockerRpcAgent` | stdin/stdout JSONL subprocess (Pi only) |
| (default) | `runLocalAgent` / `runDockerAgent` | HTTP API (start serve, poll ready, create session, send prompt, watch SSE) |

Dispatch order: **RPC → Serve**. All harnesses without RPC use serve mode.

Per-task Docker containers are the standard execution model; gVisor provides the
security boundary when isolation is required. RPC mode runs as a subprocess of
the runner (no gVisor), available only for Pi.

### Runner MCP authentication boundary

Every execution receives a new 256-bit capability for its runner-owned MCP
server. The server requires that bearer token on every request and listens on
loopback in local mode or the runner's routable Docker/Kubernetes interface in
container modes. Chetter injects the header into the OpenCode, Claude Code,
CodeWhale, Codex, and Pi MCP configurations; files containing the token are
forced to owner-only permissions.

The same capability authenticates the runner-wide Chetter MCP relay. Only
currently active executions are registered. Closing the execution MCP server
revokes relay access, and the relay replaces the capability with its upstream
credential while forwarding task and execution fencing headers. The locally
minted capability is never serialized to the control plane and is redacted
from runner-published summaries, errors, exports, and artifact strings.

## Selection

Harness can be set at two levels:

### Runner Default

The runner's YAML config sets a default harness for tasks that don't specify
one explicitly:

```yaml
# runner.yaml
execution:
  harness: pi                               # opencode (default), claude-code, pi, codewhale, codex
  container_memory: 4g                      # optional Docker memory limit (e.g. 4g, 2048m)
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
- Server receives `harness` in the MCP input and stores it in the AgentSession
  configuration snapshot.
- Runner claims an ExecutionAttempt -> proto `Task.Harness` field -> `harnessFor(req.Harness)`
  selects the right harness strategy
- Each task picks its harness independently; concurrent tasks can use different
  harnesses on the same runner

The `selectHarnessByName()` function in `runner/internal/controller/runner.go`
maps the string to a constructor.

## OpenCode (default)

**Binary:** `opencode` (installed via opencode.ai/install)
**Execution model:** Serve (HTTP API on localhost)

OpenCode runs as a local HTTP server binding to `0.0.0.0` (required
for gVisor port-mapped traffic). The runner starts `opencode serve`,
polls `/config` for readiness, creates a session, dispatches the
prompt asynchronously via `POST /session/{id}/prompt_async`, polls
`GET /session/status` every 2s until the session is idle, fetches the
last assistant message from `GET /session/{id}/message`, watches an
SSE event stream (text deltas and significant events accumulated and
batched in 3-second windows; urgent events published immediately),
and reads the session export from the on-disk SQLite database
(`opencode.db`).

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

### Permission policy (headless)

Claude's interactive approval prompts are unreachable in `-p` mode, so every
"ask" becomes a silent "deny". Its Bash matcher also splits compound commands
on `;`/`&&` and rejects redirects and `$(...)` unless every segment matches an
allow rule, which makes per-binary allowlists unreliable mid-task. Chetter
therefore runs Claude with `--permission-mode dontAsk` — the documented
fail-closed mode for headless runs — and writes `.claude/settings.json` (mode
`0600`) with a bare `"Bash"` allow plus a deny list that keeps container
escapes and host-control commands blocked (`docker`, `systemctl`,
`journalctl`, `sudo`, `ssh`, `scp`, `pkill`, `kill`, `shutdown`, `reboot`) and
`AskUserQuestion` denied. Deny rules are evaluated before allow; the gVisor
task container remains the actual security boundary. The agent also gets
`--add-dir /tmp` for scratch space, matching OpenCode's `/tmp` external
directory allow.

### MCP loading (strict)

In `-p` mode Claude Code loads a cloned repository's `.mcp.json` **without any
approval**. To stop a malicious repository from registering its own MCP
server, `GenerateConfig` additionally writes the runner-owned server map
(runner-bridge, chetter relay, team endpoints) to
`.claude/chetter-mcp.json` (mode `0600`), and the serve proxy passes it via
`--mcp-config <file> --strict-mcp-config` whenever the file exists. With
strict mode, project `.mcp.json`, `~/.claude.json`, and every other MCP source
are ignored — only the runner-generated servers load. Invalid or skipped
entries surface in the `system/init` event's `mcp_server_errors` array, which
the proxy converts into a terminal task error. `.mcp.json` is still written
for local/interactive debugging; `enabledMcpjsonServers` in settings remains
for local mode and is inert under strict mode.

### Why chosen

Claude Code is Anthropic's official CLI. The serve-proxy brings it to parity with
OpenCode's serve mode: per-task Docker isolation with gVisor, live progress via
SSE, session export, and resume support.

### Pros

- Official Anthropic CLI, well-maintained
- Per-task Docker isolation with gVisor (via serve-proxy)
- SSE streaming events for live progress, including subagent text
  (`--forward-subagent-text`) and hook lifecycle (`--include-hook-events`)
- Session resume support (`--resume`) plus idle-session continuation
  (`POST /session/{id}/continue`) used by the progress watchdog
- Session status probe (`GET /session/{id}/status`) distinguishes hung
  generations from agents that finished their turn
- Agent and skill definition injection (`.claude/agents/`, `.claude/skills/`)
- Session export from JSONL files
- Clean stream-json output format with cache token accounting
- Permission system (allow/deny lists in settings.json, `dontAsk` mode)
- MCP support built-in, restricted to the runner-generated server list via
  `--strict-mcp-config`
- Configurable turn cap (`CHETTER_CLAUDE_MAX_TURNS`, default 500) and optional
  spend ceiling (`CHETTER_CLAUDE_MAX_BUDGET_USD`)

### Cons

- Anthropic-compatible endpoints only (the Anthropic message contract is the
  sole wire protocol)
- Mid-turn steering is not available (continuation happens between turns)
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
  Chetter already provides. No `bypassPermissions` hack needed. The `--approve`
  flag is a project-trust override (`projectTrustOverride`), not a tool
  approval bypass: it loads the project's `.pi/` extensions and skills without
  a trust prompt. In headless RPC mode there are no interactive approvals
  anyway — extension UI requests (`select`/`confirm`/`input`/`editor`) are
  auto-answered with `cancelled: true` by the runner.
- **MCP via pi-mcp-adapter** - reads standard `.mcp.json` format (same
  as Claude Code), supports stdio transport for the chetter mcp-bridge
- **Session export** via `get_messages` command - full conversation
  including thinking blocks and tool results
- **`--offline` mode** - clean container behavior, no version checks
  or telemetry
- **Self-extensible** - extensions, skills (agentskills.io standard),
  prompt templates. Skills can be fed via `--skill` flag; Chetter also
  injects Git-backed skill definitions into `.pi/skills/`.
- **Agent personas** - a task agent definition is written to
  `.pi/agent/system-prompt.md` and passed via `--system-prompt`, composing
  with Pi's coding-assistant default prompt.
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

## CodeWhale

**Binary:** `codewhale` (npm: `codewhale`)
**Execution model:** Serve (HTTP/SSE runtime API)

CodeWhale runs `codewhale app-server --http`, creates a durable runtime thread
with `POST /v1/threads`, starts work with `POST /v1/threads/{id}/turns`, and
follows `GET /v1/threads/{id}/events?since_seq=0` until `turn.completed`.
Abort maps to `POST /v1/threads/{id}/turns/{turn_id}/interrupt`.

### Why chosen

CodeWhale is an open-model-first harness with a documented runtime API,
provider breadth, MCP support, and a local HTTP/SSE control surface that fits
Chetter's existing serve-mode runner model.

### Pros

- HTTP/SSE runtime API with durable threads and turns
- Bearer-token local runtime auth
- Provider support includes DeepSeek, Z.ai, OpenAI-compatible gateways, Anthropic, Xiaomi MiMo, Ollama/vLLM/SGLang, and others
- MCP config support via `.codewhale/mcp.json`
- Turn interrupt endpoint for graceful cancellation
- MIT license

### Cons

- Newer integration than OpenCode/Claude/Pi
- No stable markdown export endpoint yet; Chetter uses an observed-turn markdown fallback with local state files as a backup
- Model/provider routing depends on CodeWhale's config/env resolver

### When to use

When you want an open-model-first runtime with strong provider breadth and a
native HTTP/SSE control API.

## Codex

**Binary:** `codex` (npm: `@openai/codex`)
**Execution model:** Serve (Codex App Server behind `codex-serve-proxy`)

Codex App Server exposes a JSON-RPC thread and turn API over stdio. Chetter's
proxy starts it inside each task container, creates or resumes the real Codex
thread, and translates streamed JSON-RPC notifications into the runner's
HTTP/SSE harness contract. This preserves per-task Docker and gVisor isolation
while retaining the Codex thread ID needed for follow-up sessions.

### Pros

- Official OpenAI coding CLI with durable thread and turn IDs
- Streaming assistant deltas, tool progress, token usage, and turn completion
- Graceful cancellation through `turn/interrupt`
- Native Streamable HTTP MCP support through `.codex/config.toml`
- Workspace-scoped configuration and state, including markdown session export
- Apache 2.0 license

### Constraints

- The App Server protocol is currently experimental, so Chetter pins the CLI
  package version and covers the bridge's protocol handling with unit tests.
- Initial deployment uses `OPENAI_API_KEY` with a Responses API-compatible
  provider. Interactive ChatGPT login is intentionally not used in task
  containers.
- Chetter rejects interactive approval and elicitation requests. Codex runs
  with `workspace-write` permissions and the outer task container remains the
  security boundary.

### When to use

Use Codex when you need OpenAI models, durable follow-up sessions, and native
MCP support inside Chetter's isolated task containers.

## Comparison

| Feature | OpenCode | Claude Code | Pi | CodeWhale | Codex |
|---------|----------|-------------|-----|-----------|-------|
| Execution model | Serve (HTTP) | Serve (proxy) | RPC (subprocess) | Serve (HTTP/SSE) | Serve (App Server proxy) |
| Streaming | SSE events | SSE events | JSONL events | SSE events | JSON-RPC bridged to SSE |
| Abort | Kill process | SIGINT→SIGTERM | `abort` command | Turn interrupt endpoint | `turn/interrupt` |
| Steering | No | Continuation via resume (idle sessions) | `steer` / `follow_up` | Runtime API supports steer; Chetter does not expose it yet | Runtime API supports steer; Chetter does not expose it yet |
| Model switching | Per-session config | Per-task flag | `set_model` mid-session | Per-thread/turn model | Per-thread/turn model |
| MCP support | Built-in | Built-in (strict runner-owned config) | via pi-mcp-adapter | Built-in `.codewhale/mcp.json` | Built-in `.codex/config.toml` |
| Session export | SQLite DB | JSONL files | `get_messages` → markdown | Observed-turn markdown fallback | Observed-turn markdown export |
| Per-task Docker isolation | Yes (gVisor) | Yes (gVisor) | No | Yes (gVisor) | Yes (gVisor) |
| Provider breadth | Multiple | Anthropic-compatible endpoints | 30+ | Broad multi-provider/open-model | Responses API-compatible providers |
| Permission system | Config-based | Settings-based (`dontAsk`) | None (container-reliant) | Runtime approval/sandbox policy | `workspace-write` plus no interactive approvals |
| Agent definitions | Injected (`.config/opencode/agent/`) | Injected (`.claude/agents/`) | Injected (`.pi/agent/system-prompt.md` + `--system-prompt`) | N/A | N/A |
| Skill definitions | Injected (`.config/opencode/skill/`) | Injected (`.claude/skills/`) | Injected (`.pi/skills/`) | N/A | N/A |
| Session status probe | Yes | Yes (proxy `GET /status`) | N/A | N/A | N/A |
| Watchdog continuation | Yes | Yes (proxy `POST /continue`, resume-based) | N/A | N/A | N/A |
| Cache token accounting | Yes | Yes | No | No | No |
| Thinking levels | N/A | N/A | off/minimal/low/medium/high/xhigh | Model/provider dependent | Model/provider dependent |
| Per-task selection | Yes (harness field) | Yes (harness field) | Yes (harness field) | Yes (harness field) | Yes (harness field) |
| License | Apache 2.0 | Proprietary (CLI) | MIT | MIT | Apache 2.0 |

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
5. Install the harness binary in `runner/images/base/Dockerfile`, and the
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
| MiMo Code | MIT | `mimo` | XiaomiMiMo OpenCode fork with memory/goals/compose workflows. Needs serve/API compatibility investigation. |
| Reasonix | MIT | `reasonix run "task"` | DeepSeek cost-optimization specialist. Go static binary, reads `.mcp.json` natively. DeepSeek-only. Pin `@next` never `@latest`. |
