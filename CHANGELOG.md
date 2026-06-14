# Changelog

All notable changes to this project will be documented in this file.

## 2026-06-14

### Added

- Nightly vulnerability scan schedule template (`schedules/chetter-nightly-vulnerability-scan.yaml`) that scans Go dependencies and Docker images and opens a PR with safe fixes.
- Arcane deployment flow documentation in README, covering GitOps sync, image builds on wowbagger, and required GitHub repository secrets.
- Invocation rule in the Chetter skill so that user messages addressed to "Chetter" are delegated to a runner task via `chetter_submit_task` instead of being solved locally.
- Registry HTTP API V2 lookup in the runner entrypoint for resolving `CHETTER_RUNNER_IMAGE_DIGEST` when the Docker CLI is not available (supports Docker Hub, GHCR with `GITHUB_TOKEN`, and other V2 registries).

### Changed

- Health endpoint now reports only live (non-stale) runners; stale runners are excluded from the list.
- Runner heartbeat interval reduced from 30s to 5s; runner presence timeout reduced from 120s to 60s.
- Runner ID generation changed from hostname-based to random UUID to avoid collisions between runners.
- `CHETTER_MODEL_ID` now uses the same model fallback chain as the OpenCode session, so it is never empty even when schedules omit `provider_id`/`model_id`.
- `CHETTER_*` footer env vars (`CHETTER_AGENT_NAME`, `CHETTER_MODEL_ID`, `CHETTER_RUNNER_IMAGE`, `CHETTER_RUNNER_IMAGE_DIGEST`) are now passed through in Kata mode.
- Changelog update schedule cron changed from `0 3 * * *` to `0 4 * * *`; docs update schedule renamed to `chetter-nightly-docs-update`, enabled, and cron changed from `0 4 * * *` to `0 3 * * *`.

### Fixed

- Runner event scanner no longer silently drops events when a single SSE data line exceeds 4 MiB; `opencodeEventLineMax` increased from 4 MiB to 64 MiB, fixing tasks that appeared hung due to large payloads.

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
