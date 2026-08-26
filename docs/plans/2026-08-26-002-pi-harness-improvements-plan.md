# Pi Harness Improvements Plan

**Date:** 2026-08-26
**Branch:** `feat/pi-harness-improvements`
**Worktree:** `/home/gokr/git/chetter-pi-harness`
**Status:** In progress — Phase 1 complete (commits `4cc4dc3`, `a4f967a`); Phases 2–4 pending

## Background

A parity audit of the Pi harness (`runner/harness/pi/` + the RPC execution path in
`runner/internal/controller/runner_task.go`) against the OpenCode and Claude Code
harnesses, using the Claude Code harness improvements plan
(`docs/plans/2026-08-26-001-claude-code-harness-improvements-plan.md`) as the reference
for the feature set. Protocol details below were validated against the Pi source
(`~/git/pi`, package `@earendil-works/pi-coding-agent`; RPC protocol in
`packages/coding-agent/src/modes/rpc/`, settings schema in
`packages/coding-agent/src/core/settings-manager.ts`, usage shape in
`packages/ai/src/types.ts`).

Pi is the only **RPC-mode** harness: it runs `pi --mode rpc --no-session --offline
--approve` as a long-lived subprocess and speaks bidirectional JSONL over
stdin/stdout. This gives it capabilities the serve-mode harnesses lack natively —
true mid-turn `steer` and post-turn `follow_up` commands — but the Chetter RPC
execution path does not yet use them, and several OpenCode/Claude parity features
are missing.

Baseline: `runner/harness/pi/` (config.go, pi.go, rpc.go, state.go, export.go),
RPC flow in `runner/internal/controller/runner_task.go` (`runRPCAgentCommand`,
`handleRPCEvent`, `streamRPCLines`), watchdog in
`runner/internal/controller/progress_watchdog.go`.

## Goals

1. Close correctness gaps in the RPC execution path (token accounting, stale
   progress publication, model-resolution validation).
2. Reach feature parity with OpenCode/Claude: agent and skill injection, session
   status probing, watchdog continuation.
3. Exploit Pi's native `steer`/`follow_up` RPC commands where they beat the
   serve-mode equivalents (mid-turn steering instead of resume-based continue).
4. Improve observability and expose operational knobs.

## Non-goals

- Replacing the RPC execution model with a serve-proxy. Pi's RPC mode is strictly
  more capable than any HTTP wrapper we would build, and `get_messages` already
  provides the session export the serve-proxy would proxy.
- Mid-turn steering from external operators. Pi's `steer` is a first-class RPC
  command today; wiring it to a task-level control surface is a separate plan.
- Pi's own permission/sandboxing model. gVisor remains the task boundary; Pi's
  `--approve` is a project-trust override, not a permission bypass (see Task 1.3).

## Key facts driving this plan (validated against Pi source)

| Fact | Source | Impact |
|---|---|---|
| `message_update` events carry a top-level `usage` object (`{input, output, cacheRead, cacheWrite, cacheWrite1h?, reasoning?, totalTokens, cost:{input,output,cacheRead,cacheWrite,total}}`) — serialized by `toJsonEvent` from the assistant message | `packages/coding-agent/src/modes/json-event.ts`, `packages/ai/src/types.ts` | Task 2.1 |
| The harness's `handleRPCEvent` reads only `assistantMessageEvent` and never `usage`; the RPC path publishes `task.TokenUsage{}` empty | `runner_task.go` | Task 2.1 |
| `--approve`/`-a` sets `projectTrustOverride: true` — it trusts the project (loads `.pi/` extensions and skills without a prompt), it does **not** auto-approve tools | `packages/coding-agent/src/cli/args.ts:219` | Task 1.3 |
| RPC `get_state` returns `isStreaming`, `isCompacting`, `pendingMessageCount`, `sessionId` — the exact busy/idle discriminator the watchdog needs | `rpc-mode.ts` `get_state` case | Task 2.3 |
| RPC `steer` (mid-turn) and `follow_up` (post-turn) are native commands; `abort` returns `{success:true}` | `rpc-types.ts`, `rpc-mode.ts:418-431` | Task 2.4 |
| `agent_end` carries `willRetry`; `agent_settled` is a bare terminal event | `agent-session.ts:144-150,648` | Current state (already handled) |
| Skills load from `~/.pi/agent/skills/` and project `.pi/skills/`; `--skill <path>` loads a skill file/dir (repeatable). No built-in agent/persona file concept — persona is the system prompt | `resource-loader.ts:813`, `cli/args.ts:171` | Task 2.2 |
| JSONL framing is LF-only; `attachJsonlLineReader` intentionally avoids Node readline (U+2028/U+2029 are valid inside JSON strings). Chetter's `streamRPCLines` uses `bufio.ReadString('\n')` — correct | `modes/rpc/jsonl.ts` | Current state (verified, no change) |
| Settings schema: `compaction.{enabled,reserveTokens,keepRecentTokens}`, `retry.{enabled,maxRetries}` are the top-level keys the harness writes | `core/settings-manager.ts:101-103` | Current state (verified) |
| `get_available_models` / `set_model` / `cycle_model` RPC commands exist | `rpc-types.ts` | Open question 3 |
| The RPC path has **no** progress watchdog; `watchHarnessProgress` is only wired into the serve paths (`runLocalAgent`, `runDockerAgent`, resume) | `runner_task.go:402,733,872,1053` | Task 2.3/2.4 |

## Current-state summary

- `GenerateConfig` (`runner/harness/pi/config.go`) writes `~/.pi/agent/settings.json`
  (compaction + retry), `models.json` (custom provider), project `.pi/settings.json`
  (pi-mcp-adapter extension), and `.mcp.json` (runner-bridge, chetter, team endpoints,
  mode 0600). Local mode copies `~/.pi/agent/auth.json` into the workspace.
- `buildRPCCommand` (`runner/harness/pi/rpc.go`) launches
  `pi --mode rpc --no-session --offline --approve [--provider --model --thinking]`.
- `runRPCAgentCommand` drives the protocol: `get_state` readiness probe, `prompt`,
  event loop, `get_last_assistant_text`, `get_messages` → markdown session export.
  `handleRPCEvent` publishes 3s-batched progress (`pi: <detail>`) and handles
  `message_update`, `message_end`, `tool_execution_*`, `auto_retry_*`,
  `extension_error`, `extension_ui_request` (auto-responds `cancelled:true`),
  `agent_end`, `agent_settled`.
- Pi implements `Harness` + `RPCHarness` only. No `SessionContinuable`, no
  `SessionStatusProbe` — but those are serve-oriented interfaces; for RPC the
  equivalent is native `steer`/`follow_up` and `get_state`, which the RPC path
  does not yet use.
- `req.AgentDefinition`, `req.SkillDefinitions`, and `req.ResumeHarnessSessionID`
  are ignored by the Pi harness (OpenCode/Claude inject agent/skill definitions).

---

## Phase 1 — Correctness & security hardening

### Task 1.1 — Parse `usage` from `message_update` events

**File:** `runner/internal/controller/runner_task.go` (`handleRPCEvent`)

`toJsonEvent` serializes `message_update` as
`{type:"message_update", usage: <Usage>, assistantMessageEvent: {...}}` — usage is
**top-level**, alongside `assistantMessageEvent`. The harness currently ignores it.

- In the `message_update` case, read `ev["usage"]` (a map) and update a
  `usage task.TokenUsage` accumulator on the `rpcAgentState`:
  - `input` → `InputTokens`, `output` → `OutputTokens`,
    `cacheRead` → `CacheReadTokens`, `cacheWrite` → `CacheWriteTokens`,
    `reasoning` → `ReasoningTokens`
  - `cost.total` (dollars) → `CostCents` via `int64(math.Round(cost*100))`
- `message_update` carries the *cumulative* message usage on each event, so the
  accumulator should take the latest snapshot (replace, not add) or delta against
  the previous snapshot; use the delta form so interrupted streams don't
  double-count. `message_end` also carries `message.usage` — fall back to it when
  no `message_update` usage arrived.
- Thread `state.usage` through the terminal `publishStatusWithMetadata` calls in
  `runRPCAgentCommand` (all four terminal paths + cancellation paths) instead of
  the current `task.TokenUsage{}`.

**Tests:** `runner_task_test.go` — synthetic `message_update` with a full `usage`
map maps all six fields; `cost.total: "1.23"` → `123` cents; a follow-up
`message_update` with higher usage produces the delta; `message_end`-only usage
fallback.

### Task 1.2 — Fix stale progress publication after idle

**File:** `runner/internal/controller/runner_task.go` (`handleRPCEvent`)

Current 3s-batched progress publishes only when `state.lastDetail != ""` **and**
3s elapsed. When a long tool call produces no deltas (e.g. a 60s `bash` step),
progress goes quiet and the task appears stuck. The serve paths' watchdog has the
same concern and handles it via heartbeat/status probes (Phase 2).

- Publish a bounded keepalive (`pi: running…` or the active tool name from
  `tool_execution_start`'s `toolName`, which is already captured in
  `state.lastDetail`) at the 3s interval even when `lastDetail` is empty but the
  session is not terminal.
- Keep the existing detail-batched path; the keepalive is strictly additional and
  bounded (reuse `state.lastPublished`).

**Tests:** table case where a `tool_execution_start` with a long gap between
deltas still emits keepalive progress.

### Task 1.3 — Document `--approve` semantics correctly

**Files:** `docs/HARNESSES.md`, `runner/harness/pi/rpc.go` (comment)

`--approve` is a project-trust override (`projectTrustOverride: true`), not a
tool-approval bypass. In `-p`-style headless runs there are no interactive
approvals anyway (extension UI requests are auto-cancelled), so the flag's real
effect is: load project `.pi/` extensions/skills without a trust prompt. Update the
HARNESSES.md Pi section ("No built-in permission system") to state this precisely,
and add a comment to `buildRPCCommand` explaining why `--approve` is safe in
headless RPC mode.

### Task 1.4 — Validate model-resolution failures at claim time

**File:** `runner/harness/pi/rpc.go` (`modelFields`), `runner/internal/controller/runner_task.go` (RPC ready path)

`modelFields` silently falls back to env `PI_PROVIDER`/`PI_MODEL` and then to
hardcoded `zai`/`glm-5.2` when a task specifies no provider/model. A task that
explicitly requests a model the configured Pi build cannot resolve currently fails
only at the first `prompt` response, after the subprocess is already running.

- On the `get_state` readiness probe, also issue `get_available_models` and verify
  `provider/model` (or the qualified `model`) resolves; fail fast with a clear
  `model not found: provider/model` error instead of letting the agent burn a turn.
- Keep the env/default fallback for tasks that genuinely omit model selection
  (ambient runner defaults), but surface which fallback was used in the status
  message.

**Tests:** `pi_test.go` — `modelFields` fallback table extended; RPC flow test with
a fake `get_available_models` response that lacks the requested model → terminal
error, no `prompt` sent.

---

## Phase 2 — Feature parity with OpenCode/Claude

### Task 2.1 — Full token usage accounting

Same as Task 1.1 (parse `usage`). This is listed separately because it is the
headline parity item — it closes the last accounting gap with OpenCode/Claude
(cache read/write + reasoning + cost), so usage summaries and cost dashboards are
comparable across harnesses. **Implement together with Task 1.1; kept distinct for
tracking.**

### Task 2.2 — Inject agent persona and skill definitions

**Files:** `runner/harness/pi/config.go`, refactor `runner/harness/opencode/config.go`

- Extract OpenCode's `untarSkill` into `runner/internal/skilltar`
  (`Extract(data []byte, destDir string) error`) with the existing path-escape
  guard; refactor OpenCode to call it (no behavior change) — same as the Claude
  plan's Task 2.1.
- Pi `GenerateConfig`:
  - `req.SkillDefinitions` → `skilltar.Extract` into `<wsDir>/.pi/skills/<name>/`
    (pi discovers project skills from `.pi/skills/`; `--skill <path>` also works —
    but writing the dir is sufficient and survives the CLI flag list). If a skill
    name collides with a runner-bridge skill, the runner-owned one wins (write
    order).
  - `req.AgentDefinition` → pi has no agent-file concept; the persona composes
    with the system prompt. Write the definition to
    `<wsDir>/.pi/agent/system-prompt.md` and pass `--system-prompt
    <wsDir>/.pi/agent/system-prompt.md` (a file path argument) so the persona
    replaces the default coding-assistant prompt, matching how a Chetter agent
    definition is meant to control behavior. Pi's `--system-prompt <text>` accepts
    a file path (validated in `cli/args.ts` help text: "System prompt (default:
    coding assistant prompt)").
  - The existing `promptWithSkillHints`-style prefix in `rpcPrompt` stays as a
    belt-and-braces nudge; the injected files are the real mechanism.
- This mirrors OpenCode's `writeAgentAndSkillDefinitions` and unblocks task agents
  and Git-backed agent/skill definitions for Pi tasks (today silently ignored).

**Tests:** `pi_test.go` — skill tar extracted with `SKILL.md` at
`.pi/skills/<name>/SKILL.md`; agent persona file written and `--system-prompt`
arg points at it; shared skilltar unit tests (happy path, escape attempt rejected).

### Task 2.3 — RPC session status probing

**Files:** `runner/internal/controller/runner_task.go` (RPC loop),
`runner/harness/harness.go` (optional RPC-status interface)

The serve paths use `SessionStatusProbe`; the RPC path has no equivalent. Pi's
`get_state` returns `isStreaming`, `isCompacting`, `pendingMessageCount` —
exactly the busy/idle discriminator.

- In the RPC event loop, on a bounded silence window (reuse the watchdog's
  threshold, e.g. 30s without a line), issue a `get_state` probe. If
  `isStreaming == true` or `pendingMessageCount > 0`, the agent is alive and busy
  — keep waiting (reset the stall clock). If the probe fails or reports idle while
  the task is not terminal, feed that into the watchdog as a stall candidate.
- Expose this as an optional interface (`RPCStatusProbe` or reuse the existing
  watchdog hook shape) so the controller logic stays harness-agnostic; the
  watchdog (`progress_watchdog.go`) is the natural consumer.

**Tests:** RPC loop test with a fake subprocess that goes silent but answers
`get_state` with `isStreaming:true` → not marked stuck; `isStreaming:false` →
stall path.

### Task 2.4 — Watchdog continuation via native `steer`/`follow_up`

**Files:** `runner/internal/controller/runner_task.go` (RPC loop, watchdog wiring),
`runner/internal/controller/progress_watchdog.go`

The RPC path currently has **no** watchdog at all — a Pi agent that finishes its
turn and goes quiet burns the whole task timeout. Pi's RPC protocol has native
post-turn continuation (`follow_up`) and even mid-turn steering (`steer`), which is
strictly better than Claude's resume-based continue:

- Wire the existing progress watchdog into `runRPCAgentCommand` the way the serve
  paths use `watchHarnessProgress`.
- The watchdog's nudge callback sends `{"type":"follow_up","message":"<same
  continuation prompt OpenCode uses>"}` over stdin instead of a HTTP continue
  call. Because `follow_up` queues after the current turn, the agent resumes
  without a session restart — no `--resume`, no native-session mapping needed.
- The status probe from Task 2.3 tells the watchdog "busy but alive" from "idle
  and stuck", matching the serve-path semantics (the Claude plan's Task 2.4/2.5
  combination, but native).
- Abort semantics unchanged: `abort` command already cancels a running turn.

**Tests:** RPC flow test — silence beyond the threshold emits one `follow_up`
command (assert JSONL on the fake stdin) and the task completes; a busy probe
suppresses the nudge; `abort` still cancels.

**Spike (documented, not blocking): external steer surface.** `steer` is already a
native RPC command; exposing a task-level "steer" input (MCP tool or webhook) is a
separate feature plan on top of this watchdog wiring.

---

## Phase 3 — Observability

### Task 3.1 — Surface thinking/reasoning deltas

**File:** `runner/internal/controller/runner_task.go` (`handleRPCEvent`)

`message_update` events can carry thinking deltas in the assistant message event
(the `pi` harness already surfaces `thinking_delta` in `WatchEvents` for serve
paths; the RPC path does not). Publish `pi: thinking…` progress when a
`thinking_delta`/`thinking_start` arrives, using the same 3s batching, so long
reasoning phases don't look like stalls (complements Task 1.2).

**Tests:** event-table case — `thinking_start`/`thinking_delta` sets `lastDetail`
and emits progress.

### Task 3.2 — Include reasoning token accounting in usage

Covered by Task 1.1 (`usage.reasoning` → `ReasoningTokens`). Pi is the only
harness that reports a reasoning breakdown (other harnesses leave it 0); once Task
1.1 lands, reasoning costs appear in usage dashboards for Pi tasks.

---

## Phase 4 — Operational knobs

### Task 4.1 — Config file permissions

**File:** `runner/harness/pi/config.go`

`settings.json`, `models.json`, and project `.pi/settings.json` are written with
mode 0644; only `.mcp.json` uses `WritePrivateFile` (0600). `models.json` contains
the provider `apiKey` as an env reference (e.g. `"$SYNTHETIC_API_KEY"`), not a
literal secret, but 0644 is still wider than needed in a shared workspace root.
Write all four files with mode 0600 (defense in depth; matches the Claude plan's
Task 4.2).

**Tests:** `pi_test.go` — permission assertions on the written files.

### Task 4.2 — Verify retry/compaction settings stay valid

**File:** `runner/harness/pi/config.go`

The written settings keys (`compaction.{enabled,reserveTokens,keepRecentTokens}`,
`retry.{enabled,maxRetries}`) match the current pi settings schema (validated).
No change needed beyond a regression test asserting the JSON shape, so a future pi
settings rename (like the `retry.maxDelayMs` migration seen in the pi source)
doesn't silently break headless behavior.

---

## Phase 5 — Verification & rollout

### Automated

- `make test`, `make vet`, `make lint` in `runner/` (race detector on touched
  packages).
- Full `make check` from repo root (includes runner-check and SQL parity — this
  change does not touch SQL, but must not break it).

### Live fleet validation (after image rebuild)

1. Smoke: echo task with `harness: pi` — confirms the RPC flow + new usage parsing
   don't break plain runs; check the task detail page shows non-zero token usage
   and cost.
2. Tool-use task: "list repo files and report make targets" — confirms keepalive
   progress during long tool calls and watchdog `follow_up` behavior.
3. Skill task: submit with a skill definition and verify `.pi/skills/<name>/`
   appears in the workspace and the agent uses it.
4. Agent-definition task: submit with an `agent` from chetter-config and verify the
   persona is applied (system prompt) — confirm no double-prompt conflict with
   pi's own coding-assistant default.
5. Stall-recovery: submit a task that ends its turn early (idle-but-not-terminal);
   confirm `get_state` reports idle and the watchdog nudges via `follow_up`.

### Deployment notes

- Pi harness changes ship in the **chetter-runner** daemon image
  (`runner/harness/pi` + `runner/internal/controller/runner_task.go`). No
  agent-base rebuild needed (no proxy binary, no new pi version pinned).
- Roll through a PR after review; same CI path as any runner change.

### Documentation

- Update `docs/HARNESSES.md` Pi section and the comparison matrix:
  - `--approve` = project-trust override, not tool approval (Task 1.3).
  - Agent definitions: injected (`.pi/agent/system-prompt.md` + `--system-prompt`);
    skills: injected (`.pi/skills/`).
  - Session status probe: yes (via `get_state`); watchdog continuation: yes
    (native `follow_up`).
  - Cache token accounting: yes.
  - Remove the "No startup event in RPC mode" con if Task 2.3 lands the `get_state`
    probe loop, or reword it to "readiness probed via get_state".
- Note Pi's RPC-native steer/follow_up as an advantage over the serve-mode resume
  continuation.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| Usage parsing breaks on a pi version that changes the `usage` shape | Type-assert defensively; missing/unknown fields → 0, never fail the event loop; regression test locks the current shape |
| `--system-prompt` persona conflicts with pi's built-in coding-assistant prompt | Persona replaces the default prompt (documented behavior); smoke task 4 validates; keep the agent-definition file as the single source of truth |
| Skill name collisions with runner-bridge skills | Runner-owned skills win (write order); document in HARNESSES.md |
| Watchdog `follow_up` nudges a busy agent | Task 2.3's `get_state` probe distinguishes busy from idle; nudge only on idle |
| `get_available_models` fails on older pi builds | Fail-open: probe failure → proceed without the check (log warning); only a *successful* probe with a missing model is fatal |
| Config file 0600 breaks a workflow that reads the files | Only the containerized agent reads them; 0600 is strictly tighter and already used for `.mcp.json` |

## Open questions

1. **External steer surface:** pi's `steer` command already supports mid-turn
   injection. Should a follow-up plan expose it as an MCP tool / webhook so
   operators can redirect a running task (OpenCode/Claude cannot)? The watchdog
   wiring in Task 2.4 makes the plumbing trivial to extend.
2. **Resume for Pi sessions:** `--no-session` keeps an in-memory session, so
   `ResumeHarnessSessionID` is not usable today. Pi supports `--session-id` /
   persisted session dirs; should resumable Pi tasks persist the session instead
   of exporting to markdown only?
3. **Model switching mid-session:** `set_model`/`cycle_model` RPC commands exist;
   should a task-level `variant_id` or model change surface mid-turn? Currently
   only startup `--model`/`--provider` is honored.
4. **Max-turns equivalent:** pi has no `--max-turns`; auto-retry is bounded by
   `retry.maxRetries` (already written). Does a long implementation task need an
   explicit turn ceiling, or is the task timeout + watchdog enough (as for
   OpenCode)?

## Task sequence

```
Phase 1 (one PR):     1.1 + 1.2 + 1.3 + 1.4
Phase 2 (two PRs):    2.1 + 2.2   then   2.3 + 2.4
Phase 3 + 4 (one PR): 3.1, 3.2, 4.1, 4.2
Each phase: make check → live smoke per Phase 5 → HARNESSES.md update in the same PR.
```

Estimated size: Phase 1 ~250 lines with tests; Phase 2 ~500 lines with tests
(skilltar extraction + watchdog wiring); Phase 3/4 ~100 lines.
