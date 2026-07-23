---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: "current-main quality audit at f14cc95"
title: "refactor: Chetter quality, isolation, and lifecycle hardening"
date: 2026-07-23
---

# Chetter Quality, Isolation, and Lifecycle Hardening

## Goal

Improve the server and runner without undoing the completed Task -> AgentSession -> UserPrompt -> ExecutionAttempt refactor.

The work is ordered around four invariants:

1. A scoped caller can never see or mutate data outside its effective team scope.
2. An untrusted task can only reach the services and credentials belonging to its own execution.
3. An execution has one terminal outcome, and shutdown waits for all owned work.
4. Database, scheduler, webhook, and stream behavior remains correct across MySQL/TiDB and PostgreSQL.

This is the implementation plan for the broader hardening effort. The bounded fixes
recorded below are already implemented; the remaining findings still require the
larger changes described in the phases that follow. It does not introduce a new
credential vault or change the product model for teams and users.

## Current Baseline

Validated against `main` at `f14cc95`:

- Server tests: 515 passed.
- Runner tests: 328 passed.
- Runner statement coverage: 37.5% overall in the post-merge profile.
- `go vet ./...`: passed in both modules.
- Repository-pinned Staticcheck: passed in both modules.
- Runner race tests: passed.

The recent execution hierarchy, artifact attribution, and runner-cleanups work is retained. Several earlier findings are now fixed or partially fixed; this plan lists those statuses explicitly to avoid reimplementing completed work.

## Finding Validation

### Server Findings

| ID | Status | Current conclusion | Plan action |
|---|---|---|---|
| S1 | Fixed (2026-07-23) | `auth.ResolveTeamFilter` distinguishes unrestricted, scoped, and empty-intersection filters; task, session, trigger, and raw repository-filtered queries return no rows for disjoint filters. `internal/auth/auth.go:14-106`; `internal/service/api.go:69-121,637-677,768-805`. | Keep disjoint-team regression coverage. |
| S2 | Partially fixed | GitHub RPCs now validate task/execution hierarchy, but not active status, runner ownership, or caller runner identity. `internal/service/runner_github_rpc.go:38-126`; `internal/service/github_tools.go:42-67`. | P0 runner claim authentication. |
| S3 | Partially fixed | Agent listing has team filtering, but definition get/list and runner materialization remain name-only. `internal/service/model_catalog_tools.go:140-163,256-293`; `internal/service/runner_rpc.go:254-310`. | P0 scoped definition resolver. |
| S4 | Fixed (2026-07-23) | `drainRunnerTool` now requires admin access and records a `runner_drain_requested` audit event. `internal/service/tools.go:1326-1344`. | Keep regression coverage. |
| S5 | Fixed (2026-07-23) | Team-token fleet health is aggregate-only; runner, runner-image, and running-task detail arrays are redacted. `internal/service/api.go:933-946`. | Keep team health redaction coverage. |
| S6 | Partially fixed | GitHub writes moved server-side, but webhook review tasks still receive redacted/unusable GitHub credentials. `internal/webhook/handler.go:490-529`; `internal/service/service.go:1307-1317`. | P1 replace CLI credential delivery with runner-scoped server operations. |
| S7 | Still relevant | Team deletion is not a transactional cascade and does not remove the full task/session/attempt/artifact graph. `internal/service/api.go:1193-1230`; `db/queries/triggers.sql:102-108`. | P1 transactional deletion and migration-backed cascade. |
| S8 | Partially fixed (2026-07-23) | The trigger-execution read of `cronEntries` now uses `cronMu`; scheduler behavior still needs dedicated regression coverage. `internal/service/service.go:1615-1632,1682-1697`. | Add scheduler tests and retain the shared lock discipline. |
| S9 | Still relevant | A `task.completed` callback can create another task that triggers the same callback indefinitely. `internal/service/event_callbacks.go:245-310`; `internal/service/runner_rpc.go:1073-1092`. | P1 provenance, depth, deduplication, and rate limits. |
| S10 | Still relevant | Definition sync creates new trigger IDs and leaves old cron entries registered. `internal/service/model_catalog_tools.go:374-405,660-730`. | P1 stable trigger identity and obsolete-entry cleanup. |
| S11 | Still relevant | Raw repo-filtered queries use `?` directly and bypass PostgreSQL placeholder conversion. `internal/service/fts.go:46-83,154-187`. | P1 move queries into both sqlc dialects. |
| S12 | Partially fixed (2026-07-23) | In-memory webhook deduplication now evicts the oldest entries when the configured bound is exceeded; durable intake and acknowledgement ordering remain unresolved. `internal/webhook/dedup.go:36-80`; `internal/webhook/handler.go:189-200`. | P1 durable intake, retry/dead-letter behavior, and bounded eviction coverage. |
| S13 | Still relevant | Reaper, definition sync, callback, and shutdown goroutines are not joined. `internal/service/service.go:205-267`; `internal/service/runner_rpc.go:824-828,1088-1092`. | P1 service-owned context and wait group. |
| S14 | Partially fixed | Historical task IDs are now populated, but replay still subscribes after querying history and uses timestamp-only cursors. `internal/webapi/streaming.go:26-46`; `db/queries/task_events.sql:12-15`. | P1 cursor-safe replay protocol. |
| S15 | Fixed (2026-07-23) | Subscriber shutdown and unsubscribe now share `sync.Once`, so deferred cleanup after `CloseAll` is idempotent. `internal/webapi/eventbus.go:19-39,71-145`. | Keep regression coverage. |
| S16 | Fixed (2026-07-23) | Repository enumeration is filtered through task team ownership; task-artifact listing remains admin-only. `internal/service/api.go:1538-1570`; `internal/webapi/handlers.go:817-822`. | Keep repository scope coverage. |
| S17 | Still relevant | Callback webhooks accept arbitrary destinations through `http.DefaultClient`. `internal/service/event_callbacks.go:313-345,400-413`. | P1 SSRF-safe destination policy. |
| S18 | Partially fixed | PostgreSQL DSNs are detected, but failed probes still operationally fall back to TiDB. `internal/store/store.go:207-210,309-316`. | P1 fail-closed dialect selection. |
| S19 | Still relevant | Definition sync lacks service-level and Git-worktree serialization. `internal/service/model_catalog_tools.go:314-424`; `pkg/definitions/manager.go:90-112`. | P1 one sync coordinator. |
| S20 | Fixed (2026-07-23) | Definition proposal reads and writes now authorize through the definition source; global source writes require admin access and team-scoped sources require ownership. `internal/service/definition_proposal_tools.go:94-225,249-273`; `internal/service/model_catalog_tools.go:334-351`. | Keep source ownership regression coverage. |

### Runner Findings

| ID | Status | Current conclusion | Plan action |
|---|---|---|---|
| R1 | Still relevant | Provider credentials are still copied into task containers through the centralized environment policy. `runner/internal/agentenv/environment.go:105-115,163-188,249-277`; `runner/internal/controller/runner_task.go:447-477,1316-1343`; `runner/internal/controller/docker_args.go:68-77`. | P0 credential isolation. |
| R2 | Still relevant | The MCP relay has no inbound authentication and injects the configured upstream token. `runner/internal/network/mcp_relay.go:40-64`. | P0 execution-scoped relay authentication. |
| R3 | Still relevant | Per-execution MCP servers bind `0.0.0.0` without authentication. `runner/internal/mcp/server.go:38-64`. | P0 authenticated bridge or isolated network. |
| R4 | Still relevant | Non-gVisor tasks share the runner network and bypass forced proxy/DNS controls. `runner/internal/controller/docker_args.go:24-33,56-67`; `runner/internal/controller/runner_task.go:1094-1135`. | P0 require enforced isolation for untrusted tasks. |
| R5 | Partially fixed | Task/execution IDs and generic workspace tools are safer, but `ExtraFiles`, resume paths, and host-path translation remain unchecked. `runner/internal/controller/runner_task.go:177-190,702-715`; `runner/internal/controller/docker_args.go:34-36`; `runner/internal/agentenv/environment.go:18-26`. | P0 shared safe-path boundary. |
| R6 | Partially fixed | The closed-channel early return is fixed; normal drain waits for active tasks and has a deadline, but deadline cancellation returns before task cleanup completes. `runner/internal/controller/heartbeat.go:181-224`. | P1 explicit drain completion barrier. |
| R7 | Partially fixed | Process-group cancellation and process waiting improved, but RPC writes can still block and terminal reporting remains detached. `runner/internal/controller/process_unix.go:10-21`; `runner/internal/controller/runner_task.go:1573-1585`; `runner/internal/controller/runner_rpc.go:216-232`. | P1 bounded writer and joined reporting. |
| R8 | Fixed | Harness instances and execution/container identity are now per execution. `runner/internal/controller/runner.go:94-114`; `runner/internal/controller/runner_task.go:31-37`. | Keep regression coverage; no redesign. |
| R9 | Partially fixed | Cleanup and cancellation ordering improved, but terminal publication still precedes all deferred cleanup. `runner/internal/controller/runner_task.go:742-747,918-924,1435-1437`; `internal/service/api.go:154-178`. | P1 execution lifecycle barrier. |
| R10 | Fixed | Task panic recovery now reports an error without re-panicking into the claim loop. `runner/internal/controller/runner_task.go:41-48`. | Add regression coverage; no redesign. |
| R11 | Still relevant | `MaxMemoryMB` and `MaxCPU` are carried but not applied consistently to Docker/RPC execution. `runner/internal/task/types.go:34-36`; `runner/internal/controller/runner_rpc.go:132-134`; `runner/internal/controller/docker_args.go:29-31`. | P1 enforce resource limits or remove misleading fields. |
| R12 | Fixed | The watchdog has an explicit stop channel and its goroutine is joined. `runner/internal/controller/progress_watchdog.go:39-65`; `runner/internal/controller/runner_task.go:270-287`. | Keep regression coverage; no lifecycle redesign for this component. |
| R13 | Still relevant | Terminal reports run in a background goroutine outside runner shutdown. `runner/internal/controller/runner_rpc.go:216-232`. | P1 report queue with shutdown join. |
| R14 | Partially fixed | Workspace destruction now reports traversal, chmod, and removal errors, but callers still log and continue; symlink-safe deletion and shared path validation remain incomplete. `runner/internal/workspace/manager.go:58-81`; `runner/internal/controller/runner_task.go:125-133`. | P1 fail-closed cleanup and symlink-safe deletion. |
| R15 | Still relevant | OpenCode copies host auth/model/cache state into Docker workspaces. `runner/harness/opencode/config.go:311,397-405`; `runner/harness/opencode/state.go:40-55`. | P0 prohibit secret state in untrusted workspaces. |
| R16 | Partially fixed | DNS trims trailing dots, but HTTP proxy matching still performs incomplete host parsing. `runner/internal/network/dns.go:26-30,119-120`; `runner/internal/network/proxy.go:39-43`. | P1 shared host normalization and tests. |

### Recent Fixes Not To Reimplement

The following previous concerns are materially addressed by the recent task-session work:

- Execution attempts now own execution identity and resource ownership.
- Stale runner events are checked against task/session/prompt/attempt hierarchy.
- Workspace pruning is execution-aware.
- Artifact attribution includes execution attempts.
- Runner GitHub artifact tools are no longer exposed through the control-plane MCP surface.
- Historical event conversion now includes task IDs.
- Task/execution IDs reject path separators.
- Concurrent Codex state is no longer shared across execution instances.
- Harness capabilities are now split between common, serve-mode, RPC-mode, and output-piping interfaces.
- Readiness polling and SSE parsing are shared in `runner/harness/transport`.
- Runner environment and Docker environment policy is centralized in `runner/internal/agentenv` and `runner/internal/controller/docker_args.go`.
- Regular and resume Docker serve setup is shared.
- Process-group cancellation is implemented for Unix and process cancellation for Windows.
- Normal drain waits for active tasks and has a configurable deadline.
- Watchdog shutdown is joined.
- MCP and proxy servers now own and join their serving goroutines during shutdown.
- Workspace destruction reports traversal, chmod, and removal errors.

These changes are structural or lifecycle improvements, not complete security fixes. They still need regression tests and exact hierarchy-scoped mutations, but they should not be redesigned from scratch.

## Implementation Phases

### P0. Authorization And Isolation

#### P0.1 Centralize authorization scope

Create one scope helper used by Web API, ConnectRPC, and MCP service methods. It must distinguish:

- admin/unconstrained access;
- a non-admin scope with one or more allowed teams;
- a constrained scope whose requested filter has an empty intersection.

An empty constrained intersection must produce no rows or an authorization error. It must never select an unfiltered query.

Apply the helper to tasks, sessions, trigger runs, artifacts, repositories, fleet health, definitions, definition sources, definition proposals, Git identities, and all destructive operations.

Make `chetter_drain_runner` admin-only. Fleet health for team tokens should either return aggregate-only data or filter all task details to the caller's teams.

#### P0.2 Scope definitions and proposals

Add one definition resolver that accepts `{source_id, definition_type, name, team_id, repo}` and applies explicit precedence:

```text
repo > team > global
```

Use it for runner agent, skill, and MCP endpoint materialization as well as read tools. Enforce source ownership before proposal creation and allow global source writes only for admins.

Add tests for same-name global/team/repository definitions and cross-team read/write attempts.

#### P0.3 Authenticate runner task services

Generate an execution-scoped secret when an execution starts. Use it for:

- the per-execution GitHub MCP server;
- the Chetter MCP relay;
- any future runner-local service.

The server must validate `{runner_id, execution_id, attempt_id, capability}` before dispatching a tool. A peer task must receive `401` or `403` even if it discovers another task's port.

Bind the runner-local GitHub RPC operations to the authenticated runner identity, active execution attempt, lease, and execution ID. The static runner token may authenticate the process initially, but it must not be the only identity check. Prefer a per-runner session credential or mTLS-bound runner registration.

Add tests for another task, another runner, pending attempt, completed attempt, reclaimed attempt, and expired lease.

#### P0.4 Remove secrets from untrusted tasks

Remove provider keys from `runnerOwnedEnvKeys`, `providerCredentialEnv`, Docker arguments, local child environments, and harness-generated workspace configuration. Do not copy OpenCode, Claude, Pi, or other provider auth files into Docker workspaces.

Use the existing credential-forwarder direction from `docs/plans/2026-06-27-001-feat-agent-auth-broker-plan.md`, extended to MCP relay credentials:

```text
task capability -> authenticated runner forwarder -> trusted runner credential -> provider/MCP service
```

No real model or MCP credential should appear in task environment, process arguments, workspace, checkpoint, export, or logs.

#### P0.5 Enforce the untrusted execution network boundary

Until non-gVisor network isolation is implemented, reject forwarder-protected or untrusted tasks on runners that do not provide enforced isolation. Do not treat `HTTP_PROXY`, `HTTPS_PROXY`, or `NO_PROXY` as a security control.

For the eventual non-gVisor path, use per-execution Docker networks or an equivalent firewall policy that:

- blocks peer task-to-task traffic;
- permits only authenticated runner services;
- enforces provider/MCP egress policy;
- cannot be overridden by task environment variables.

#### P0.6 Establish one safe path boundary

Add a shared runner path utility for:

- `ExtraFiles`;
- agent and skill materialization;
- resume workspace/checkpoint paths;
- host workspace translation;
- Docker bind mounts;
- cleanup and pruning.

Reject absolute paths, `..` traversal, paths outside the configured root, and symlink escapes. Revalidate the path immediately before a mount or deletion.

## P1. Execution And Shutdown Reliability

### P1.1 Introduce an execution lifecycle owner

The runner-cleanups merge partially implements this boundary: watchdogs, event watchers, process groups, and several serving goroutines now have explicit shutdown paths. Complete the boundary with an execution runtime object that owns:

- execution context and cancellation;
- child process/container;
- MCP server and relay;
- progress watcher;
- terminal report worker;
- workspace cleanup;
- one terminal-state `sync.Once`.

Run owned goroutines under an `errgroup.Group` or equivalent wait group. Publish a terminal event only after process/container cleanup and required export collection. Preserve the current execution hierarchy and attempt fencing.

Task-level panic recovery should publish a structured failure and release all resources without re-panicking into the claim loop.

### P1.2 Correct draining and reporting

Separate these states:

```text
accepting work -> draining -> no new claims -> all executions stopped -> runner stopped
```

`waitDrain` now waits for active tasks and enforces a deadline. It still returns after deadline cancellation without waiting for task cleanup. Complete the barrier so the runner waits for all owned executions up to the configured shutdown deadline. Terminal reports must be bounded, retried through a runner-owned queue, and joined during shutdown.

Add tests for drain beginning with zero tasks, drain with active tasks, deadline cancellation, report retry, and shutdown during a report.

### P1.3 Make process I/O cancellation-safe

Process-group cancellation and process waiting now exist. Complete the small process abstraction around `exec.Cmd` and stdin/stdout handling, and move Codex and generic RPC writes behind a serialized writer with explicit close behavior. A blocked child write must not hold the application mutex indefinitely.

Only one prompt may be active per harness session. Reject or queue concurrent prompts instead of resetting shared turn state.

### P1.4 Enforce resource limits

Apply `MaxMemoryMB` and `MaxCPU` consistently to regular Docker, resume, and RPC Docker arguments. If local execution cannot enforce them, reject non-default requests in local mode rather than silently ignoring them.

Add assertions over generated Docker arguments and integration coverage with a fake Docker executable.

### P1.5 Make cleanup explicit and fail closed

Workspace cleanup now reports traversal, chmod, and deletion errors, but callers still log and continue. Make cleanup fail closed, add symlink/no-follow behavior using `Lstat` where possible, and never silently continue after deleting the wrong path or failing to remove a sensitive workspace.

Add checkpoint and workspace garbage collection for expired sessions, including retained execution directories and gVisor checkpoints.

### P1.6 Finish host policy normalization

Centralize hostname normalization for DNS and HTTP policy. Normalize trailing dots, case, ports, and IPv6 forms before matching. Add tests proving equivalent host spellings cannot bypass allow/block rules.

## P2. Server Data And Background Correctness

### P2.1 Move raw SQL behind dialect-aware repositories

Move task/session repository filtering and FTS queries from `internal/service/fts.go` into both MySQL/TiDB and PostgreSQL sqlc query files. Remove direct `?` construction from service code.

Add parity tests for status, team, repository, agent, search, pagination, and empty-result behavior on both dialects.

### P2.2 Make dialect detection fail closed

A failed version probe must not silently select TiDB. Use the DSN scheme when available, require an explicit dialect override when detection is ambiguous, and return a startup error rather than running with an unsafe driver/query set.

Test PostgreSQL, MySQL, and TiDB startup behavior for valid, invalid, and unavailable probes.

### P2.3 Make team deletion transactional

Define the deletion graph explicitly:

```text
team -> tokens/users -> tasks -> sessions/prompts/attempts/events/artifacts
     -> triggers/runs/callbacks/identities/definitions/proposals
```

Implement the cascade in a transaction with dialect-specific queries or foreign keys. Never delete a trigger by an unrelated team name. Add rollback and partial-failure tests.

### P2.4 Serialize definitions and scheduler state

Use one service-level sync mutex around Git pull, definition materialization, model catalog updates, trigger updates, and cron reconciliation. Use stable trigger identity derived from source/name rather than a new random ID on every sync.

On sync:

- upsert unchanged trigger identity;
- update cron entries in place;
- remove or disable entries removed from the source;
- preserve dynamic triggers according to the documented ownership rules;
- update `next_run_at` under the same synchronization policy.

Protect every `cronEntries` read and write with the same mutex.

### P2.5 Make webhooks and callbacks durable and bounded

Replace in-memory-only webhook acknowledgement with a durable delivery/intake record or outbox. A request may return success after the delivery is durably queued, not merely after it is placed in an in-memory dedup map.

Add:

- bounded dedup eviction;
- retry and dead-letter behavior;
- callback provenance and recursion depth;
- idempotency keys for callback-created tasks;
- per-callback rate limits;
- SSRF-safe webhook destination validation;
- shutdown-aware workers.

Reject loopback, link-local, private, metadata, and control-plane destinations unless explicitly allowed by operator policy.

### P2.6 Make event streams cursor-safe

Subscribe before replaying history, then replay through a durable event cursor that excludes duplicates. Prefer monotonically ordered event IDs or `(created_at, id)` cursors over timestamp-only filtering.

Make event bus unsubscribe idempotent and make stream loops observe subscriber closure as well as request cancellation.

### P2.7 Complete hierarchy attribution and idempotency

Carry exact session, prompt, and attempt IDs through every runner mutation instead of re-selecting the latest row by task ID. This is especially important for reclaim, pause, resume, terminal updates, and cleanup.

Add operation IDs for GitHub side effects. If GitHub creation succeeds but artifact persistence fails, retries must reconcile the existing artifact rather than create a duplicate.

Add hierarchy fields to audit records or store them in structured audit payloads.

## P3. Modularization And Simplification

Do this after P0/P1 seams exist; avoid a large rewrite before the security and lifecycle contracts are tested.

### P3.1 Server decomposition

No server decomposition was included in runner-cleanups. Split the current `Service` responsibilities into focused components:

- `TaskService`: submission, access, cancellation, usage, and task history.
- `ExecutionService`: runner claims, event acceptance, leases, and hierarchy transitions.
- `SessionService`: sessions, prompts, checkpoints, recovery, and resume.
- `SchedulerService`: cron triggers and trigger runs.
- `DefinitionService`: source sync, scope resolution, and proposals.
- `IntegrationService`: GitHub, Arcane, webhooks, and callbacks.

Keep the generated repository facade as the storage boundary. Inject small interfaces only where they improve testing; do not create interfaces for every method.

### P3.2 Runner decomposition

Runner-cleanups partially implemented this phase: harness capabilities, shared transport, Docker serve setup, and environment policy are now extracted. Continue by splitting the remaining `runner_task.go` responsibilities into:

- execution lifecycle;
- workspace and Git preparation;
- Docker/local launch;
- RPC subprocess execution;
- environment and credential policy;
- MCP setup;
- reporting and cleanup.

The shared Docker serve builder now exists; keep regular, resume, and RPC paths on it and add coverage for the remaining resource and isolation flags.

The `harness.Harness` capability split now exists. Retain the capability boundaries and remove any remaining mode-specific coupling only where lifecycle tests demonstrate a need; do not reintroduce no-op methods.

### P3.3 Centralize authentication and environment policy

Runner environment policy is now centralized, but it still exports real credentials into task processes and containers. Use one token extraction/authentication implementation for main HTTP middleware, Web API middleware, and Connect interceptors, and extend the environment policy to distinguish:

- trusted runner-only variables;
- task-safe variables;
- execution-scoped capabilities;
- forbidden secrets.

The helper duplication in `runner_task.go` has been removed. The remaining work is to make the centralized policy forbid untrusted credential exposure and to align it with the credential forwarder and authenticated MCP capability design.

## P4. Test Coverage Plan

The target is meaningful behavioral coverage, not an arbitrary percentage. The current post-merge profile is 37.5% overall; command wrappers and interfaces depress the aggregate while critical controller paths remain lightly tested.

Runner-cleanups added or expanded coverage for:

- drain task-change notification, deadline cancellation, and timeout configuration;
- watchdog stop and goroutine completion;
- shared readiness and multiline SSE transport behavior;
- environment policy and managed credential handling;
- harness capability selection;
- Docker serve argument construction.

Priority test suites:

1. Controller lifecycle: claim, heartbeat, cancellation, drain completion after deadline, panic containment, terminal arbitration, report retry, and shutdown.
2. Task orchestration: local/Docker/resume paths, timeout, cleanup, checkpoint, resource arguments, and path validation.
3. Security boundaries: model credentials absent, MCP relay authentication, per-execution MCP isolation, runner GitHub claim validation, and non-gVisor rejection.
4. RPC subprocesses: writer cancellation, malformed JSONL, EOF, prompt serialization, UI requests, process failure, and export.
5. MCP: HTTP `tools/list`, authenticated `tools/call`, invalid arguments, handler errors, and cross-execution access.
6. Harness configuration: OpenCode auth-state exclusion, endpoint translation, bearer headers, tar path traversal, and resume config.
7. Server authorization: disjoint team filters, definition precedence, fleet health, repositories, proposals, drain, and artifacts.
8. Server background systems: cron synchronization, callback recursion/idempotency, webhook dedup/retry, event replay, and shutdown.
9. Database parity: PostgreSQL and MySQL/TiDB query behavior, migrations, team deletion, and failed dialect detection.

Required regression tests for already-fixed work:

- stale execution event rejection;
- execution-scoped workspace pruning;
- artifact attempt attribution;
- runner-only GitHub MCP registration;
- per-execution harness state;
- task/execution path validation.
- process-group cancellation and process wait behavior;
- harness capability selection;
- environment policy and Docker environment construction;
- shared harness transport readiness and SSE parsing.

## Rollout Order

1. Ship P0 authorization and runner-service authentication with deny-by-default tests.
2. Ship P0 credential and network isolation; gate untrusted work on capable runners.
3. Ship P1 lifecycle barriers and resource enforcement.
4. Ship P2 SQL, migration, scheduler, webhook, callback, and stream correctness.
5. Refactor server and runner modules behind the tested seams.
6. Update `docs/PLAN.md`, `docs/FEATURES.md`, and `docs/TASK_SESSION_MODEL_REFACTOR.md` to distinguish data-model completion from remaining operational hardening.

Each phase should land independently with its focused tests and a rollback path. No phase should silently preserve an insecure fallback while the new path is unavailable.

## Definition Of Done

- Team-scoped requests cannot broaden to unscoped data.
- Runner task services reject cross-execution access.
- Model, MCP, GitHub, and provider credentials are not exposed to untrusted task environments.
- Non-gVisor untrusted execution cannot bypass network policy.
- Drain waits, task panics are contained, and shutdown joins owned work.
- Every execution has one authoritative terminal result after cleanup.
- Resource limits are either enforced or explicitly rejected.
- PostgreSQL and MySQL/TiDB repository behavior is query-equivalent.
- Team deletion, definition sync, callbacks, webhooks, and streams are transactional or durable where required.
- Server and runner core lifecycle tests cover the failure paths listed above.
- Staticcheck, vet, unit tests, race tests, and dialect integration tests remain green.
