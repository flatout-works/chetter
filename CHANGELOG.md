# Changelog

All notable changes to this project will be documented in this file.

## 2026-06-12

### Added

- Initial open source release of Chetter (formerly Devfleet): self-hosted MCP server for running autonomous AI development agents on a fleet of containerized runners. Includes server, runner, Dockerfiles, schedule templates, bundled skills, and documentation.
- Signature footer on PRs and reviews that identifies the agent name, model ID, runner image, and image digest (`CHETTER_AGENT_NAME`, `CHETTER_MODEL_ID`, `CHETTER_RUNNER_IMAGE`, `CHETTER_RUNNER_IMAGE_DIGEST`).
- Runner entrypoint auto-detects `CHETTER_RUNNER_IMAGE_DIGEST` via Docker inspect so PR footers include accurate image digests.
- Arcane-based CI pipeline: GitOps sync, per-image builds via the Arcane API, and project redeploy on merge to main.
- Root-level `compose.yaml` with build directives for Arcane-native image builds.

### Changed

- Project renamed from Devfleet to Chetter across all source code, Dockerfiles, compose files, configurations, schedules, documentation, and assets.
- Docker Compose quick start simplified; environment variables moved to `.env.example`.
- Schedule YAML examples made generic and project-agnostic.
- Bundled runner skills pruned: removed project-specific skills (flatout-backend, go-mcp-server-generator, openapi, protobuf, sqlc) and templates (go-huma-gin); only general-purpose skills (golang-pro, mysql, tidb-sql) and OpenCode agents remain. Updating a bundled skill now requires rebuilding and redeploying the runner image.
- Removed the `chetter_sync_schedules` bulk YAML sync tool; schedules are now managed individually through `chetter_schedule_task` and `chetter_update_schedule`.
- Docker Compose switched from pulling pre-built GHCR images to building locally via `build:` directives, with `chetter-runner-base` as a separate build stage.
- CI pipeline migrated from GitHub Actions image builds to Arcane API-driven builds and deployment.

### Fixed

- Runner image references corrected across config, Dockerfile, compose, and schedule files.
- Local MySQL service extracted into a separate `deploy/compose.local.yaml` override so the default compose stack runs without a database dependency.
- `ARG BASE_IMAGE` declared globally before the first `FROM` in `runner/Dockerfile.chetter` so Docker build args propagate across all stages.
- Arcane CI workflow reads the project directory from the API response instead of assuming a fixed path.
- NATS service name updated to `chetter-nats` across compose and entrypoint for consistency.
