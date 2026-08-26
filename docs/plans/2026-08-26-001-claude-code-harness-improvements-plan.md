# Claude Code Harness Improvements Plan

**Date:** 2026-08-26
**Branch:** `feat/claude-code-harness-improvements`
**Worktree:** `/home/gokr/git/chetter-claude-harness`
**Status:** Proposed

## Background

A full audit of the Claude Code harness (`runner/harness/claude/` + `runner/cmd/claude-serve-proxy/`)
compared it against the OpenCode harness (our most mature integration) and the official
Claude Code documentation (CLI reference, env-vars, hooks, permissions, settings-reference,
MCP, streaming result schemas; docs current as of CLI v2.1.24x era). The harness is solid —
serve-proxy architecture, SSE streaming, native-session mapping, resume, abort escalation,
and the recent provider-error hardening — but has parity gaps with OpenCode and misses
several documented capabilities that matter for headless fleet operation.

Baseline commits already on `main` (prerequisites, included in this branch):

- `bd28707` — agent base image harness version bumps (claude-code pinned 2.1.245)
- `034568a` — claude-serve-proxy provider-error preservation, api_retry metadata,
  MCP init-error detection, missing-result fail-closed

## Goals

1. Close the security and correctness gaps that only affect headless `-p` operation.
2. Reach feature parity with the OpenCode harness: agent/skill injection, session
   status probing, watchdog continuation, full token accounting.
3. Improve live observability of Claude Code tasks (subagent activity, lifecycle).
4. Expose operational knobs for MCP output size and model output limits.

## Non-goals

- Replacing the serve-proxy with the TypeScript/Python Agent SDK. The Go proxy stays.
- Claude Code's own sandboxed-Bash settings: gVisor remains the security boundary.
- Mid-flight prompt steering via `--input-format stream-json` in this change (spiked
  below; resume-based continuation is the scoped fallback).

## Key doc facts driving this plan

| Fact | Source | Impact |
|---|---|---|
| Result error arm subtypes are `error_max_turns`, `error_during_execution`, `error_max_budget_usd`, `error_max_structured_output_retries` and carry `errors: string[]` instead of `result` | agent-sdk typescript reference | Task 1.1 |
| `--permission-mode dontAsk` is the documented fail-closed mode for `-p`; `skipPermissionsOnAllowed` is absent from current docs | cli-reference, headless, settings-reference | Task 1.3 |
| `-p` sessions load project `.mcp.json` **without asking**; `--strict-mcp-config --mcp-config <file>` restricts to only those servers; skipped entries surface in `system/init` `mcp_server_errors` | mcp.md, headless | Task 1.4 |
| `--max-turns` "exits with an error when the limit is reached" | cli-reference | Task 1.2 |
| `--add-dir` grants additional working directories | cli-reference | Task 1.5 |
| Custom subagents load from `.claude/agents/*.md`; skills from `.claude/skills/`; `--append-system-prompt-file` appends to the default prompt instead of replacing it | sub-agents, skills, cli-reference | Tasks 2.1/2.2 |
| `--forward-subagent-text` (v2.1.211+) streams subagent text/thinking into stream-json | cli-reference, headless | Task 3.1 |
| `--include-hook-events` adds hook lifecycle events to stream-json output | cli-reference | Task 3.2 |
| `MAX_MCP_OUTPUT_TOKENS` defaults to 25000 (warn at 10000); `CLAUDE_CODE_MAX_OUTPUT_TOKENS` defaults to 32000 for unrecognized model IDs | env-vars | Task 4.1 |
| Result `usage` includes `cache_creation_input_tokens` / `cache_read_input_tokens` | agent-sdk typescript reference | Task 2.3 |

## Current-state summary

- Proxy launches: `claude -p <prompt> --output-format stream-json --verbose
  --include-partial-messages --model <model> --max-turns 100 --settings
  /workspace/.claude/settings.json [--system-prompt <file>] [--resume <id>]`
  (`runner/cmd/claude-serve-proxy/main.go`, `handleSendPrompt`).
- `GenerateConfig` (`runner/harness/claude/config.go`) writes `.claude/settings.json`
  (bare `Bash` allow + deny list + `skipPermissionsOnAllowed` + `enabledMcpjsonServers`)
  and `.mcp.json` (runner-bridge, chetter, team endpoints).
- `claudeEnv` (`runner/harness/claude/resolve.go`) sets `CLAUDE_CONFIG_DIR`,
  nonessential-traffic/attribution disables, `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN`
  for custom endpoints, model alias env vars.
- The harness implements `ServeHarness` + `CompletionAwareHarness` only — no
  `SessionStatusProbe`, no `SessionContinuable` (`runner/harness/harness.go`).
- `req.AgentDefinition` and `req.SkillDefinitions` are ignored by the Claude harness
  (OpenCode writes them in `writeAgentAndSkillDefinitions`).

---

## Phase 1 — Correctness & security hardening

### Task 1.1 — Parse `errors[]` in the result error arm

**File:** `runner/cmd/claude-serve-proxy/main.go` (`claudeResultError`)

Insert an `errors` array check into the extraction priority order. New order:

1. `error` field
2. login-failure heuristic
3. *gate:* `is_error` or `subtype` prefix `error`
4. `errors[]` (array of strings, joined with `"; "`) — the documented payload for
   `error_max_turns` / `error_during_execution` / `error_max_budget_usd` /
   `error_max_structured_output_retries`
5. `message` field
6. `result` field
7. retry metadata fallback
8. subtype fallback

Prefix joined errors with the subtype when it is an `error_*` subtype so the message
is self-describing, e.g. `error_max_turns: Turn limit reached` (and map to a clean
`max turns reached` phrase — see Task 1.2).

**Tests** (`main_test.go`):
- `error_max_turns` with `errors:["Turn limit reached"]`, `is_error:true`, no `result`
  → runErr contains both subtype and message.
- `error_max_budget_usd` with `errors[]`.
- Existing success/login cases unchanged.

### Task 1.2 — Raise `--max-turns` and make it configurable

**Files:** `runner/cmd/claude-serve-proxy/main.go`

- Replace the hardcoded `--max-turns 100` with a package-level default of `500`
  and an env override `CHETTER_CLAUDE_MAX_TURNS` (read at proxy start; ignore
  invalid values with a warning log).
- Rationale: a 4-hour implementation task can legitimately exceed 100 agentic
  turns; today it would die with a generic error. 500 plus the task timeout and
  the progress watchdog still bound runaway loops.

**Tests:** flag assertions in an args-capturing test; env override parsing unit test.

### Task 1.3 — Deterministic `dontAsk` permission mode

**Files:** `runner/cmd/claude-serve-proxy/main.go` (args), `runner/harness/claude/config.go` (settings), `docs/HARNESSES.md`

- Append `--permission-mode dontAsk` to the claude args. Docs position `dontAsk` as
  the locked-down headless mode: anything not in `permissions.allow` or the read-only
  command set is denied; `AskUserQuestion` is denied even when allowed.
- Remove `"skipPermissionsOnAllowed": true` from the generated settings — it is no
  longer documented and the flag supersedes it.
- Keep the allow list (Bash/Read/Edit/Glob/Grep/Write + `mcp__*` entries) and the
  deny list exactly as-is; deny still evaluates before allow.
- Update the HARNESSES.md permission-policy paragraph.

**Tests:** `config_test.go` — settings no longer contain `skipPermissionsOnAllowed`,
still contain allow/deny; proxy args test asserts `--permission-mode dontAsk`.

**Risk:** anything previously implicitly allowed now hard-denies. In practice `-p`
already converts ask→deny, so behavior is equivalent but deterministic. Validate with
a tool-using smoke task (Phase 5 checklist).

### Task 1.4 — Strict MCP config (block repo-injected MCP servers)

**Files:** `runner/harness/claude/config.go`, `runner/cmd/claude-serve-proxy/main.go`

Problem: `-p` loads a cloned repository's `.mcp.json` without any approval. A
malicious repo can register an MCP server that receives every tool call and
exfiltrates workspace data or redirects the agent. OpenCode explicitly guards
against this; Claude currently does not.

- `GenerateConfig` additionally writes the runner-owned MCP server map
  (runner-bridge, chetter, team endpoints — the exact map written to `.mcp.json`
  today) to `<wsDir>/.claude/chetter-mcp.json` with mode `0600` via
  `mcpconfig.WritePrivateFile`. Continue writing `.mcp.json` as well (local/interactive
  compatibility, debugging).
- The proxy appends `--mcp-config /workspace/.claude/chetter-mcp.json
  --strict-mcp-config` **when that file exists**. With strict mode, project
  `.mcp.json`, `~/.claude.json`, and every other MCP source are ignored — only our
  generated servers load.
- Failure mode is safe: if our file is missing or invalid, claude reports the entries
  in `system/init` `mcp_server_errors`, which the proxy already converts into a
  terminal error (commit `034568a`).
- `enabledMcpjsonServers` stays in settings.json for local mode; it is inert under
  strict mode.

**Tests:** `config_test.go` — `chetter-mcp.json` written with expected servers and
`0600`; proxy test — strict flags present iff file exists.

### Task 1.5 — `--add-dir /tmp`

**File:** `runner/cmd/claude-serve-proxy/main.go`

Append `--add-dir /tmp` (validated to exist) so agents can use `/tmp` for scratch
files — parity with OpenCode's `external_directory` allow for `/tmp`.

**Tests:** args assertion.

---

## Phase 2 — Feature parity with OpenCode

### Task 2.1 — Inject agent and skill definitions

**Files:** new `runner/internal/skilltar/skilltar.go`, `runner/harness/claude/config.go`, refactor `runner/harness/opencode/config.go`

- Extract OpenCode's `untarSkill` into `runner/internal/skilltar`
  (`Extract(data []byte, destDir string) error`) with the existing path-escape
  guard; refactor OpenCode to call it (no behavior change).
- Claude `GenerateConfig`:
  - `req.AgentDefinition` + `req.Agent` → write `<wsDir>/.claude/agents/<agent>.md`
    (dir 0750, file 0644). Claude Code discovers project subagents from this path;
    the file also remains the source for the system-prompt flag (Task 2.2).
  - `req.SkillDefinitions` → `skilltar.Extract` into `<wsDir>/.claude/skills/<name>/`.
    Claude Code discovers project skills from `.claude/skills/`; the existing
    `promptWithSkillHints` prompt prefix already names the requested skills.
- This mirrors OpenCode's `writeAgentAndSkillDefinitions` and unblocks task agents
  and Git-backed agent/skill definitions for Claude Code tasks (today silently
  ignored — the biggest parity gap).

**Tests:** `config_test.go` — agent file content and location; skill tar extracted
with SKILL.md at the expected path; shared skilltar unit tests (happy path, escape
attempt rejected).

### Task 2.2 — `--append-system-prompt-file` instead of `--system-prompt`

**File:** `runner/cmd/claude-serve-proxy/main.go` (`resolveAgentFile` usage)

Today the agent persona file is passed with `--system-prompt`, which **replaces**
Claude Code's entire built-in system prompt (tool guidance, coding persona, safety
instructions). Switch to `--append-system-prompt-file <path>` so the persona
composes with the default prompt, matching how OpenCode layers agent definitions.

**Tests:** args assertion in the args-capturing proxy test (agent set →
`--append-system-prompt-file`, never bare `--system-prompt`).

**Risk:** persona instructions now coexist with Claude's defaults; a persona that
conflicts with defaults may behave differently than under full replacement. Validate
with the issue-triage-style smoke task (Phase 5).

### Task 2.3 — Full token usage accounting

**File:** `runner/harness/claude/serve.go` (`extractClaudeTokenUsage`)

Parse from the result `usage` object:

- `cache_read_input_tokens` → `TokenUsage.CacheReadTokens`
- `cache_creation_input_tokens` → `TokenUsage.CacheWriteTokens`

`total_cost_usd` → `CostCents` is already handled. This closes the accounting gap
with OpenCode (cache read/write) so usage summaries and cost dashboards are
comparable across harnesses.

**Tests:** `serve_test.go` — synthetic result event with cache fields maps correctly.

### Task 2.4 — Session status probe (`SessionStatusProbe`)

**Files:** `runner/cmd/claude-serve-proxy/main.go` (new `status` action), `runner/harness/claude/harness.go` + `serve.go`

- Proxy: `GET /session/{id}/status` returns `{"status":"busy"|"idle"}` — busy while
  the claude subprocess is running and no terminal result has arrived, idle
  otherwise. Reuses existing session mutex state; no new process control.
- Harness: implement `SessionStatus(ctx, baseURL, sessionID, secret) (string, error)`
  calling the endpoint. The controller's watchdog already probes any harness
  implementing the interface (`runner_task.go:430`) — no controller change needed.
- Value: distinguishes an in-flight hung generation (busy) from an agent that
  finished its turn and went quiet (idle), which today is indistinguishable and
  burns the whole task timeout.

**Tests:** proxy handler test (busy while running, idle after completion);
harness-side probe test against an httptest server.

### Task 2.5 — Watchdog continuation (`SessionContinuable`)

**Files:** `runner/cmd/claude-serve-proxy/main.go` (new `continue` action), `runner/harness/claude/harness.go`, `runner/harness/claude/serve.go`

- Proxy: `POST /session/{id}/continue` with `{"prompt": "..."}`:
  - If the session is busy → `409 Conflict` (the watchdog tolerates nudge failures).
  - If idle and a native session mapping exists → run the existing resume pipeline
    (`claude --resume <nativeID> -p "<continue prompt>" ...`) and stream events on
    the session channel as usual.
- Harness: implement `ContinueSession` posting a standard continuation prompt
  ("Continue working on the current task now. Resume from the existing state and
  complete the requested work without waiting for more input." — same text OpenCode
  uses) to the endpoint.
- This recovers the "finished its turn and went quiet" stall case without human
  intervention — the same scenario OpenCode already survives.

**Spike (documented, not blocking): true mid-flight steering.** Claude supports
streaming input (`claude -p --input-format stream-json --output-format stream-json`)
where user messages are fed as JSONL on stdin while the process runs. Restructuring
the proxy around a long-lived process with an open stdin pipe would give OpenCode-grade
steering (queue continuation prompts mid-turn). Requires verifying the exact stdin
message schema against `agent-sdk/streaming-vs-single-mode` docs and restructuring
process lifecycle, abort, and resume semantics. Recommend a separate follow-up plan;
the resume-based continue above is the safe scoped step.

**Tests:** proxy continue endpoint (409 while busy; launches resume when idle —
assert `--resume <nativeID>` in captured args); harness `ContinueSession` happy
path and 409 propagation.

---

## Phase 3 — Observability

### Task 3.1 — Forward subagent text

**File:** `runner/cmd/claude-serve-proxy/main.go` (args)

Add `--forward-subagent-text` (supported since v2.1.211; we pin 2.1.245). Subagent
text/thinking then flows through the existing `stream_event`/`text_delta` path and
appears in live progress exactly like main-thread output. Also add
`CLAUDE_CODE_FORWARD_SUBAGENT_TEXT=1` to `claudeEnv` as a belt-and-braces default.

**Tests:** args assertion.

### Task 3.2 — Include hook lifecycle events

**File:** `runner/cmd/claude-serve-proxy/main.go` (args)

Add `--include-hook-events`. Hook events (`hook_started`/`hook_response`) arrive as
additional stream-json lines; the proxy's `parseStreamEvent` already ignores unknown
types safely, and `recordStreamEvent` is unaffected. This gives the event stream
lifecycle signal (SessionStart/Setup hooks) at zero parsing cost.

**Tests:** proxy tolerates a `hook_started` line (no error, no completion impact) —
table case in the stream-parsing test.

### Task 3.3 — (Optional) Surface stderr warnings as progress

**File:** `runner/cmd/claude-serve-proxy/main.go` (`pipeStderr`)

Emit an SSE `error`-type event for stderr lines beginning with `Warning:` or
`Error:` (bounded, deduplicated). Low priority; only if trivial after Phase 1–2 land.

---

## Phase 4 — Operational knobs

### Task 4.1 — MCP and output limits

**File:** `runner/harness/claude/resolve.go` (`claudeEnv`)

- `MAX_MCP_OUTPUT_TOKENS`: default `50000` (docs default 25000 warns at 10000 —
  runner-bridge payloads like full PR diffs and task exports routinely exceed that),
  overridable via `CHETTER_CLAUDE_MAX_MCP_OUTPUT_TOKENS`.
- `CLAUDE_CODE_MAX_OUTPUT_TOKENS`: leave the CLI default (32000 for unrecognized
  model IDs) but allow override via `CHETTER_CLAUDE_MAX_OUTPUT_TOKENS`.
- Optional safety knob: `CHETTER_CLAUDE_MAX_BUDGET_USD`, when set, passes
  `--max-budget-usd <value>` (per-task spend ceiling, v2.1.217+). Not wired to any
  server-side budget field yet — see Open Questions.

**Tests:** `resolve_test.go` — defaults present, env overrides honored.

### Task 4.2 — Tighten settings.json permissions

**File:** `runner/harness/claude/config.go`

Write `.claude/settings.json` with mode `0600` instead of `0644` (defense in depth;
`.mcp.json` already uses `WritePrivateFile`).

**Tests:** existing config tests extended with a permission assertion.

---

## Phase 5 — Verification & rollout

### Automated

- `make test`, `make vet`, `make lint` in `runner/` (race detector on touched packages).
- Full `make check` from repo root (includes runner-check and SQL parity, which this
  change does not touch but must not break).

### Live fleet validation (after image rebuild)

1. Smoke: echo task with `harness: claude-code` — confirms dontAsk + flags don't break
   plain runs.
2. Tool-use task: "list repo files and report make targets" — confirms Bash allow and
   permissions under dontAsk.
3. MCP task: a task that calls a runner-bridge artifact tool (e.g. `chetter_issue_comment`
   on a scratch issue) — confirms `--strict-mcp-config` still loads runner-bridge and
   that `system/init` shows zero `mcp_server_errors`.
4. Agent-definition task: submit with an `agent` that exists in chetter-config and
   verify the persona is applied (append-system-prompt) and skills resolve.
5. Stall-recovery: submit a task that ends its turn early (idle-but-not-terminal),
   confirm the watchdog probe reports idle and continuation resumes it.

### Deployment notes

- `runner/cmd/claude-serve-proxy` is built into **chetter-agent-base** (proxy-builder
  stage) — all stack variants inherit it; base image rebuild + fleet redeploy required.
- `runner/harness/claude` changes ship in the **chetter-runner** daemon image.
- Both images rebuild via the normal CI path (push to `main` → wowbagger build →
  drain → GitOps sync). Roll this branch through a PR after review.

### Documentation

- Update `docs/HARNESSES.md` Claude Code section: permission policy (dontAsk),
  strict MCP loading, agent/skill injection, status probe + continuation, env knobs.
  Remove "No mid-task steering or follow-up" con once Task 2.5 lands (reword to
  "continuation via resume; mid-turn steering planned").
- Add the new `CHETTER_CLAUDE_*` env vars to `docs/MANUAL.md` runner configuration
  table and `.env.example` if runner-scoped.

---

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| `dontAsk` hard-denies a tool that previously slipped through | Equivalent semantics in `-p` (ask already meant deny); smoke tests 1–2 validate before rollout |
| Strict MCP config breaks a workflow that relied on repo-provided `.mcp.json` servers | Intentional security posture change; document in HARNESSES.md and PR description; teams needing repo servers can request them as chetter-config MCP endpoints (properly scoped and tokened) |
| `--append-system-prompt` changes persona behavior vs full replacement | Smoke test 4; if a persona requires full replacement we keep a per-task escape hatch (see Open Questions) |
| Higher `--max-turns` lets runaway loops burn tokens | Task timeout, progress watchdog, and (optional) `--max-budget-usd` knob still bound it |
| Proxy changes require agent-base rebuild across all stack variants | Standard CI path; validate with self-test profile `harnesses` after deploy |
| `--forward-subagent-text` increases event volume | Progress publisher already batches/flushes at 3s intervals and truncates |

## Open questions

1. **Streaming-input steering:** verify the exact `--input-format stream-json` stdin
   message schema (user message JSONL shape) against the Agent SDK docs; if clean,
   propose a follow-up plan converting the proxy to a long-lived process with stdin
   steering, abort-via-stdin-close, and true mid-turn continuation.
2. **Budget wiring:** should `--max-budget-usd` eventually be driven by a server-side
   per-task cost ceiling (there is `budget_exceeded` classification in the controller
   but no field plumbed into `task.TaskRequest` today)?
3. **System-prompt escape hatch:** do any existing chetter-config agent definitions
   assume full system-prompt replacement for Claude Code? Audit `chetter-config`
   agents before merging Task 2.2.
4. **`ANTHROPIC_DEFAULT_MODEL`:** set it alongside the alias vars (v2.1.236+) so
   `/model`-style resolution is fully pinned for unrecognized model IDs?

## Task sequence

```
Phase 1 (single PR or two): 1.1 → 1.2 → 1.3 → 1.4 → 1.5   (proxy + config + tests)
Phase 2 (two PRs):          2.1 + 2.2 + 2.3   then   2.4 + 2.5
Phase 3 + 4 (one PR):       3.1, 3.2, 4.1, 4.2  (3.3 optional)
Each phase: make check → live smoke per Phase 5 → HARNESSES.md update in the same PR.
```

Estimated size: Phase 1 ~300 lines with tests; Phase 2 ~600 lines with tests
(skilltar extraction + proxy endpoints); Phase 3/4 ~120 lines.
