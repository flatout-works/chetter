# Paused Sessions

Status: **Implemented**

Chetter models resumable work as a stable Task with one or more AgentSessions.
Each user turn is a UserPrompt, and each lease-bound runner invocation is an
ExecutionAttempt:

```text
Task
  AgentSession
    UserPrompt 1
      ExecutionAttempt 1
    UserPrompt 2 (resume)
      ExecutionAttempt 1
```

## Ownership

- Task owns the durable objective, authorization, origin, aggregate status, and
  artifacts.
- AgentSession owns the agent/model/image/configuration snapshot, resume mode,
  harness session identity, retained workspace/checkpoint, expiry, and lifecycle.
- UserPrompt owns one instruction and its aggregate result within a session.
- ExecutionAttempt owns runner affinity, claim and lease state, timeout,
  workspace, runtime metadata, result, diagnostics, and token usage.

Task rows do not own runner affinity, checkpoint policy, leases, or workspaces.

## Submission And Resume

Every submission creates one Task, AgentSession, UserPrompt, and queued
ExecutionAttempt. With `session_mode: resumable`, the AgentSession uses
`resume_mode: harness_session`, stores the optional pause reason, and expires
after `ttl_hours` (72 hours by default).

`chetter_resume_agent_session` requires a `paused` or `recoverable` session,
verifiable retained state, a live pinned runner, and a supported resume mode. It
appends a UserPrompt to the same AgentSession and queues a new ExecutionAttempt.
Runner affinity is stored on that attempt as `required_runner_id`.

If continuity cannot be proven, Chetter does not reuse the session or workspace.
Cold reclaim and explicit recovery create a fresh AgentSession and UserPrompt
under the same stable Task, optionally carrying the previous export as recovery
context.

## Resume Modes

- `harness_session` resumes from a preserved workspace and harness session ID.
- `gvisor_checkpoint` resumes from a ready process checkpoint and preserved
  workspace.
- `none` is non-resumable and always uses a fresh attempt workspace.

Resume is same-runner only. A pinned runner that is unavailable prevents manual
resume and causes webhook-driven resume to be skipped.

## Workspace And Fencing

Attempt workspaces are keyed by immutable ExecutionAttempt ID. Retained session
state is keyed by AgentSession ID. Heartbeats, events, cancellation, terminal
reports, and cleanup must present the matching Task, AgentSession, UserPrompt,
and ExecutionAttempt IDs, so a stale lease cannot mutate or delete newer work.

Pruning protects active attempts and retained session/checkpoint paths. Ordinary
non-resumable attempts never reuse a Task-level workspace.

## Artifact Attribution

Chetter-created artifacts and canonical footers carry `Task:`, `Session:`,
`Prompt:`, and `Execution:` attribution. The server derives session and prompt
ownership from the authoritative ExecutionAttempt and rejects Task mismatches.
Artifact deduplication includes ExecutionAttempt ID so repeated contributions
remain visible in history.

## Agent Environment

Each execution receives protected hierarchy variables:

```text
CHETTER_TASK_ID
CHETTER_AGENT_SESSION_ID
CHETTER_USER_PROMPT_ID
CHETTER_EXECUTION_ID
```

Task-provided environment values cannot override them.

## Operations

Use these MCP tools to inspect and resume sessions:

- `chetter_list_agent_sessions`
- `chetter_agent_session_status`
- `chetter_resume_agent_session`

Real Docker/gVisor checkpoint behavior still depends on the runner host and
runtime supporting checkpoint/restore. See [research/SNAPSHOTS.md](research/SNAPSHOTS.md)
for the underlying checkpoint design.
