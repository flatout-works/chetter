# Changelog

All notable changes to this project will be documented in this file.

## 2026-06-15

### Added

- Multi-team support: `teams`, `users`, and `api_tokens` database tables with `team_id` columns on tasks and schedules for team-scoped resource isolation.
- Token-based authentication system supporting admin bypass (`MCP_AUTH_TOKEN`), SHA-256 hashed token lookup in the database, and automatic `team_id` injection into request contexts.
- Token management MCP tools (`chetter_create_token`, `chetter_list_tokens`, `chetter_delete_token`), REST API at `/api/v1/tokens`, and `chetterctl` CLI for creating, listing, and deleting tokens. Non-admin tokens see only resources scoped to their team.
- Schedule `chetter-nightly-website-presentation-update` for automated website and presentation content updates (runs daily at 05:00 UTC).
- `chetterctl` binary added to the default `make build` target.
- Trigger system: `trigger_type` (cron/pr_review) and `trigger_config` (JSON) columns on `chetter_schedules` replacing the purely-cron schedule model. Migration 004 adds columns with zero-downtime ALTER.
- Trigger CRUD MCP tools: `chetter_create_trigger`, `chetter_update_trigger`, `chetter_list_triggers`, `chetter_delete_trigger`, `chetter_run_trigger`. Type-specific validation (cron requires `cron_expr`, pr_review requires `repo` and `agent`).
- PR review dispatch via DB triggers: GitHub webhook queries `pr_review`-type triggers per repository and dispatches one review task per matching trigger, replacing hardcoded reviewer configuration.
- `team_id` column on `chetter_schedule_runs` for team-scoped run tracking (migration 005).
- Team/user CRUD MCP tools: `chetter_create_team`, `chetter_list_teams`, `chetter_delete_team` (cascades tokens/users), `chetter_list_users` (optionally filtered by team_name).
- Team-scoped MCP tool `chetter_list_schedule_runs` with optional `schedule_name` filter and team isolation checks.
- New documentation: `AGENTS.md` (repo guidance for LLM agents), `MANUAL.md` (operation manual with env reference and MCP tools guide), `SCHEDULES.md` (schedule lifecycle and YAML reference), `REVIEWS.md` (PR review architecture), `TRIGGERS_PROPOSAL.md` (trigger design proposal).

### Changed

- Replaced NATS embedded server with a ConnectRPC-based task queue backed by the database, removing the NATS dependency (NATS SDK, embedded server, NATS bus, NATS-specific config, related test fixtures and smoke tests). Runners now communicate with the server via ConnectRPC for task assignment and heartbeats.
- Runner agent execution refactored into a `harness.Harness` interface, decoupling agent backends from runner execution modes (local/Docker/Kata) for modular support of future agent runtimes.
- Dropped unused `listen_subject` and `result_subject` columns from `chetter_runners` (migration 002).
- `.env.example`, `Makefile`, `compose.yaml`, and runner configuration files updated following the NATS removal and ConnectRPC migration.
- Reaper and lease timings tightened: reaper interval 5m → 30s, grace period 5m → 120s, health staleness threshold 600s → 120s, task lease 120s → 60s. Reduces zombie task recovery from ~12min to ~90s.
- Schedule management migrated from cron-only `chetter_schedule_*` MCP tools to a generalized trigger system with `chetter_*trigger_*` tools. Cron engine now loads only `trigger_type='cron'` schedules.
- Website and presentation slides updated to reflect ConnectRPC architecture, token management, trigger system, and schema changes.

### Removed

- `chetter_schedule_task`, `chetter_list_schedules`, `chetter_delete_schedule`, `chetter_update_schedule`, `chetter_run_schedule` MCP tools removed (replaced by trigger tools).
- `GITHUB_REVIEW_ALLOWED_REPOS` environment variable and hardcoded `ReviewerAgent`/`ReviewerProviderID`/`ReviewerModelID`/`ReviewerTimeoutSec` config fields removed; repo allowlisting and reviewer configuration is now per-trigger via `trigger_config`.

### Fixed

- Backfill `NULL` trigger_config in existing schedules after ALTER TABLE ADD COLUMN, preventing startup crash when sqlc's `json.RawMessage` scans a `NULL` value.
- Runner now resolves the model from the agent's `.opencode/agent/<agent>.md` config when `provider_id`/`model_id` are not specified in schedule or task requests, instead of falling back to hardcoded defaults.
- Schedule YAML `agent_image` references corrected from `your-org` to `flatout-works`.
- Webhook reviewer configuration (`ReviewerAgent`, `ReviewerProviderID`, `ReviewerModelID`, `ReviewerTimeoutSec`) now actually flows from `HandlerConfig` to the review task submitter instead of being hardcoded.
- Database connection pool sets `SetConnMaxIdleTime(5m)` to recycle idle connections before TiDB's server-side `wait_timeout` kills them, eliminating noisy `broken pipe` / `closing bad idle connection` log spam.

## 2026-06-14

### Added

- Nightly vulnerability scan schedule (`chetter-nightly-vulnerability-scan`) scanning Go dependencies and Docker images.
- Registry HTTP API V2 lookup for `CHETTER_RUNNER_IMAGE_DIGEST` resolution in runner images without Docker CLI (supports Docker Hub, GHCR, and other V2 registries).
- `CHETTER_RUNNER_IMAGE_DIGEST` environment variable exposed in compose files for deployments that pin the image digest explicitly.
- `schedules-examples/` directory for example schedule templates; `schedules/` now contains only active production schedules.

### Changed

- `CHETTER_MODEL_ID` now resolves using the runner's promptModel fallback chain instead of raw `provider_id/model_id` fields, so it is never empty even when schedules omit those fields.
- Example schedules moved from `schedules/` to `schedules-examples/` (code-quality-audit-daily, nightly-dependency-upgrade, nightly-issue-fixer, nightly-vulnerability-scan, weekday-doc-review).
- Schedule cron times adjusted: changelog update at :04, docs update at :03.
- Runner heartbeat interval reduced from 30s to 5s; runner presence timeout reduced from 120s to 60s.
- Runner IDs now generated as random UUIDs instead of HOSTNAME-based identifiers.
- Health endpoint reports only live (non-stale) runners instead of including stale runners.

### Fixed

- Runner event line max increased from 4 MiB to 64 MiB to prevent silent event drops when OpenCode SSE payloads exceed the previous limit.

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
