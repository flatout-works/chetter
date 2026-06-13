# Changelog

All notable changes to this project will be documented in this file.

## 2026-06-12

### Added

- Initial open source release of Chetter (formerly Devfleet): self-hosted MCP server for running autonomous AI development agents on a fleet of containerized runners. Includes server, runner, Dockerfiles, schedule templates, bundled skills, and documentation.
- Signature footer on PRs and reviews that identifies the agent name, model ID, runner image, and image digest (`CHETTER_AGENT_NAME`, `CHETTER_MODEL_ID`, `CHETTER_RUNNER_IMAGE`, `CHETTER_RUNNER_IMAGE_DIGEST`).
- Image build and push script (`deploy/build-and-push.sh`) for webhook-triggered runner image builds.
- Static website (`website/`) deployed to GitHub Pages via `.github/workflows/website.yml`.
- Client setup documentation and opencode skill for interacting with the Chetter MCP server.

### Changed

- Project renamed from Devfleet to Chetter across all source code, Dockerfiles, compose files, configurations, schedules, documentation, and assets.
- Docker Compose quick start simplified; environment variables moved to `.env.example`.
- Schedule YAML examples made generic and project-agnostic.

### Added

- Arcane-native CI builds: GitHub Actions workflow builds and pushes Docker images through the Arcane platform API, then redeploys the Chetter project.
- Runner entrypoint auto-resolves `CHETTER_RUNNER_IMAGE_DIGEST` from Docker inspect for PR signature footers.

### Changed

- NATS service renamed from `nats` to `chetter-nats` in compose files for consistent service naming.
- Bundled runner image pruned: removed templates and extra skills (`flatout-backend`, `go-mcp-server-generator`, `openapi`, `protobuf`, `sqlc`); only `golang-pro`, `mysql`, and `tidb-sql` skills remain.
- CI builds migrated from manual Docker push to Arcane project build API.

### Removed

- Schedule sync tool (`chetter_sync_schedules`) removed from MCP tools. Use `chetter_schedule_task` and `chetter_update_schedule` to manage schedules individually.

### Fixed

- Runner image references corrected across config, Dockerfile, compose, and schedule files.
- Local MySQL service extracted into a separate `deploy/compose.local.yaml` override so the default compose stack runs without a database dependency.
