# Paused Sessions

Status: **Partially implemented - current behavior and remaining work**

Chetter now has first-class agent session records. A task is no longer only a one-shot row in `chetter_tasks`; every submitted task also creates an `agent_session` and a first `session_run`.

Resumable sessions use gVisor/Docker checkpoint metadata and runner affinity. Non-resumable sessions are still useful for lineage and UI/history, but they do not support true process restore.

## Concepts

An `agent_session` is the long-lived identity and workspace lineage for an agent.

A `session_run` is one prompt execution inside that session.

```text
agent_session sess_1
  session_run run_1 -> creates PR
agent_session -> paused_waiting_review

review feedback arrives

agent_session sess_1
  session_run run_2 -> addresses feedback
```

For a default one-shot task:

```text
agent_session sess_1
  session_run run_1 -> completed
agent_session -> completed
```

## Implemented Today

### Database Model

Migration `011_add_agent_sessions.sql` adds:

- `chetter_agent_sessions`
- `chetter_session_runs`
- `chetter_agent_session_checkpoints`
- `chetter_tasks.required_runner_id`
- `chetter_tasks.checkpoint_after_success`
- `chetter_task_artifacts.agent_session_id`
- `chetter_task_artifacts.session_run_id`

### Task Submission

Every submitted task creates:

- A `task_*` row in `chetter_tasks`.
- A `sess_*` row in `chetter_agent_sessions`.
- A `run_*` row in `chetter_session_runs`.

When `session_mode: resumable` is set:

- `resume_mode` becomes `gvisor_checkpoint`.
- `checkpoint_after_success` is set on the task.
- `pause_reason` is stored if provided.
- `expires_at` defaults to 72 hours unless `ttl_hours` is provided.

### Runner Affinity

Resume work is pinned to the runner that owns the checkpoint.

Task claiming respects `required_runner_id`:

```sql
WHERE status = 'pending'
  AND (
    required_runner_id IS NULL
    OR required_runner_id = ''
    OR required_runner_id = :runner_id
  )
```

If the pinned runner is not alive, manual resume fails and webhook-driven resume is skipped.

### Checkpoint Creation

When a resumable gVisor task completes successfully, the runner attempts:

```bash
docker checkpoint create <container> chetter-checkpoint-<taskID>
```

The runner reports checkpoint and workspace paths back to the server. The server stores a `ready` checkpoint row and moves the session to `paused_waiting_review`.

### Manual Resume

MCP tools:

- `chetter_list_agent_sessions`
- `chetter_agent_session_status`
- `chetter_resume_agent_session`

`chetter_resume_agent_session` checks that:

- The session is `paused` or `paused_waiting_review`.
- The session uses `resume_mode: gvisor_checkpoint`.
- A pinned runner exists.
- A ready checkpoint exists.
- The pinned runner is alive.

It then creates a follow-up task and `session_run` with `required_runner_id` set to the pinned runner.

### PR Feedback Resume

Webhook handling can look up a paused Chetter-authored PR session through `chetter_task_artifacts` and enqueue a follow-up run with a feedback-response prompt.

This path currently relies on server-side artifact records. Chetter-authored artifacts should continue to include `Task:` footers for compatibility, and future footers should include `Session:` and `Run:` when the creating task has session metadata.

## Current Workflow

Submit a resumable task:

```json
{
  "prompt": "Create a PR for the next useful documentation improvement.",
  "git_url": "https://github.com/flatout-works/chetter",
  "git_ref": "main",
  "harness": "opencode",
  "session_mode": "resumable",
  "pause_reason": "waiting_for_pr_feedback",
  "ttl_hours": 72
}
```

After the task completes, inspect sessions:

```json
{
  "status": "paused_waiting_review",
  "limit": 20
}
```

Resume manually:

```json
{
  "session_id": "sess_...",
  "prompt": "Address the review feedback on the existing PR branch and push the smallest correct changes.",
  "timeout_sec": 1800
}
```

## Rules

- True process resume requires gVisor.
- Resume is same-runner only for the current implementation.
- A session can resume only from a ready checkpoint.
- Non-gVisor sessions remain one-shot. They still create session/run records, but they should not fake process resume.
- Resume runs inherit the original session's repo, ref, image, agent, provider, model, and variant.
- Resume runs are pinned with `required_runner_id`.

## Workspace Strategy

Chetter bind-mounts host workspaces into containers:

```text
-v {workspace_dir}:/workspace
```

The gVisor checkpoint captures process state, but bind-mounted files live on the host. The current model stores both:

```text
gVisor/Docker checkpoint path
+ preserved workspace path
```

This avoids copying a workspace while the process may have open file descriptors, file offsets, mmap state, temporary files, or deleted-but-open files.

## Remaining Work

### Documentation And UX

- Add session views to the web UI if not already complete.
- Show checkpoint status, pinned runner, expiry, and latest run in the UI.
- Link tasks, session runs, artifacts, and GitHub PRs from each other.

### Footers And Artifact Ownership

- Extend Chetter artifact footers from `Task:` only to include `Session:` and `Run:`.
- Keep `Task:` parsing for backward compatibility.
- Prefer server-side artifact records when available.

Suggested footer:

```text
---
Generated by [Chetter](https://github.com/flatout-works/chetter)
Session: sess_abc123
Run: run_def456
Task: task_789
Agent: ... | Model: ... | Runner: ...
```

### Cleanup And Reliability

- Add TTL cleanup for expired paused sessions, checkpoints, and preserved workspaces.
- Add clear terminal error states for checkpoint creation failure and restore failure.
- Add operator actions for abandoning, expiring, or force-cleaning paused sessions.
- Add tests for real Docker/gVisor checkpoint restore, not only metadata transitions.

### Workspace Backends

Future workspace backends can implement:

```go
type WorkspaceSnapshotter interface {
    Snapshot(ctx context.Context, workspacePath, snapshotID string) (Metadata, error)
    Restore(ctx context.Context, snapshotID, targetPath string) error
    Delete(ctx context.Context, snapshotID string) error
}
```

Likely backends:

- `preserve-dir`
- `tar-zstd`
- `btrfs`
- ZFS
- XFS reflinks
- CSI volume snapshots

## Relationship To Snapshots

[SNAPSHOTS.md](SNAPSHOTS.md) is the broader design reference for gVisor checkpoint/restore and filesystem-only snapshots. Paused sessions are the first product workflow built on top of that mechanism.
