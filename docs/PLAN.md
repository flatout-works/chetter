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

| File | Current role | Action taken |
|---|---|---|
| `README.md` | Documentation index | Kept as entry point for docs navigation. |
| `MANUAL.md` | Canonical operator guide | Slimmed again (2026-08): deployment and gVisor content moved to `DEPLOYMENT.md`, agent image content to `IMAGES.md`, definitions YAML reference to `CONFIGURATION.md`. |
| `FEATURES.md` | Capability inventory | Kept; trimmed tables replaced with links to MANUAL.md. |
| `TRIGGERS.md` | Trigger reference | **New**: merged `SCHEDULES.md` + `REVIEWS.md` into one cron + PR review guide. |
| `SESSIONS.md` | Resumable session reference | Renamed from `PAUSED_SESSIONS.md`. |
| `CONFIGURATION.md` | Configuration-as-code + model catalog | Absorbed the definitions-repo YAML reference, managed Git identities, and MCP endpoints sections from MANUAL.md. |
| `HARNESSES.md` | Runner harness reference | Kept as canonical harness architecture docs. |
| `DEPLOYMENT.md` | Deployment and sandboxing | **New**: Docker + gVisor setup, Kubernetes gVisor DaemonSet/RuntimeClass, graceful shutdown, network isolation (from MANUAL.md). |
| `IMAGES.md` | Agent images | **New**: image sources, resolution, variants, custom images, container contract (from MANUAL.md). |
| `K3S.md` | Canonical local k3s guide | Kept. |
| `EKS.md` | EKS production guide | Kept. |
| `EXECUTION.md` | Execution backend contract | Kept as canonical backend reference. |
| `PLAN.md` | This roadmap | Updated. |

### Completed Restructure

```text
docs/
  README.md                 # documentation index
  MANUAL.md                 # canonical operator guide (backbone)
  FEATURES.md               # slim capability inventory
  PLAN.md                   # roadmap
  HARNESSES.md              # harness architecture
  TRIGGERS.md               # cron schedules + PR review automation (merged)
  SESSIONS.md               # resumable sessions (was PAUSED_SESSIONS.md)
  CONFIGURATION.md          # definitions repo + model catalog
  PROVIDERS.md              # provider support matrix
  LITELLM.md                # LiteLLM gateway integration
  EXECUTION.md              # execution backends
  DEPLOYMENT.md             # deployment + sandboxing guides
  IMAGES.md                 # agent images
  K3S.md                    # k3s + gVisor setup
  EKS.md                    # EKS production guide
  PRIVATEFORK.md            # private fork workflow
  TIDB-WOWBAGGER.md         # TiDB ops runbook
  presentation/
  testing/
    k3d-gvisor.md
  plans/
  research/
    OPENHANDS.md
    DAYTONA.md
    GVISOR.md
    SNAPSHOTS.md
    REVIEWER.md
    UNIVERSAL_HARNESS.md
    TASK_SESSION_MODEL_REFACTOR.md      # moved from docs/ (completed design doc)
    REPOSITORY_QUALITY_REVIEW.md        # moved from docs/ (dated review)
```

**Status:** Completed 2026-07-02, re-run 2026-08-02. `MANUAL.md` is the backbone document with links to specialized docs; `FEATURES.md` is a slim capability scan; `SCHEDULES.md` and `REVIEWS.md` merged into `TRIGGERS.md`; `PAUSED_SESSIONS.md` renamed to `SESSIONS.md`; deployment, images, and definitions-reference content moved out of `MANUAL.md` into `DEPLOYMENT.md`, `IMAGES.md`, and `CONFIGURATION.md`; `TASK_SESSION_MODEL_REFACTOR.md` and `REPOSITORY_QUALITY_REVIEW.md` moved to `research/`.

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

Status: **Completed 2026-07-23** — the Task/AgentSession/UserPrompt/ExecutionAttempt model shipped
(see `docs/research/TASK_SESSION_MODEL_REFACTOR.md`), manual resume, recovery with custom
prompts, and webhook-driven review feedback are live. gVisor checkpoint/restore remains
runtime-dependent; see `docs/SESSIONS.md`.

Why next:

Chetter's biggest product gap is that agents are still mostly one-shot. OpenHands' strongest relevant pattern is conversation lifecycle management. Chetter now has the data model and tools to build this without changing the whole control plane.

Current foundation:

- `agent_sessions`, `user_prompts`, `execution_attempts`, and checkpoint metadata tables exist.
- `session_mode`, `pause_reason`, and `ttl_hours` are accepted on task and trigger submission.
- ExecutionAttempt `required_runner_id` supports same-runner resume affinity.
- `chetter_list_agent_sessions`, `chetter_agent_session_status`, and `chetter_resume_agent_session` are registered.
- GitHub artifact tools and `task_artifacts` provide server-side ownership records.

Next deliverables:

- Document the current manual resumable-session workflow.
- Verify gVisor checkpoint creation and restore end-to-end in Docker mode.
- ~~Add checkpoint garbage collection for expired sessions.~~ **Done 2026-08-07** — the 30s reaper expires paused/recoverable sessions past their `ttl_hours` pause TTL (fenced against in-flight prompts/attempts) and clears terminal-session checkpoint paths and exports after `SESSION_ARTIFACT_TTL`; actions are recorded as `session.expired` / `session.artifact_gc` audit events and surfaced in the `chetter_sessions` fleet metric. See `docs/SESSIONS.md`.
- Keep four-ID artifact attribution (`Task:`, `Session:`, `Prompt:`, and `Execution:`) consistent across direct tools and webhooks.
- Add webhook-driven resume when PR review feedback arrives on a Chetter-owned PR.
- Add web UI pages for sessions, runs, checkpoints, and resume actions.
- Add operational safeguards for pinned-runner offline cases, expired sessions, and failed restores.

Definition of done:

A scheduled authoring agent can create a PR, pause with a preserved workspace, receive review feedback, resume the same session on the pinned runner, update the PR branch, and expose the full session/run history in MCP and the web UI.

### P2: Configuration As Code For All Automation

Status: **Completed 2026-07-30** — agents, skills, triggers, task templates, MCP endpoints,
and the model catalog all sync from the definitions repo with read/proposal tooling; team
scoping of definitions is enforced. See `docs/CONFIGURATION.md`.

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

#### Runtime Definition Injection

The target model is to keep images stable and inject changing behavior from the Git-backed definitions repo at task time:

1. `chetter_sync_definitions` syncs root `model-catalog.yaml` plus scoped triggers, agents, skills, MCP endpoints, and task templates from `global/`, `groups/<team-name>/`, and `repos/<owner>/<repo>/` into the database.
2. When a runner claims a task, it asks the server for the resolved definitions for that task, considering global/team/repo scope.
3. Before starting the harness, the runner writes those definitions into the task workspace, for example `.opencode/agent/*.md` and `.opencode/skill/*/SKILL.md`.
4. The harness starts with workspace config paths, so injected definitions take precedence over image-baked fallback definitions.
5. Updating agents, skills, prompts, task templates, model catalog entries, or Git-managed triggers becomes a config repo PR plus sync, not a dev image rebuild.

Trigger ownership should remain explicit:

| Trigger source | Behavior |
|---|---|
| Git-managed triggers | Created or updated from scoped `triggers/*.yaml` paths in the definitions repo. Manual DB edits are overwritten on the next sync. If removed from Git, they should be disabled rather than deleted. |
| Dynamic MCP-created triggers | Created through `chetter_create_trigger` or the web/API. They are not modified by Git sync unless explicitly adopted. |
| Conflicts | If Git sync would create a trigger with the same name as a dynamic trigger, sync should fail with a clear conflict rather than silently taking ownership. |

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

Status: **Partially completed** — event callbacks (list/create/edit/delete tools, web UI page,
task-event dispatch with exact/wildcard event-type matching, `create_task`/`webhook`/`slack`
actions, template rendering, recursion guard) shipped. Callback action failures are
logged only — no outbound delivery queue with retry/backoff or dead-lettering yet.
The `webhook_deliveries` table covers **inbound** GitHub webhook processing (retry,
idempotency, dead-letter status), not callback deliveries. A unified inbound/outbound
webhook platform is planned in `docs/plans/2026-07-28-001-feat-webhook-platform-plan.md`.

Why next:

The current trigger system handles cron, PR review, and issue/comment-style workflows. OpenHands' event callback pattern suggests a more general automation layer for task lifecycle events.

Next deliverables:

- Add callback event coverage for artifact created, session paused/resumed, and runner stale (task lifecycle events are dispatched today).
- Add retry and dead-letter behavior for callback action failures.
- Add trigger types for release events, Sentry alerts, Linear tickets, and multi-repo PR review triggers.

Definition of done:

Users can wire Chetter automations to lifecycle events without adding hardcoded server paths for each new workflow.

### P5: Separate Docker Mode From Kubernetes Mode

Status: **Implementation recovered; k3s ServeHarness and resume validated.** The current tree
contains strict backend selection, client-go Pod execution, execution-only workspace
subPath mounts over an operator-provided PVC or single-node hostPath, per-execution
Secrets, ServeHarness resume, and Pi attach support. A clean single-node k3s run has
validated normal OpenCode execution and resumable-session follow-up under gVisor,
including Pod/Secret cleanup. Do not mark this complete until live tests also cover Pi
attach, cancellation, a rolling runner update, and a production multi-node cluster.

Why next:

The runner currently creates agent containers by shelling out to `docker run` via the
host's Docker socket. This works for Docker Compose and single-host deployments, but is
not Kubernetes-native:

- Agent containers are invisible to Kubernetes (no scheduling, no resource accounting, no eviction).
- Mounting `/var/run/docker.sock` into the runner pod grants full host-level container control.
- gVisor isolation depends on the host Docker daemon's runtime configuration, not Kubernetes.
- Workspace persistence uses host filesystem paths, not Kubernetes volumes.

The goal is to separate Docker mode (current behavior, good for Docker Compose) from
Kubernetes mode (runner creates agent Pods via the Kubernetes API, with
`runtimeClassName: gvisor`, no Docker socket).

See `docs/K3S.md` for local k3s + gVisor validation, and `docs/EKS.md` for production EKS installation.

Related docs:
- `docs/K3S.md` — canonical local k3s guide
- `docs/EKS.md` — production EKS installation guide

#### Current Architecture

```text
Docker mode (unchanged):
  Runner pod
    └─ docker run agent container via host Docker socket

Kubernetes mode (new):
  Runner pod (no Docker socket)
    └─ Kubernetes API creates agent Pod
         spec:
            runtimeClassName: gvisor
            containers:
              - agent (opencode serve --port 9999)
            volumes:
              - operator-provided shared workspace, execution-only subPath mount
```

The original multi-phase proposal (emptyDir workspaces, ConfigMap archives,
per-session PVC creation, workspace sidecar) was superseded during
implementation and is not the current contract — see
[EXECUTION.md](EXECUTION.md) for the current Kubernetes executor contract.

#### Environment Variables

Docker mode (unchanged):

```
EXECUTION_BACKEND=docker    # or omit (default)
USE_GVISOR=true|false
```

Kubernetes mode (new):

```
EXECUTION_BACKEND=kubernetes
KUBERNETES_NAMESPACE=chetter
KUBERNETES_RUNTIME_CLASS=gvisor
KUBERNETES_CLEANUP_AFTER_TASK=true
KUBERNETES_AGENT_IMAGE_PULL_POLICY=IfNotPresent
KUBERNETES_AGENT_SERVICE_ACCOUNT=chetter-agent
```

#### Risks

- `k8s.io/client-go` is a large dependency. Consider `kubectl` subprocess for MVP.
- ConfigMap size limits (1 MB). Use init containers for large files.
- Pod IP reachability: verify runner-to-agent-pod traffic in multi-node clusters.
- gVisor availability: requires `runsc` on nodes. Not all managed Kubernetes supports it.
- Workspace resume semantics change from Docker/container preservation to PVC + harness-level resume.

Definition of done:

Runner task execution can switch between Docker and Kubernetes backends without
changing server task claiming, heartbeats, auth, triggers, or MCP tool contracts.
Kubernetes-mode runner has no Docker socket mount. Agent pods use
`runtimeClassName: gvisor` validated on k3s. Production EKS deployment is documented.

### P6: Observability, Safety, And Failure Classification

Status: **Partially completed** — structured failure classification, server-side env var
validation, secret redaction, Prometheus metrics, `/readyz` probes, gVisor sandbox metrics
collection, and runtime sandbox monitoring (see `docs/DEPLOYMENT.md` — issue #302) shipped.
Remaining: richer dashboards.

Why next:

As agents become long-lived and autonomous, Chetter needs better operational answers: why did this fail, what did the sandbox do, and was a secret or policy boundary crossed?

Next deliverables:

- Add task failure categories such as `model_error`, `runtime_error`, `timeout`, `cancelled`, `budget_exceeded`, `restore_failed`, and `policy_blocked`.
- Add secrets validation with blocked names, blocked prefixes, max counts, max name length, and max value length.
- ~~Add gVisor metrics collection for filesystem and network behavior~~ — shipped: per-sandbox start/lifetime latency, peak RSS/CPU, start failures and crashes are collected on the runner and surfaced via heartbeats, `/metrics`, and `task_events` (`sandbox_start_failed`, `sandbox_crashed`). See issue #302.
- ~~Add optional runtime monitoring for suspicious sandbox activity~~ — shipped: `sandbox_available` heartbeat probe and per-sandbox teardown monitoring cover runtime sandbox health. See issue #302.
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

- Do not build Daytona integration before extracting the execution backend interface (P5).
- Do not mount `/var/run/docker.sock` into the runner pod when `EXECUTION_BACKEND=kubernetes`. The Kubernetes executor must use the Kubernetes API to create agent pods.
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

Status: **Completed 2026-07-23**

Target: one end-to-end resumable session flow.

- Submit resumable task.
- Pause after successful run with checkpoint metadata.
- List session and run history.
- Resume manually with follow-up prompt on the pinned runner.
- Expire and clean up paused sessions.

### Milestone 3: PR Feedback Resume

Status: **Completed 2026-07-23** — follow-up resume via webhook feedback shipped with the
session model refactor.

Target: close the agent feedback loop.

- Add session/run IDs to Chetter-authored artifact metadata.
- Map PR review feedback to owning session.
- Submit pinned follow-up runs automatically.
- Show session lifecycle in web UI.

### Milestone 4: Definitions Beyond Model Catalog

Status: **Completed 2026-07-30**

Target: configuration as code for automation.

- Sync agents, skills, triggers, and task templates from Git.
- Add definition read tools.
- Store definition hashes on tasks and agent sessions.
- Add agent-authored definition change PR workflow.

### Milestone 5: Execution Backend Separation

Status: **Completed 2026-07-28** — Kubernetes pod execution shipped and validated on
single-node k3s; remaining validation: Pi attach, cancellation, rolling runner update,
multi-node production.

Target: Docker mode and Kubernetes mode coexist cleanly.

- Extract execution backend interface (Phase 1-2).
- Implement Kubernetes executor with `emptyDir` workspace (Phase 3).
- Add RBAC and Kubernetes-mode manifests (Phase 4).
- Validate on k3s with gVisor (Phase 5).
- Add PVC-backed resumable sessions (Phase 6-7).
- Add end-to-end tests (Phase 8).

See P5 above for the detailed phase breakdown.

## Open Questions

- Should definitions repo sync replace DB trigger edits entirely, or should DB edits remain as explicit operational overrides?
- How long should attempt-level artifact contribution history be retained?
- How strict should skill and agent frontmatter validation be during definitions sync?
- Should Pi and Claude Code get stronger isolation by running inside per-task Docker containers, or should OpenCode remain the only gVisor-isolated harness for now?
