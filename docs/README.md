# Chetter Documentation

Status: **Index**

Use this page to pick the right document. `MANUAL.md` is the canonical operator guide; `FEATURES.md` is the quick capability scan. Research and reference docs are marked explicitly.

## Current Operation

| Document | Purpose |
|---|---|
| [MANUAL.md](MANUAL.md) | Canonical operations guide: setup, config, deployment, sandbox isolation, custom images, MCP tools, env vars. |
| [FEATURES.md](FEATURES.md) | Quick capability inventory (feature descriptions, no env/tool tables). |
| [HARNESSES.md](HARNESSES.md) | Runner harness architecture and supported agent CLIs. |
| [SCHEDULES.md](SCHEDULES.md) | Cron trigger/schedule management. |
| [REVIEWS.md](REVIEWS.md) | GitHub PR review automation. |
| [CONFIGURATION.md](CONFIGURATION.md) | Configuration-as-code: definitions repo, model catalog, sync, and change workflow. |
| [PAUSED_SESSIONS.md](PAUSED_SESSIONS.md) | Resumable session model, checkpoint/restore, and remaining work. |
| [K3S.md](K3S.md) | Canonical local k3s guide for running the Chetter stack with Docker/gVisor task execution. |
| [EKS.md](EKS.md) | Production EKS (or similar managed Kubernetes) installation guide. |
| [testing/k3d-gvisor.md](testing/k3d-gvisor.md) | Local Kubernetes and gVisor testing guide (k3d). |

## Planning

| Document | Purpose |
|---|---|
| [PLAN.md](PLAN.md) | Product roadmap and milestones. |

## Research And Reference

| Document | Purpose |
|---|---|
| [research/OPENHANDS.md](research/OPENHANDS.md) | OpenHands architecture findings relevant to Chetter. |
| [research/GVISOR.md](research/GVISOR.md) | gVisor feature research beyond checkpoint/restore. |
| [research/SNAPSHOTS.md](research/SNAPSHOTS.md) | Snapshot/checkpoint design reference (gVisor checkpoint, Docker commit, K8s alternatives). |
| [research/DAYTONA.md](research/DAYTONA.md) | Optional Daytona backend proposal. |
| [research/REVIEWER.md](research/REVIEWER.md) | Archived PR reviewer implementation plan; use `REVIEWS.md` for current behavior. |
| [research/UNIVERSAL_HARNESS.md](research/UNIVERSAL_HARNESS.md) | Universal harness architecture design (implemented; see `HARNESSES.md` for current state). |
