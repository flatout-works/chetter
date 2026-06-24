# Chetter Plan

Status: **Working plan - created from docs audit and changelog review**

Last reviewed: 2026-06-21

## Inputs

This plan is based on:

- `CHANGELOG.md` through 2026-06-21.
- Current docs under `docs/`.
- OpenHands findings in `docs/research/OPENHANDS.md`.
- Spot checks against current source for recently implemented sessions and MCP tools.

## Summary

Chetter has moved from a scheduled task runner into a self-hosted agent automation control plane. The most important next step is to make Chetter good at multi-step agent workflows, not just one-shot tasks.

The best near-term feature focus is **resumable agent sessions for PR feedback loops**. The foundations already exist: GitHub artifact tools, artifact tracking, task attribution, session/run schema, pinned-runner claims, and resume MCP tools. Finishing this closes the loop where an authoring agent creates a PR, receives review feedback, resumes the same workspace, and updates the branch.

The second major focus is **configuration as code**. The model catalog has already moved to Git-backed definitions, but triggers, agents, skills, task templates, and definition proposal workflows should follow.

The docs need consolidation before or alongside this work. Several files describe shipped features as proposals, and some reference old env vars or tool counts.

## Documentation Consolidation

### Current State

| File | Current role | Recommended action |
|---|---|---|
| `README.md` | Documentation index | Added in Milestone 1. Keep as the entry point for docs navigation. |
| `MANUAL.md` | Operator setup and env reference | Updated in Milestone 1. Keep as canonical setup and operations guide. |
| `FEATURES.md` | Current capability reference | Updated in Milestone 1 from current MCP registration and runner behavior. |
| `SCHEDULES.md` | Cron trigger guide | Keep as a focused how-to, or merge into a broader `AUTOMATION.md`. Update to explain Git-synced trigger definitions if that is now the intended source of truth. |
| `REVIEWS.md` | Current PR review architecture | Keep as canonical PR review docs. |
| `REVIEWER.md` | Old PR reviewer implementation plan | Marked archived in Milestone 1. Use `REVIEWS.md` for current behavior. |
| `TRIGGERS_PROPOSAL.md` | Remaining trigger future work | Removed; ideas migrated to `docs/PLAN.md` line items. |
| `CONFIG_IN_GIT.md` | Configuration-as-code design | Keep, but update shipped status. Model catalog sync is implemented; broader definitions are still planned. |
| `MODEL_CATALOG.md` | Model catalog reference | Keep, or merge with `CONFIG_IN_GIT.md` into `CONFIGURATION.md`. |
| `HARNESSES.md` | Runner harness reference | Keep. Use it as canonical harness architecture docs. |
| `PAUSED_SESSIONS.md` | Resumable session reference | Updated in Milestone 1 from plan to current behavior plus remaining work. |
| `SNAPSHOTS.md` | Snapshot/checkpoint design | Marked as design reference in Milestone 1. Keep linked from `PAUSED_SESSIONS.md`. |
| `GVISOR.md` | gVisor research | Moved to `docs/research/GVISOR.md` as research doc. |
| `DAYTONA.md` | Optional remote backend proposal | Moved to `docs/research/DAYTONA.md` as proposal reference. |
| `OPENHANDS.md` | External architecture inspiration | Moved to `docs/research/OPENHANDS.md` as reference. |
| `testing/k3d-gvisor.md` | Local Kubernetes/gVisor test guide | Keep under `docs/testing/`. Update env vars if needed. |

### Proposed Structure

```text
docs/
  README.md                 # documentation index (current state)
  MANUAL.md                 # operator guide
  FEATURES.md               # current shipped capability reference
  PLAN.md                   # this roadmap
  AUTOMATION.md             # triggers, schedules, reviews, event callbacks (future)
  CONFIGURATION.md          # env vars, definitions repo, model catalog (future)
  HARNESSES.md              # agent harnesses
  SESSIONS.md               # agent sessions, pause/resume, snapshots (future)
  EXECUTION.md              # Docker, gVisor, runner execution backends (future)
  testing/
    k3d-gvisor.md
  research/
    OPENHANDS.md            # moved from root
    DAYTONA.md              # moved from root
    GVISOR.md               # moved from root
```

**Status:** Research docs moved as of 2026-06-22. Remaining restructuring (AUTOMATION.md, CONFIGURATION.md, SESSIONS.md, EXECUTION.md) deferred until those topics need dedicated docs.

This does not need to happen in one large PR. The first useful change is to make `MANUAL.md`, `FEATURES.md`, `REVIEWS.md`, `HARNESSES.md`, and `PAUSED_SESSIONS.md` truthful against the current implementation.

### Milestone 1 Documentation Fixes

Status: **Completed 2026-06-21**

Milestone 1 added `docs/README.md`, refreshed the canonical current-state docs, marked proposal/reference docs explicitly, and updated the resumable session docs from plan to current implementation notes.

Items addressed:

- `MANUAL.md` now distinguishes server `MCP_AUTH_TOKEN`, deployment-facing `CHETTER_MCP_AUTH_TOKEN`, and runner `CHETTER_RUNNER_RPC_TOKEN`.
- `FEATURES.md` now uses the current MCP tool surface, including sessions, GitHub artifact tools, runner drain, audit events, task artifacts, model catalog, and definition sync.
- `FEATURES.md` now documents only the runner MCP bridge tools that are exposed today.
- `PAUSED_SESSIONS.md` now describes current session/run/checkpoint behavior and remaining work.
- `DAYTONA.md` now treats Daytona as a future optional backend and no longer frames Kata as current.
- `REVIEWER.md`, `GVISOR.md`, `SNAPSHOTS.md`, and `OPENHANDS.md` are marked as archived, proposal, research, or reference docs. `TRIGGERS_PROPOSAL.md` is removed.
- `GVISOR.md`, `DAYTONA.md`, and `OPENHANDS.md` moved to `docs/research/` for documentation restructure.

## Product Roadmap

### P0: Documentation Truth Pass

Status: **Completed as Milestone 1 on 2026-06-21**

Goal: make docs reliable enough that agents and operators can use them without rediscovering implementation details.

Deliverables:

- Update `MANUAL.md` env reference and tool list.
- Update `FEATURES.md` from current MCP registration and runner features.
- Convert `PAUSED_SESSIONS.md` from future plan to current status plus remaining work.
- Mark `REVIEWER.md`, `DAYTONA.md`, `GVISOR.md`, and `OPENHANDS.md` as reference or research documents, then move research docs to `docs/research/`. `TRIGGERS_PROPOSAL.md` is removed.
- Add a short docs index, either in `docs/README.md` or at the top of `MANUAL.md`.
- Move research/reference docs (`GVISOR.md`, `DAYTONA.md`, `OPENHANDS.md`) into `docs/research/`.

### P1: Resumable Agent Sessions And PR Feedback Loops

Why next:

Chetter's biggest product gap is that agents are still mostly one-shot. OpenHands' strongest relevant pattern is conversation lifecycle management. Chetter now has the data model and tools to build this without changing the whole control plane.

Current foundation:

- `agent_sessions`, `session_runs`, and checkpoint metadata tables exist.
- `session_mode`, `pause_reason`, and `ttl_hours` are accepted on task and trigger submission.
- `required_runner_id` supports same-runner resume affinity.
- `chetter_list_agent_sessions`, `chetter_agent_session_status`, and `chetter_resume_agent_session` are registered.
- GitHub artifact tools and `chetter_task_artifacts` provide server-side ownership records.

Next deliverables:

- Document the current manual resumable-session workflow.
- Verify gVisor checkpoint creation and restore end-to-end in Docker mode.
- Add checkpoint garbage collection for expired sessions.
- Extend GitHub artifact footers and artifact tracking to include `Session:` and `Run:` where possible.
- Add webhook-driven resume when PR review feedback arrives on a Chetter-owned PR.
- Add web UI pages for sessions, runs, checkpoints, and resume actions.
- Add operational safeguards for pinned-runner offline cases, expired sessions, and failed restores.

Definition of done:

A scheduled authoring agent can create a PR, pause with a preserved workspace, receive review feedback, resume the same session on the pinned runner, update the PR branch, and expose the full session/run history in MCP and the web UI.

### P2: Configuration As Code For All Automation

Why next:

The model catalog Git sync is a good start, but Chetter's real automation assets are agents, skills, triggers, task templates, and model profiles. They need review, diffs, rollback, and attribution.

Current foundation:

- `DEFINITIONS_REPO` and periodic sync exist for model catalogs.
- `chetter_sync_definitions` and `chetter_get_model_catalog` exist.
- Task attribution has started with trigger metadata and artifact tracking.

Next deliverables:

- Add definition sources for agents, skills, triggers, and task templates.
- Store active parsed definitions in TiDB with source repo, path, commit, and content hash.
- Record immutable definition hashes on every task/session run.
- Sync trigger definitions from Git, with DB changes treated as operational overrides.
- Add read tools for definitions and definition sources.
- Add proposal tooling so agents can open PRs against the definitions repo instead of mutating production config directly.

Definition of done:

An operator can point Chetter at a definitions repo, review automation changes through PRs, sync them into TiDB, and trace every task back to the exact definitions that produced it.

### P3: Setup Pipeline And Multi-Source Skills

Why next:

OpenHands has a useful setup pipeline: clone repo, run project setup, load skills, create agent, start conversation. Chetter currently submits and claims tasks directly. A setup pipeline would make tasks more repeatable and project-aware.

Next deliverables:

- Define setup phases and persist phase status per task or session run.
- Support a project-level Chetter setup file, likely under `.chetter/` or `.opencode/`.
- Run optional setup commands before starting the harness, with timeouts and logs.
- Load skills from global, team, repository, and runner image sources.
- Deduplicate skills by name and record skill hashes in task attribution.
- Surface setup failures as classified task errors.

Definition of done:

Tasks show structured setup progress before agent execution, and a repo can ship its own reviewed skills and setup instructions without baking them into runner images.

### P4: Event Callbacks And More Trigger Types

Why next:

The current trigger system handles cron, PR review, and issue/comment-style workflows. OpenHands' event callback pattern suggests a more general automation layer for task lifecycle events.

Next deliverables:

- Add event callbacks for task started, task completed, task failed, artifact created, session paused, session resumed, and runner stale.
- Store callback definitions in the same definitions system as triggers.
- Add trigger types for release events, Sentry alerts, Linear tickets, and multi-repo PR review triggers.
- Add retry and dead-letter behavior for callback failures.
- Add audit events for callback dispatch and outcome.

Definition of done:

Users can wire Chetter automations to lifecycle events without adding hardcoded server paths for each new workflow.

### P5: Execution Backend Interface

Why next:

Both OpenHands and `docs/research/DAYTONA.md` point to the same architectural need: runner execution should be abstracted behind a backend interface. This should start as a refactor around current Docker/gVisor/local behavior, not as a Daytona-first feature.

Next deliverables:

- Extract an `ExecutionBackend` around sandbox/container create, start, stop, checkpoint, restore, and metadata operations.
- Keep Docker plus gVisor as the first implementation.
- Keep local execution as a development implementation.
- Add enough interface shape to support future Kubernetes or remote sandbox implementations.
- Revisit Daytona only after the interface exists and after cost/latency testing.

Definition of done:

Runner task execution can switch backends without changing server task claiming, heartbeats, auth, triggers, or MCP tool contracts.

### P6: Observability, Safety, And Failure Classification

Why next:

As agents become long-lived and autonomous, Chetter needs better operational answers: why did this fail, what did the sandbox do, and was a secret or policy boundary crossed?

Next deliverables:

- Add task failure categories such as `model_error`, `runtime_error`, `timeout`, `cancelled`, `budget_exceeded`, `restore_failed`, and `policy_blocked`.
- Add secrets validation with blocked names, blocked prefixes, max counts, max name length, and max value length.
- Add gVisor metrics collection for filesystem and network behavior.
- Add optional runtime monitoring for suspicious sandbox activity.
- Add runner and session dashboards in the web UI.

Definition of done:

Operators can distinguish provider failures from sandbox failures, timeout failures, policy failures, and restore failures without reading raw logs.

### P7: Git Provider Abstraction

Why later:

OpenHands' `GitService` abstraction is attractive, but GitHub is currently central to Chetter's artifact tools, webhooks, and signatures. Multi-provider Git support should wait until GitHub flows are more complete.

Next deliverables when demand exists:

- Define a `GitService` interface for issues, comments, PRs/MRs, reviews, labels, author permissions, and webhook verification.
- Keep GitHub as the first implementation.
- Add GitLab next, because merge requests map most closely to PRs.
- Rework artifact records to store provider, owner, repo, artifact type, and provider-specific IDs.

Definition of done:

PR review, artifact creation, and artifact tracking can run against GitHub and at least one non-GitHub provider behind the same service interface.

## Not Now

- Do not build Daytona integration before extracting the execution backend interface.
- Do not prioritize GPU sandboxes until there is a real GPU workload.
- Do not build multi-provider Git support before the GitHub session feedback loop is complete.
- Do not make TiDB the authoritative source for durable automation definitions; use Git as source of truth and TiDB as parsed runtime state.
- Do not add fake resume for non-gVisor sessions. Non-gVisor can support one-shot sessions and filesystem snapshots, but true process resume should remain gVisor-only.

## Suggested Milestones

### Milestone 1: Docs Truth And Index

Status: **Completed 2026-06-21**

Target: 1 small PR.

- Fix stale env vars and MCP tool references.
- Add docs index.
- Mark proposal/research/archive docs clearly.
- Update `PAUSED_SESSIONS.md` to reflect current implementation state.

### Milestone 2: Manual Resumable Sessions V1

Target: one end-to-end resumable session flow.

- Submit resumable task.
- Pause after successful run with checkpoint metadata.
- List session and run history.
- Resume manually with follow-up prompt on the pinned runner.
- Expire and clean up paused sessions.

### Milestone 3: PR Feedback Resume

Target: close the agent feedback loop.

- Add session/run IDs to Chetter-authored artifact metadata.
- Map PR review feedback to owning session.
- Submit pinned follow-up runs automatically.
- Show session lifecycle in web UI.

### Milestone 4: Definitions Beyond Model Catalog

Target: configuration as code for automation.

- Sync agents, skills, triggers, and task templates from Git.
- Add definition read tools.
- Store definition hashes on tasks and session runs.
- Add agent-authored definition change PR workflow.

### Milestone 5: Execution And Event Architecture

Target: prepare for scale and new backends.

- Add lifecycle event callbacks.
- Extract execution backend interface.
- Add failure classification and gVisor metrics.
- Reassess Daytona, Kubernetes-native execution, and remote sandbox options.

## Open Questions

- Should `SCHEDULES.md` and `REVIEWS.md` stay separate, or should they become sections of `AUTOMATION.md`?
- Should definitions repo sync replace DB trigger edits entirely, or should DB edits remain as explicit operational overrides?
- Should session artifacts require updated footers for ownership, or can ownership be inferred from server-side artifact creation records only?
- How strict should skill and agent frontmatter validation be during definitions sync?
- Should Pi and Claude Code get stronger isolation by running inside per-task Docker containers, or should OpenCode remain the only gVisor-isolated harness for now?
