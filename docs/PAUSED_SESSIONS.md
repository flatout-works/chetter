# Paused Sessions

## Status

Proposal for implementing resumable agent sessions using gVisor checkpoint/restore.

**Current gVisor deployment (June 2026):** Runners now execute agent containers under gVisor (`runsc`) with bridge-network isolation (`--network chetter_default`). Provider DNS is handled via `--add-host` aliases. The default fallback model is `opencode/deepseek-v4-flash-free`. Session export uses a DB-read-then-HTTP-API fallback because the opencode SQLite DB lives under `$HOME=/opt/opencode` inside the container, not the bind-mounted workspace.

This document describes the desired product behavior, data model, runner affinity rules, workspace preservation strategy, and implementation phases. It intentionally limits true pause/resume support to gVisor-backed runners. Non-gVisor runners continue to support normal one-shot tasks only.

## Problem

Chetter currently treats agent work as a single task lifecycle:

1. Clone repository into a fresh workspace.
2. Start an agent container.
3. Create one agent session.
4. Send one prompt.
5. Stream events until the prompt completes.
6. Export session state where possible.
7. Destroy the container and workspace.

This works for independent tasks, but it breaks feedback-loop workflows.

Example:

1. Agent A creates a pull request.
2. Agent B reviews the pull request and leaves feedback.
3. Agent A should wake up, read the feedback, and improve the same PR.

Today step 3 requires starting a new task from scratch. The new agent can read the repository, PR, and previous session export, but it is not the same running agent session. It has lost process memory, in-flight tool state, local caches, open session state, and any uncommitted context that was not preserved through Git.

## Goals

- Introduce `agent_sessions` as the long-lived unit of agent work.
- Introduce `session_runs` as individual prompt executions inside an agent session.
- Allow gVisor-backed sessions to pause after a run and resume later with another prompt.
- Keep Chetter useful without gVisor by allowing exactly one finished `session_run` per `agent_session`.
- Track which Chetter session created each GitHub PR, issue, review, or comment.
- Route review feedback back to the original paused session when possible.
- Guarantee same-runner restore for v1.
- Preserve the workspace directory for paused sessions instead of attempting cross-node workspace migration.
- Avoid transcript replay as a fake resume mechanism.

## Non-Goals

- No non-gVisor pause/resume fallback.
- No LLM transcript import/replay resume mode.
- No cross-runner restore in v1.
- No periodic mid-run checkpoints in v1.
- No checkpointing while an LLM request is actively in flight in v1.
- No Kubernetes-native pod spawning rewrite in v1.
- No workspace filesystem portability guarantee in v1.

## Core Concept

An `agent_session` represents a durable agent identity and workspace across one or more prompts.

A `session_run` represents one prompt sent to that session.

For non-gVisor execution:

```text
agent_session sess_1
  session_run run_1 -> completed
agent_session -> completed
```

For gVisor resumable execution:

```text
agent_session sess_1
  session_run run_1 -> completed
agent_session -> paused_waiting_review

review feedback arrives

agent_session sess_1 -> resuming -> running
  session_run run_2 -> completed
agent_session -> paused_waiting_review or completed
```

The important distinction is that a completed `session_run` does not necessarily mean the `agent_session` is complete.

## Runtime Behavior

### Without gVisor

Runners without gVisor keep the current behavior:

- One prompt per container.
- One `session_run` per `agent_session`.
- Container and workspace are destroyed after terminal completion.
- Attempts to pause/resume should be rejected explicitly.

The system should not offer replay-based resume, because that is not true continuation.

### With gVisor

Runners with `USE_GVISOR=true` start agent containers with Docker's `runsc` runtime:

```text
docker run --runtime runsc ...
```

After a run completes and the session is configured to remain resumable, the runner creates a gVisor checkpoint and preserves the workspace directory. The container can then be stopped while the session remains logically paused.

On resume, the same runner restores the checkpoint, reattaches to the agent process, and sends a new prompt into the restored session.

## Data Model

### `agent_sessions`

Suggested columns:

| Column | Purpose |
|---|---|
| `id` | Stable session ID, e.g. `sess_...` |
| `team_id` | Team scope |
| `status` | `running`, `paused_waiting_review`, `paused`, `resuming`, `completed`, `expired`, `error` |
| `resume_mode` | `none` or `gvisor_checkpoint` |
| `pinned_runner_id` | Runner that owns the checkpoint and workspace |
| `pinned_runner_name` | Optional human/debug value |
| `checkpoint_id` | Latest checkpoint metadata row |
| `workspace_path` | Host workspace path on the pinned runner |
| `container_name` | Docker/runsc container name |
| `harness_session_id` | OpenCode/Claude session ID where available |
| `git_url` | Repository URL |
| `git_ref` | Original ref |
| `agent_image` | Runner image used for the agent container |
| `agent` | Agent name |
| `provider_id` | Provider ID |
| `model_id` | Model ID |
| `variant_id` | Model variant |
| `created_at` | Creation timestamp |
| `updated_at` | Last update timestamp |
| `paused_at` | Last pause timestamp |
| `expires_at` | TTL for paused session cleanup |
| `error` | Terminal or restore error |

### `session_runs`

Suggested columns:

| Column | Purpose |
|---|---|
| `id` | Stable run ID, e.g. `run_...` |
| `agent_session_id` | Parent session |
| `task_id` | Backing queue task ID, if task rows remain the claimable unit |
| `status` | `pending`, `claimed`, `running`, `completed`, `failed`, `cancelled` |
| `prompt` | Prompt for this run |
| `required_runner_id` | Runner affinity for resume runs |
| `started_at` | Run start timestamp |
| `ended_at` | Run end timestamp |
| `session_export` | Export from the run, if available |
| `summary` | Run summary |
| `error` | Run error |

### `agent_session_checkpoints`

Suggested columns:

| Column | Purpose |
|---|---|
| `id` | Stable checkpoint ID |
| `agent_session_id` | Parent session |
| `session_run_id` | Run after which checkpoint was created |
| `runner_id` | Runner that created the checkpoint |
| `checkpoint_path` | Host path to gVisor checkpoint directory |
| `workspace_path` | Host path to preserved workspace |
| `container_name` | Container name at checkpoint time |
| `runsc_version` | Optional diagnostic value |
| `agent_image` | Image used by checkpointed container |
| `size_bytes` | Best-effort checkpoint size |
| `created_at` | Creation timestamp |
| `expires_at` | GC deadline |
| `status` | `creating`, `ready`, `restoring`, `deleted`, `error` |
| `error` | Checkpoint/restore error |

### `agent_session_artifacts`

Suggested columns:

| Column | Purpose |
|---|---|
| `id` | Stable artifact ID |
| `agent_session_id` | Session that created or owns the artifact |
| `session_run_id` | Run that created or updated the artifact |
| `task_id` | Task that created or updated the artifact |
| `provider` | `github` initially |
| `artifact_type` | `pull_request`, `issue`, `comment`, `review` |
| `repo` | GitHub repo, e.g. `flatout-works/chetter` |
| `number` | PR or issue number |
| `external_id` | Provider-specific ID where available |
| `url` | Artifact URL |
| `created_at` | Creation timestamp |
| `updated_at` | Last update timestamp |

## Footer Signature

Chetter-created PRs, issues, reviews, and comments should include session metadata in addition to task metadata.

Example footer:

```text
---
Generated by [Chetter](https://github.com/flatout-works/chetter)
Session: sess_abc123
Run: run_def456
Task: task_789
Agent: pr-implementer | Model: opencode/minimax-m3 | Runner: ghcr.io/flatout-works/chetter-runner:main (sha256:...)
```

Webhook artifact discovery should parse `Session`, `Run`, and `Task` footers. When only old `Task` footers exist, Chetter may continue to associate artifacts with tasks, but resumable behavior requires session ownership.

## Same-Runner Restore

v1 restore must happen on the same runner that created the checkpoint.

When pausing a session, store:

- `agent_sessions.pinned_runner_id`
- `agent_sessions.workspace_path`
- `agent_sessions.checkpoint_id`
- `agent_session_checkpoints.checkpoint_path`

When creating a resume run, set:

```text
session_runs.required_runner_id = agent_sessions.pinned_runner_id
```

If tasks remain the claimable queue unit, mirror this onto the task row:

```text
chetter_tasks.required_runner_id = agent_sessions.pinned_runner_id
```

The claim query must only return pinned resume work to the pinned runner:

```sql
WHERE status = 'pending'
  AND (
    required_runner_id IS NULL
    OR required_runner_id = :runner_id
  )
ORDER BY created_at
LIMIT 1
FOR UPDATE SKIP LOCKED
```

Before enqueueing a resume run, check runner health:

- If the pinned runner is alive, enqueue the resume run.
- If the pinned runner is offline, leave the session paused and report `runner_offline`.
- Do not allow another runner to claim the resume run in v1.

## Workspace Preservation

Chetter currently bind-mounts the host workspace into the agent container:

```text
-v {workspace_dir}:/workspace
```

The gVisor checkpoint captures process state, but the bind-mounted workspace files still live on the runner host. A valid restore therefore needs both:

```text
gVisor checkpoint directory
+ original preserved workspace directory
```

### v1 Strategy: Preserve Directory

The v1 workspace strategy should be deliberately simple:

1. Checkpoint only after a run completes, when the agent is idle.
2. Let the checkpoint stop the container.
3. Do not delete the workspace directory.
4. Store the workspace path on `agent_sessions` and `agent_session_checkpoints`.
5. Restore only on the same runner, using the original workspace path.

This avoids unsafe copies of files while the process is mutating them.

### Why Not Tar First?

A tar archive of visible workspace files is not guaranteed to be a safe companion for a process checkpoint. The restored process may depend on:

- Open file descriptors.
- File offsets.
- Deleted-but-open files.
- Memory-mapped files.
- Temporary files created during tool execution.

Tar is useful for export, backup, and future portability. It should not be the v1 correctness mechanism for true process resume.

### Future Snapshot Backends

Introduce a pluggable workspace snapshotter later:

```go
type WorkspaceSnapshotter interface {
    Snapshot(ctx context.Context, workspacePath, snapshotID string) (Metadata, error)
    Restore(ctx context.Context, snapshotID, targetPath string) error
    Delete(ctx context.Context, snapshotID string) error
}
```

Potential backends:

| Backend | Use |
|---|---|
| `preserve-dir` | v1 default, same-runner only |
| `tar-zstd` | Portable backup/export, not primary true-resume path |
| `btrfs` subvolume snapshots | Best local filesystem option if runner nodes are controlled |
| `zfs` datasets | Strong snapshot semantics, higher ops burden |
| XFS reflink copy | Fast copy-on-write files, not a full atomic tree snapshot by itself |
| CSI volume snapshots | Kubernetes-native option, storage-provider-specific |

## gVisor Checkpoint Operations

Preferred raw `runsc` shape:

```bash
runsc checkpoint --image-path=/var/lib/chetter/checkpoints/sess_abc/run_1 chetter-task-task_123
```

Restore shape:

```bash
runsc create chetter-task-task_123-restored
runsc restore --image-path=/var/lib/chetter/checkpoints/sess_abc/run_1 chetter-task-task_123-restored
```

Docker checkpoint shape:

```bash
docker checkpoint create chetter-task-task_123 checkpoint-1
docker start --checkpoint checkpoint-1 chetter-task-task_123
```

The raw `runsc` path is likely necessary for restoring into new containers and for better control over image paths. Docker's checkpoint API is useful for proof-of-concept work but has limitations around restoring into different containers and checkpoint directories.

Recommended v1 checkpoint policy:

- Checkpoint after `SendPrompt` returns successfully.
- Do not use `--leave-running` in v1.
- Do not checkpoint while prompt execution is active.
- Preserve workspace after checkpoint.
- Mark session paused only after checkpoint metadata is durable.

Future optimizations:

```bash
runsc checkpoint \
  --image-path=/var/lib/chetter/checkpoints/sess_abc/run_1 \
  --compression=none \
  --exclude-committed-zero-pages \
  chetter-task-task_123

runsc restore \
  --image-path=/var/lib/chetter/checkpoints/sess_abc/run_1 \
  --background \
  chetter-task-task_123-restored
```

## PR Feedback Flow

1. A resumable trigger or task creates an `agent_session`.
2. Chetter creates `session_run` 1 and starts the agent container.
3. The agent creates or updates a PR.
4. Artifact discovery records the PR in `agent_session_artifacts`.
5. The run completes successfully.
6. The runner checkpoints the container and preserves the workspace.
7. Chetter marks the session `paused_waiting_review`.
8. A PR review or comment webhook arrives.
9. Chetter looks up the PR in `agent_session_artifacts`.
10. If the owning session is paused and resumable, Chetter creates `session_run` 2 with `required_runner_id` set to the pinned runner.
11. The pinned runner claims the resume run.
12. The runner restores the checkpoint and sends a follow-up prompt.
13. The agent reads review feedback, updates the PR branch, and completes the run.
14. The session either pauses again or is marked completed, depending on policy.

Example follow-up prompt:

```text
Your PR #123 received review feedback.

Read the PR review comments and review threads using gh.
Address the feedback with the smallest correct changes.
Push updates to the existing branch.
Reply to resolved review comments where appropriate.
Do not open a new PR.
```

## Configuration

Resumability should be explicit per task or trigger.

Example trigger config:

```json
{
  "session": {
    "mode": "resumable",
    "pause_after_success": true,
    "pause_reason": "waiting_for_pr_feedback",
    "ttl_hours": 72
  }
}
```

For non-gVisor runners, `mode: resumable` should be rejected or the trigger should be considered unschedulable for runner pools without gVisor support. Do not silently downgrade to transcript replay.

## MCP Tools

Initial tools:

| Tool | Purpose |
|---|---|
| `chetter_list_agent_sessions` | List sessions, filter by status/team/repo |
| `chetter_agent_session_status` | Show session, latest run, checkpoint, artifacts, runner affinity |
| `chetter_resume_agent_session` | Manually enqueue a follow-up prompt for a paused session |
| `chetter_cancel_agent_session` | Cancel active run or expire paused session |
| `chetter_expire_agent_session` | Delete checkpoint/workspace and mark expired |

Existing task tools can continue to expose task/run-level status while session tools expose long-lived state.

## Runner Responsibilities

The runner must be able to:

- Advertise whether gVisor checkpoint/restore is supported.
- Advertise a stable `runner_id` used for restore affinity.
- Start normal new sessions.
- Start resume runs pinned to itself.
- Checkpoint containers after successful runs.
- Preserve paused workspaces.
- Restore containers from checkpoints.
- Verify checkpoint path and workspace path before restore.
- Garbage collect expired checkpoints and workspaces only after server-side state allows it.

Runner health should include checkpoint capability:

```json
{
  "runner_id": "runner_abc",
  "gvisor_enabled": true,
  "checkpoint_restore": true,
  "runsc_version": "..."
}
```

## Server Responsibilities

The server must be able to:
- Create sessions and runs.
- Enforce one active run per session.
- Enforce runner affinity on resume runs.
- Track GitHub artifact ownership.
- Route webhook feedback to paused sessions.
- Keep paused sessions scoped by team.
- Refuse resume when the pinned runner is offline.
- Expire paused sessions after TTL.
- Expose session state through MCP tools.

## State Machines

### Agent Session

```text
running
  -> paused_waiting_review
  -> resuming
  -> running
  -> paused_waiting_review
  -> completed

running -> error
paused_waiting_review -> expired
paused_waiting_review -> error
```

### Session Run

```text
pending
  -> claimed
  -> running
  -> completed

pending -> cancelled
running -> failed
running -> cancelled
```

## Implementation Phases

### Phase 1: Data Model and Ownership

- Add `agent_sessions` table.
- Add `session_runs` table.
- Add `agent_session_checkpoints` table.
- Extend artifact tracking to include `agent_session_id` and `session_run_id`.
- Extend Chetter footer format with `Session` and `Run`.
- Keep current task behavior unchanged.

### Phase 2: Runner Affinity

- Add `required_runner_id` to the claimable work unit.
- Update claim query to honor runner affinity.
- Expose runner checkpoint capability in health/heartbeat.
- Add server validation for pinned resume work.

### Phase 3: gVisor Pause After Success

- Add resumable session config.
- For gVisor runners, checkpoint after successful run.
- Preserve workspace instead of destroying it.
- Mark session `paused_waiting_review`.
- Add TTL and manual expiration.

### Phase 4: Manual Resume

- Add `chetter_resume_agent_session`.
- Enqueue a pinned resume run.
- Restore checkpoint on pinned runner.
- Send follow-up prompt into restored agent session.
- Mark run completed and pause or complete session based on policy.

### Phase 5: Webhook-Driven Resume

- On PR review/comment webhook, look up owning `agent_session_artifacts` row.
- If the session is paused and resumable, enqueue a pinned resume run.
- Generate a targeted feedback prompt.
- Avoid duplicate resume runs for the same review event.

### Phase 6: Workspace Snapshot Backends

- Add `WorkspaceSnapshotter` interface.
- Keep `preserve-dir` as default.
- Add `tar-zstd` for export/debug/backup.
- Evaluate `btrfs` for controlled runner nodes.

### Phase 7: Advanced Checkpointing

- Consider `--leave-running` periodic checkpoints.
- Consider `--background` restore.
- Consider cross-runner restore with shared storage and CPU feature pinning.
- Consider prewarmed agent pools.

## Open Questions

1. Should resumable mode be configured per trigger, per task, or both?
2. Should PR review feedback always resume the original authoring session, or only when a label/comment command requests it?
3. What TTL should paused sessions use by default?
4. Should a paused session be able to own multiple PRs/issues, or should v1 enforce one primary artifact?
5. Should runner affinity be stored on `chetter_tasks`, `session_runs`, or both during migration?
6. Should checkpoint paths live under the existing workspace root or a new root like `/var/lib/chetter/checkpoints`?
7. How should checkpoint files be encrypted or protected, given they may contain secrets in memory?
8. Should session pause fail the run if checkpoint creation fails, or should the run complete and the session become non-resumable?
9. How should Chetter handle a pinned runner that never comes back before TTL expiry?

## Recommended v1 Slice

Build the smallest useful version for the PR feedback loop:

1. `agent_sessions` and `session_runs` exist.
2. GitHub PR artifacts are mapped to sessions and runs.
3. Resumable sessions require gVisor.
4. Pause happens only after a successful prompt.
5. The workspace directory is preserved in place.
6. Restore is same-runner only.
7. Review feedback can enqueue a pinned follow-up run.
8. Expired sessions delete checkpoint and workspace state.

This delivers the core feature without overbuilding cross-node storage, replay fallback, or mid-run checkpointing.
