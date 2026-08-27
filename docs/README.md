# Chetter Documentation

Status: **Index**

Use this page to pick the right document. `MANUAL.md` is the canonical operator guide; `FEATURES.md` is the quick capability scan. Research and reference docs are marked explicitly.

## Positioning

| Document | Purpose |
|---|---|
| [WHY-CHETTER.md](WHY-CHETTER.md) | Why run agents as a shared service instead of one-off GitHub Actions jobs; 20 example automations. |

## Current Operation

| Document | Purpose |
|---|---|
| [MANUAL.md](MANUAL.md) | Canonical operations guide: setup, config, deployment overview, MCP tools, env vars. |
| [FEATURES.md](FEATURES.md) | Quick capability inventory (feature descriptions, no env/tool tables). |
| [HARNESSES.md](HARNESSES.md) | Runner harness architecture and supported agent CLIs. |
| [TRIGGERS.md](TRIGGERS.md) | Cron schedules and GitHub PR review automation (merged from SCHEDULES.md + REVIEWS.md). |
| [SESSIONS.md](SESSIONS.md) | Resumable session model, checkpoint/restore, and remaining work (was PAUSED_SESSIONS.md). |
| [CONFIGURATION.md](CONFIGURATION.md) | Configuration-as-code: definitions repo, model catalog, Git identities, MCP endpoints, sync. |
| [PROVIDERS.md](PROVIDERS.md) | Supported provider matrix per harness and catalog `kind` reference. |
| [LITELLM.md](LITELLM.md) | LiteLLM gateway integration (model routing and MCP gateway). |
| [IMAGES.md](IMAGES.md) | Agent images: sources, resolution, variants, custom images, container contract. |
| [EXECUTION.md](EXECUTION.md) | Execution backends: docker, kubernetes, local. |
| [DEPLOYMENT.md](DEPLOYMENT.md) | Deployment overview, Docker + gVisor, Kubernetes gVisor setup, graceful shutdown. |
| [K3S.md](K3S.md) | Canonical local k3s guide for running the Chetter stack with the Kubernetes backend. |
| [EKS.md](EKS.md) | Production EKS (or similar managed Kubernetes) installation guide. |
| [testing/k3d-gvisor.md](testing/k3d-gvisor.md) | Local Kubernetes and gVisor testing guide (k3d). |
| [PRIVATEFORK.md](PRIVATEFORK.md) | Maintaining a private fork of the repository. |
| [SCHEMA.md](SCHEMA.md) | Database schema reference with mermaid ER diagrams: task model, fleet, triggers, auth, definitions, audit. |
| [TIDB-WOWBAGGER.md](TIDB-WOWBAGGER.md) | TiDB deployment on wowbagger: cluster shape and DSN routing, UTC time-zone requirement, migration history and schema-drift gotchas, TiDB planner bug, runsc isolation. |

## Architecture & Design

| Document | Purpose |
|---|---|
| [WEBUI.md](WEBUI.md) | How the web UI is built: SvelteKit SPA, Tailwind v4 + Flowbite-Svelte, ConnectRPC/protobuf data layer, state stores, auth, and serving via `go:embed`. |
| [BACKEND.md](BACKEND.md) | How the backend is built: the MCP server/control plane and the runner — ConnectRPC surfaces, sqlc dual-dialect data layer, lease-based task claiming, reaper, harnesses, and gVisor isolation. |

## Planning

| Document | Purpose |
|---|---|
| [PLAN.md](PLAN.md) | Product roadmap and milestones. |
| [plans/](plans/) | Dated implementation plans (webhook platform, GitHub multi-installation, quality hardening, ops). |

## Research And Reference

| Document | Purpose |
|---|---|
| [research/OPENHANDS.md](research/OPENHANDS.md) | OpenHands architecture findings relevant to Chetter. |
| [research/GVISOR.md](research/GVISOR.md) | gVisor feature research beyond checkpoint/restore. |
| [research/SNAPSHOTS.md](research/SNAPSHOTS.md) | Snapshot/checkpoint design reference (gVisor checkpoint, Docker commit, K8s alternatives). |
| [research/DAYTONA.md](research/DAYTONA.md) | Optional Daytona backend proposal. |
| [research/REVIEWER.md](research/REVIEWER.md) | Archived PR reviewer implementation plan; use `TRIGGERS.md` for current behavior. |
| [research/UNIVERSAL_HARNESS.md](research/UNIVERSAL_HARNESS.md) | Universal harness architecture design (implemented; see `HARNESSES.md` for current state). |
| [research/TASK_SESSION_MODEL_REFACTOR.md](research/TASK_SESSION_MODEL_REFACTOR.md) | Completed Task/AgentSession/UserPrompt/ExecutionAttempt refactor design; see `SESSIONS.md` for current behavior. |
| [research/REPOSITORY_QUALITY_REVIEW.md](research/REPOSITORY_QUALITY_REVIEW.md) | Dated repository quality review (2026-08-01) with implementation progress. |
| [STATS.md](STATS.md) | Generated code statistics (LOC by language, tables, migrations, MCP tools, routes, harnesses). Regenerate with `scripts/gen-stats.sh`. |
