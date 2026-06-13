# Changelog

All notable changes to this project will be documented in this file.

## 2026-06-12

### Added

- Initial open source release of Chetter (formerly Devfleet): self-hosted MCP server for running autonomous AI development agents on a fleet of containerized runners. Includes server, runner, Dockerfiles, schedule templates, bundled skills, and documentation.
- Signature footer on PRs and reviews that identifies the agent name, model ID, runner image, and image digest (`CHETTER_AGENT_NAME`, `CHETTER_MODEL_ID`, `CHETTER_RUNNER_IMAGE`, `CHETTER_RUNNER_IMAGE_DIGEST`).
- Image build and push script (`deploy/build-and-push.sh`) for webhook-triggered runner image builds.
- Static website (`website/`) deployed to GitHub Pages via `.github/workflows/website.yml`.
- Client setup documentation and opencode skill for interacting with the Chetter MCP server.
- Root `compose.yaml` with build directives for Arcane-compatible image builds of all Chetter services.
- Runner auto-resolves `CHETTER_RUNNER_IMAGE_DIGEST` from Docker inspect for PR signature footers.

### Changed

- Project renamed from Devfleet to Chetter across all source code, Dockerfiles, compose files, configurations, schedules, documentation, and assets.
- Docker Compose quick start simplified; environment variables moved to `.env.example`.
- Schedule YAML examples made generic and project-agnostic.
- CI migrated from Wowbagger webhook triggers to Arcane API for building, pushing, and redeploying images.
- `deploy/compose.yaml` now supports local image builds via `compose build` with a configurable `BASE_IMAGE` build arg for the runner.
- Schedule management workflow changed: schedules are now created and updated individually via `chetter_schedule_task` and `chetter_update_schedule` instead of bulk syncing from a YAML directory.
- Schedule templates renamed with `chetter-` prefix for consistency (`chetter-nightly-changelog-update`, `chetter-nightly-docs-update`).

### Removed

- `chetter_sync_schedules` MCP tool removed; schedules are managed individually instead of bulk-synced from a directory.
- Bundled project-specific skills (flatout-backend, protobuf, openapi, sqlc, go-mcp-server-generator) and templates (go-huma-gin) removed from the runner image; mount custom skills at runtime instead.
- `docs/TEMPLATES.md` removed.
- `deploy/rebuild-on-wowbagger.sh` removed (superseded by Arcane CI).

### Fixed

- Runner image references corrected across config, Dockerfile, compose, and schedule files.
- Local MySQL service extracted into a separate `deploy/compose.local.yaml` override so the default compose stack runs without a database dependency.
- Runner `Dockerfile.chetter` declares `BASE_IMAGE` build arg globally so it is visible in multi-stage `FROM`.
- LICENSE copyright holder corrected.
