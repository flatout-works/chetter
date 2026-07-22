# Task and Agent Session Model Refactor

Status: **Completed**

## Goal

Make the persisted model match the product concepts:

- A **Task** is the stable objective the user wants Chetter to achieve.
- An **AgentSession** is one conversational attempt to achieve that task.
- A **UserPrompt** is one user-authored turn in an agent session.
- An **ExecutionAttempt** is one lease-bound runner execution of a user prompt.

The target hierarchy is:

```text
Task
  +-- AgentSession 1
  |     +-- UserPrompt 1
  |     |     +-- ExecutionAttempt 1
  |     |     +-- ExecutionAttempt 2 (only if session state is preserved)
  |     +-- UserPrompt 2 (resume/follow-up)
  |           +-- ExecutionAttempt 1
  +-- AgentSession 2 (fresh retry/recovery)
        +-- UserPrompt 1
              +-- ExecutionAttempt 1
```

This separates product lifecycle from queue and runner lifecycle. In particular,
the Task ID remains stable across retries, recovery, and follow-up conversation.

## Terminology

### Task

A durable objective, such as "implement issue #46". It owns authorization,
repository and trigger origin, aggregate status, and artifacts produced while
pursuing the objective.

### AgentSession

One coherent agent conversation and workspace lineage. It owns the harness
session identity, resume policy, retained workspace/checkpoint, agent/model
configuration, and session-level outcome.

A task can have multiple agent sessions when work is restarted from scratch.

### UserPrompt

One user instruction added to an agent session. Rename the public
`SessionRun` concept to `UserPrompt`; it should not own runner leases or imply
that the prompt has only one execution.

Follow-up feedback and explicit resume operations create another UserPrompt in
the same AgentSession.

### ExecutionAttempt

One runner claim with an immutable ID and lease generation. It owns runner
assignment, lease, timestamps, timeout, workspace instance, harness execution
ID, result, token usage, and terminal diagnostics.

An old attempt must never be able to report events or clean resources belonging
to a newer attempt.

## Lifecycle Rules

### Initial submission

1. Create one Task.
2. Create AgentSession sequence 1.
3. Create UserPrompt sequence 1 from the submitted prompt.
4. Queue ExecutionAttempt sequence 1.

### Follow-up or resume

1. Keep the same Task and AgentSession.
2. Create the next UserPrompt in that AgentSession.
3. Queue its first ExecutionAttempt on the runner that owns the retained session
   when runner affinity is required.

### Cold reclaim

Use this path when the previous runner/session cannot prove continuity, including
ordinary non-resumable work reclaimed after lease expiry.

1. Mark the current ExecutionAttempt `lost`.
2. Mark its AgentSession `abandoned` or `recoverable`, depending on whether a
   transcript can be recovered.
3. Create a new AgentSession under the same Task.
4. Create its first UserPrompt from the stable task objective plus explicit
   recovery context when available.
5. Queue a fresh ExecutionAttempt with a new workspace.
6. Emit a first-class `task.session_restarted` event linking the old and new
   session IDs and the reclaim reason.

### Verified continuation

Only keep the same AgentSession/UserPrompt when Chetter can verify that the
workspace and harness session are the same retained state. Create a new
ExecutionAttempt with a new lease token and fence the old attempt.

### Explicit recovery

`RecoverTask` should stop creating a separate Task. It creates a new
AgentSession under the existing Task, optionally attaching the previous
session export as recovery context. Provide a separate `DuplicateTask` or
`ForkTask` operation when the user intentionally wants a new objective.

### Completion

An AgentSession succeeding can mark the Task succeeded. A failed or abandoned
session does not make the Task terminal while another session is pending,
running, paused, or recoverable.

## Workspace Policy

- Non-resumable/cold attempts always use a fresh, attempt-scoped workspace.
- Resumable sessions retain a session-scoped workspace or checkpoint explicitly.
- A workspace must never be keyed only by Task ID.
- Container and workspace names include the immutable ExecutionAttempt ID.
- Cleanup requires the matching attempt lease token/generation.
- Failure to clean or initialize a fresh workspace is fatal; deletion errors are
  not ignored.
- Stopped harness containers are removed unless a real process checkpoint owns
  them. Workspace retention and container retention are separate decisions.
- Startup pruning protects `running`, `resuming`, `paused`, and `recoverable`
  session resources and reconciles ownership with the control plane.

Suggested layout:

```text
workspaces/
  sessions/<agent-session-id>/retained/
  attempts/<execution-attempt-id>/workspace/
```

## Data Model

### `chetter_tasks`

Retain objective-level fields:

- Identity, team, objective/prompt, repository and trigger origin.
- Aggregate status, summary/error, created/updated/completed timestamps.
- Active/latest agent session ID for efficient reads.
- Objective-level search text.

Move queue, lease, runner, harness session, per-run result, and token fields out
of this table.

### `chetter_agent_sessions`

Owns:

- `task_id` (required).
- `sequence` (unique within task).
- Start/end timestamps and session outcome.
- Agent/model/image/configuration snapshot.
- Resume mode, workspace/checkpoint, pinned runner, harness session identity.

### `chetter_user_prompts`

Replaces the former public role of `chetter_session_runs` and owns:

- `agent_session_id` (required).
- `sequence` (unique within session).
- Prompt text and source/origin prompt ID.
- Prompt-level aggregate status, summary, error, and transcript/export reference.
- Created/started/ended timestamps.

### `chetter_execution_attempts`

Owns:

- `user_prompt_id`, sequence, immutable lease token/generation.
- Runner and required runner IDs.
- Pending/claimed/running/lost/succeeded/failed/cancelled status.
- Claim and lease timestamps, timeout, start/end timestamps.
- Workspace/container/harness execution identity.
- Per-attempt summary, error category, transcript/export, and token usage.

### Events and artifacts

Every event records:

- `task_id`
- `agent_session_id`
- `user_prompt_id`
- `execution_attempt_id`

Artifacts are owned by Task and record the session, prompt, and attempt that
created or discovered them. Objective-level artifact deduplication must preserve
the contributing prompt/attempt history rather than dropping duplicate links.

## API Cutover

Change the API and runner protocol directly:

- Replace `SessionRun` with `UserPrompt` everywhere.
- Add `ExecutionAttempt` messages and task history APIs.
- Add all four IDs to runner claims, events, heartbeats, and cancellation.
- Remove old session-run endpoints, MCP response fields, and UI terminology.
- Regenerate and deploy the server, runner, and web client together.

There is no legacy API, URL, task-ID, or mixed-runner compatibility period.

The task detail API should expose pagination metadata or use cursor pagination
for events. The web timeline should load the newest page initially and provide
`Load older` until history is exhausted.

## Timeline UX

Display a grouped hierarchy rather than one undifferentiated stream:

```text
Agent session 2 - fresh restart
  User prompt 1
    Attempt 1 - running on runner B

Reclaimed after runner A stopped renewing its lease

Agent session 1 - abandoned
  User prompt 1
    Attempt 1 - lease lost
```

Requirements:

- Insert explicit session-created, attempt-claimed, lease-lost, reclaimed, and
  session-restarted events.
- Show session/prompt/attempt IDs and runner identity in expandable details.
- Label events from older sessions and attempts.
- Provide `Load older` and a clear "showing N of M" or `has_more` indication.
- Continue suppressing repetitive heartbeat rows while showing heartbeat health
  as attempt metadata.

## Breaking Migration Strategy

This is a coordinated breaking cutover. Stop the server and runners, migrate the
database once, deploy all components, and restart the fleet. Do not dual write
or retain compatibility tables.

If preserving current development history is worthwhile, the migration may
convert rows in place before dropping the old shape:

1. Add `task_id` and `sequence` to agent sessions.
2. Create UserPrompt and ExecutionAttempt tables.
3. Convert each current Task/SessionRun pair into a UserPrompt.
4. Convert current claim counters and events into best-effort ExecutionAttempts.
5. For resumed sessions, select the earliest Task as the stable Task and move all
   prompts, events, artifacts, and sessions under it.
6. Convert unambiguous recovery chains into AgentSessions under one Task.
7. Drop ambiguous or orphaned development records instead of carrying repair or
   compatibility machinery into the application.
8. Add the four hierarchy IDs to events and artifacts and enforce foreign keys.
9. Remove execution fields from Task and drop `chetter_session_runs`.
10. Update startup schema, both dialect migrations and query trees, generated
    repositories, the data facade, and generated protobuf clients.

If the existing data has no value, reset the application tables and install the
new schema directly. This is preferable to adding permanent migration complexity.

### Domain operations

Replace the current orchestration with:

- `CreateTask`
- `StartAgentSession`
- `AddUserPrompt`
- `StartExecutionAttempt`
- `RestartTaskInNewSession`

### Queue ownership

Move claim, lease, heartbeat, cancellation, timeout, and terminal reporting from
Task to ExecutionAttempt. Require attempt identity and lease token on every
runner mutation. Reject stale reports and cleanup operations.

### Workspace and runner fencing

1. Use attempt-scoped workspace/container names.
2. Prevent overlapping attempts from sharing paths.
3. Make workspace initialization fail closed.
4. Scope startup cleanup by runner and attempt labels.
5. Wait for bounded cleanup during ordinary shutdown.
6. Reject resumable modes on runner/harness paths that cannot honor retention.

### Recovery and resume

- Resume adds a UserPrompt to the same session.
- Cold reclaim/recovery adds a new AgentSession to the same Task.
- Explicit task duplication is a separate operation.
- Task status becomes an aggregate of its sessions.

### API and UI

1. Add task-history endpoints and paginated event endpoints.
2. Group the task timeline by session, prompt, and attempt.
3. Add `Load older` with correct pagination across filtered progress events.
4. Update session pages to call runs `User prompts`.
5. Remove the old session-run pages, fields, and handlers in the same cutover.
6. Update lifecycle and paused-session documentation.

## Delivered Slices

The refactor was delivered in focused commits:

1. **Observability:** explicit reclaim event, attempt number in events/API, and
   paginated timeline with `Load older`.
2. **Execution fencing:** immutable attempt IDs, stale-report rejection, and
   attempt-scoped resources.
3. **Domain schema:** Task-to-session linkage, UserPrompt, ExecutionAttempt, and
   the one-time migration.
4. **Queue migration:** claims and leases move to ExecutionAttempt.
5. **Lifecycle migration:** recover/reclaim/resume adopt the target semantics.
6. **UI/API cutover:** grouped task history and terminology changes.

All six slices are complete. Server, runner, web API, generated repositories,
and both database dialects now use the hierarchy directly.

## Verification

Add integration coverage for:

- Initial Task/Session/UserPrompt/Attempt creation.
- Follow-up prompt in the same session.
- Cold reclaim creating a new session under the same task.
- Verified continuation retaining a session but creating a fenced attempt.
- Stale attempt events and cleanup being rejected.
- Non-resumable attempts receiving a clean workspace.
- Resumable workspaces surviving active resume and being removed on expiry.
- Concurrent resume requests producing at most one active prompt.
- Recovery preserving the Task ID and starting a new session.
- Timeline pagination without skipped or duplicated entries.
- MySQL/TiDB and PostgreSQL migrations and query parity.

Run root and runner checks independently (`make check` in each Go module), web
checks, Buf generation/lint, sqlc generation, facade generation, and dialect
integration tests.

## Final Decisions

1. Recoverability is represented on AgentSession; Task remains the aggregate
   objective rather than acquiring another execution-specific status.
2. ExecutionAttempt owns exports and diagnostics for the execution that produced
   them; UserPrompt exposes their aggregate outcome.
3. Cold restart creates a new AgentSession and UserPrompt under the same Task,
   with explicit recovery context when an export is available.
4. Harness-session resume and gVisor checkpoint resume remain distinct modes.
   Both require verifiable retained state and a pinned ExecutionAttempt.
