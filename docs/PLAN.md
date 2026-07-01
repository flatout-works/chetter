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
| `MANUAL.md` | Canonical operator guide | Trimmed: K8s deployment moved to EKS.md link, harness matrix moved to HARNESSES.md link, planned injection moved to PLAN.md. |
| `FEATURES.md` | Capability inventory | Trimmed: env vars and MCP tool tables replaced with links to MANUAL.md; harness table replaced with link to HARNESSES.md. |
| `SCHEDULES.md` | Cron trigger guide | Kept as focused how-to. |
| `REVIEWS.md` | Current PR review architecture | Kept as canonical PR review docs. |
| `CONFIGURATION.md` | Configuration-as-code + model catalog | **New**: merged from `CONFIG_IN_GIT.md` + `MODEL_CATALOG.md` (both deleted). |
| `HARNESSES.md` | Runner harness reference | Kept as canonical harness architecture docs. |
| `PAUSED_SESSIONS.md` | Resumable session reference | Kept. |
| `K3S.md` | k3s + gVisor setup guide | Fixed stale env var (`RUNNER_EXECUTION_BACKEND` → `EXECUTION_BACKEND`). |
| `EKS.md` | EKS production guide | Kept. |
| `PLAN.md` | This roadmap | Updated. |

### Completed Restructure

```text
docs/
  README.md                 # documentation index
  MANUAL.md                 # canonical operator guide (backbone)
  FEATURES.md               # slim capability inventory
  PLAN.md                   # roadmap
  HARNESSES.md              # harness architecture
  SCHEDULES.md             # cron trigger how-to
  REVIEWS.md               # PR review automation
  PAUSED_SESSIONS.md        # resumable sessions
  CONFIGURATION.md          # definitions repo + model catalog (merged)
  K3S.md                    # k3s + gVisor setup
  EKS.md                    # EKS production guide
  presentation/
  testing/
    k3s-chetter.md
    k3d-gvisor.md
  research/
    OPENHANDS.md
    DAYTONA.md
    GVISOR.md
    SNAPSHOTS.md            # moved from docs/; updated to reflect partial implementation
    REVIEWER.md             # moved from docs/; archived implementation plan
    UNIVERSAL_HARNESS.md    # moved from docs/; implemented design doc
```

**Status:** Completed 2026-07-02. `MANUAL.md` is now the backbone document with links to specialized docs. `FEATURES.md` is a slim capability scan. `CONFIG_IN_GIT.md` and `MODEL_CATALOG.md` merged into `CONFIGURATION.md`. `REVIEWER.md`, `UNIVERSAL_HARNESS.md`, and `SNAPSHOTS.md` moved to `research/`. Overlapping env var tables, MCP tool tables, and harness matrices de-duplicated — `MANUAL.md` is the single source of truth for those.

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

#### Runtime Definition Injection

The target model is to keep images stable and inject changing behavior from the Git-backed definitions repo at task time:

1. `chetter_sync_definitions` syncs `model-catalog.yaml`, `triggers/*.yaml`, `agents/*.md`, `skills/**/SKILL.md`, and `task-templates/*.md` from the config repo into the database.
2. When a runner claims a task, it asks the server for the resolved definitions for that task, considering global/team/repo scope.
3. Before starting the harness, the runner writes those definitions into the task workspace, for example `.opencode/agent/*.md` and `.opencode/skill/*/SKILL.md`.
4. The harness starts with workspace config paths, so injected definitions take precedence over image-baked fallback definitions.
5. Updating agents, skills, prompts, task templates, model catalog entries, or Git-managed triggers becomes a config repo PR plus sync, not a dev image rebuild.

Trigger ownership should remain explicit:

| Trigger source | Behavior |
|---|---|
| Git-managed triggers | Created or updated from `triggers/*.yaml` in the definitions repo. Manual DB edits are overwritten on the next sync. If removed from Git, they should be disabled rather than deleted. |
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

### P5: Separate Docker Mode From Kubernetes Mode

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

See `docs/testing/k3s-chetter.md` for local k3s + gVisor validation, and `docs/EKS.md`
for production EKS installation.

Related docs:
- `docs/K3S.md` — k3s + gVisor setup guide
- `docs/testing/k3s-chetter.md` — full Chetter stack on k3s
- `docs/EKS.md` — production EKS installation guide

#### Architecture Target

```text
Docker mode (unchanged):
  Runner pod
    └─ docker run agent container via host Docker socket

Kubernetes mode (new):
  Runner pod (no Docker socket)
    └─ Kubernetes API creates agent Pod
         spec:
           runtimeClassName: gvisor
           initContainers:
             - clone (git clone into shared volume)
           containers:
             - workspace-mcp (sidecar, serves MCP tools)
             - agent (opencode serve --port 9999)
```

#### Phases

**Phase 1: Execution Backend Abstraction**

Extract current execution logic behind an interface.

- Define `ExecutionBackend` interface in `runner/internal/controller/executor.go`.
- Move `runDockerAgent` / `runDockerAgentResume` into `DockerExecutor`.
- Move `runLocalAgent` into `LocalExecutor`.
- Config: `EXECUTION_BACKEND=docker|local|kubernetes` (default: `docker`).
  Keep backward compat: `RUNNER_LOCAL=true` maps to `local`, `false` maps to `docker`.
- `KubernetesExecutor` stub: returns "not implemented".
- No behavior change for existing paths. `make check` passes.

**Phase 2: Shared Harness Driver**

Extract harness HTTP interaction logic so both backends share it.

- Extract into `runner/internal/controller/harness_driver.go`:
  wait ready, create session, watch events, send prompt, abort session,
  read export, classify errors, publish terminal result.
- Docker and Kubernetes executors differ only in workspace creation, agent
  process start/networking, and cleanup.
- `make check` passes. No behavior change.

**Phase 3: Kubernetes Executor — Non-Resumable MVP**

Create one Pod per task with `emptyDir` workspace, connect by Pod IP.

- Implement `KubernetesExecutor` in `runner/internal/controller/kubernetes_executor.go`.
- Add Kubernetes client-go dependency to `runner/go.mod`.
- Config: `KUBERNETES_NAMESPACE`, `KUBERNETES_RUNTIME_CLASS`,
  `KUBERNETES_CLEANUP_AFTER_TASK`, `KUBERNETES_AGENT_IMAGE_PULL_POLICY`.
- Pod creation: init container clones repo, agent container runs harness,
  optional workspace-mcp sidecar, shared `emptyDir` at `/workspace`.
- Runner flow: create Pod, wait for Running, read Pod IP, connect to
  `http://<podIP>:9999`, drive harness via shared driver, collect export,
  delete Pod.
- Error handling: Pod stuck/Failed → fetch events, logs, container status;
  classify and publish. Transport errors use same diagnostics as Docker mode.
- Works on k3s without gVisor (empty `KUBERNETES_RUNTIME_CLASS`).

**Phase 4: Runner Manifests And RBAC**

Production-ready manifests for Kubernetes backend.

- `deploy/k8s/runner-kubernetes-deployment.yaml` — no Docker socket.
- `deploy/k8s/runner-rbac.yaml` — ServiceAccount, Role (pods, pods/log,
  configmaps, secrets, PVCs), RoleBinding.
- `deploy/k3s/kubernetes-runner.yaml` — k3s local testing variant.
- Existing Docker-mode manifests remain unchanged.

**Phase 5: gVisor Validation On k3s**

Prove Kubernetes executor works with `runtimeClassName: gvisor`.

- Follow `docs/K3S.md` for k3s + gVisor setup.
- Deploy full Chetter stack on k3s per `docs/testing/k3s-chetter.md`.
- Submit trivial task, verify:
  - Runner creates `chetter-task-*` Pod with `runtimeClassName: gvisor`.
  - Agent harness starts and responds.
  - Task reaches `done`.
  - Pod is cleaned up.
- Verify non-gVisor path too (empty `KUBERNETES_RUNTIME_CLASS`).

**Phase 6: Resumable Sessions With PVC**

Support resumable agent sessions using PVC-backed workspaces.

- Resumable task → create PVC instead of `emptyDir`.
- PVC lifecycle: created on first run, kept after Pod deletion on
  timeout/transport failure, reused on resume, deleted on session TTL expiry.
- Schema changes (additive): `workspace_backend`, `workspace_ref` columns
  on `chetter_agent_sessions`. Update `schema.go`, `store.go`, migration, sqlc.
- On recoverable failure: delete Pod, keep PVC, report workspace ref.
- On resume: new Pod mounts same PVC, harness resumes from saved state.

**Phase 7: Reaper And Cleanup**

Extend cleanup for Kubernetes resources.

- All resources labeled `chetter.io/task-id` and `chetter.io/session-id`.
- Normal completion: delete Pod, ConfigMap, Secret.
- Recoverable: delete Pod, keep PVC.
- Expired session: delete PVC and orphaned Pods.
- `chetter_runner_health` exposes Kubernetes executor health and orphan counts.

**Phase 8: End-to-End Tests**

- Unit tests: executor selection, Pod spec generation, PVC naming, error mapping.
- Integration tests with fake Kubernetes client: Pod lifecycle, PVC retention.
- k3s validation scripts: `scripts/k3s/create-cluster.sh`,
  `scripts/k3s/load-images.sh`, `scripts/k3s/smoke-task.sh`,
  `scripts/k3s/smoke-gvisor.sh`.

#### Environment Variables

Docker mode (unchanged):

```
EXECUTION_BACKEND=docker    # or omit (default)
RUNNER_LOCAL=false
USE_GVISOR=true|false
```

Kubernetes mode (new):

```
EXECUTION_BACKEND=kubernetes
KUBERNETES_NAMESPACE=chetter
KUBERNETES_RUNTIME_CLASS=gvisor
KUBERNETES_CLEANUP_AFTER_TASK=true
KUBERNETES_AGENT_IMAGE_PULL_POLICY=IfNotPresent
KUBERNETES_SERVICE_ACCOUNT=chetter-runner
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

### Milestone 5: Execution Backend Separation

Target: Docker mode and Kubernetes mode coexist cleanly.

- Extract execution backend interface (Phase 1-2).
- Implement Kubernetes executor with `emptyDir` workspace (Phase 3).
- Add RBAC and Kubernetes-mode manifests (Phase 4).
- Validate on k3s with gVisor (Phase 5).
- Add PVC-backed resumable sessions (Phase 6-7).
- Add end-to-end tests (Phase 8).

See P5 above for the detailed phase breakdown.

## Open Questions

- Should `SCHEDULES.md` and `REVIEWS.md` stay separate, or should they become sections of `AUTOMATION.md`?
- Should definitions repo sync replace DB trigger edits entirely, or should DB edits remain as explicit operational overrides?
- Should session artifacts require updated footers for ownership, or can ownership be inferred from server-side artifact creation records only?
- How strict should skill and agent frontmatter validation be during definitions sync?
- Should Pi and Claude Code get stronger isolation by running inside per-task Docker containers, or should OpenCode remain the only gVisor-isolated harness for now?
