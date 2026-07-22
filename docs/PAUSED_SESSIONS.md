# Paused Sessions

Status: **Partially implemented - current behavior and remaining work**

Chetter now has first-class agent session records. A task is no longer only a one-shot row in `chetter_tasks`; every submitted task also creates an `agent_session` and a first `user_prompt`.

Resumable sessions use gVisor/Docker checkpoint metadata and runner affinity. Non-resumable sessions are still useful for lineage and UI/history, but they do not support true process restore.

## Concepts

An `agent_session` is the long-lived identity and workspace lineage for an agent.

A `user_prompt` is one user-authored turn inside that session. Runner executions are tracked separately as execution attempts.

```text
agent_session sess_1
  user_prompt prompt_1 -> creates PR
agent_session -> paused

review feedback arrives

agent_session sess_1
  user_prompt prompt_2 -> addresses feedback
```

For a default one-shot task:

```text
agent_session sess_1
  user_prompt prompt_1 -> completed
agent_session -> completed
```

## Implemented Today

### Database Model

Migration `011_add_agent_sessions.sql` adds:

- `chetter_agent_sessions`
- `chetter_user_prompts`
- `chetter_agent_session_checkpoints`
- `chetter_tasks.required_runner_id`
- `chetter_tasks.checkpoint_after_success`
- `chetter_task_artifacts.agent_session_id`
- `chetter_task_artifacts.user_prompt_id`

### Task Submission

Every submitted task creates:

- A `task_*` row in `chetter_tasks`.
- A `sess_*` row in `chetter_agent_sessions`.
- A `prompt_*` row in `chetter_user_prompts`.

When `session_mode: resumable` is set:

- `resume_mode` becomes `harness_session`.
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

The runner reports checkpoint and workspace paths back to the server. The server stores a `ready` checkpoint row and moves the session to `paused`.

### Manual Resume

MCP tools:

- `chetter_list_agent_sessions`
- `chetter_agent_session_status`
- `chetter_resume_agent_session`

`chetter_resume_agent_session` checks that:

- The session is `paused` or `recoverable`.
- The session uses `resume_mode: harness_session` or `gvisor_checkpoint`.
- A pinned runner exists.
- `harness_session` has preserved workspace plus harness session metadata, or `gvisor_checkpoint` has a ready checkpoint.
- The pinned runner is alive.

If a resumable harness session times out or encounters an opencode transport failure (EOF, connection reset, broken pipe) after the runner has captured workspace and harness session metadata, the server marks it `recoverable` instead of `error`, so it can be resumed manually from the UI or API.

It appends a `user_prompt`, keeps the same stable task ID, and requeues that task with `required_runner_id` set to the pinned runner.

### PR Feedback Resume

Webhook handling can look up a paused Chetter-authored PR session through `chetter_task_artifacts` and enqueue a follow-up user prompt with review feedback.

This path relies on server-side artifact records. Chetter-authored artifacts include `Task:`, `Session:`, and `Prompt:` attribution.

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
  "status": "paused",
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
- A `gvisor_checkpoint` session can resume only from a ready checkpoint.
- A `harness_session` session can resume from preserved workspace and harness session metadata.
- Resume prompts inherit the original session's repo, ref, image, agent, provider, model, and variant.
- Resume prompts are pinned with `required_runner_id`.

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
- Show checkpoint status, pinned runner, expiry, and latest prompt in the UI.
- Link tasks, user prompts, artifacts, and GitHub PRs from each other.

### Footers And Artifact Ownership

- Keep `Task:`, `Session:`, and `Prompt:` attribution aligned with stored artifacts.
- Prefer server-side artifact records when available.

Suggested footer:

```text
---
Generated by [Chetter](https://github.com/flatout-works/chetter)
Session: sess_abc123
Prompt: prompt_def456
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

[research/SNAPSHOTS.md](research/SNAPSHOTS.md) is the broader design reference for gVisor checkpoint/restore and filesystem-only snapshots. Paused sessions are the first product workflow built on top of that mechanism.
