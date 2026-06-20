# Changelog

All notable changes to this project will be documented in this file.

## 2026-06-20

### Added

- Runner drain mechanism: `chetter_drain_runner` MCP tool requests a runner to stop claiming new tasks, finish in-flight work, then exit. The runner reports `draining` status on heartbeats. CI adds a drain step before redeploy to ensure zero tasks are running during rollout.
- Network egress controls: `CHETTER_PROXY_ALLOWED_DOMAINS`, `CHETTER_PROXY_BLOCKED_DOMAINS`, and `CHETTER_DNS_BLOCKED_DOMAINS` environment variables for restricting outbound traffic from runner containers (default: unfiltered).

### Changed

- Server now requires `CHETTER_MCP_AUTH_TOKEN` to start; fails with a clear error if unset or if a placeholder value like `change-me` is used.
- Runner ConnectRPC requires a dedicated `CHETTER_RUNNER_RPC_TOKEN` environment variable; admin and team-scoped MCP tokens are no longer accepted as runner RPC credentials.
- Per-ID task MCP tools (`chetter_get_task`, `chetter_cancel_task`, `chetter_list_task_events`, `chetter_task_export`) are now scoped by `team_id`. Fleet-wide tools (`chetter_list_tasks`, `chetter_list_runners`, `chetter_list_audit_events`, `chetter_list_task_artifacts`, `chetter_clear_queue`) restricted to admin tokens.
- RPC-based agent harnesses now run inside the agent Docker container (instead of on the host) with readiness routed via the host port.
- Deployment compose files use local Docker image tags (`chetter-mcp:latest`, `chetter-runner:latest`) instead of GHCR-published tags; GHCR push deferred. Builder auto-builds the runner base image when missing locally.
- gVisor sandbox containers now receive `HTTP_PROXY`/`HTTPS_PROXY` environment variables for outbound proxy routing.
- Default proxy allowlist expanded with `github.com` subdomains (`api.github.com`, `uploads.github.com`, `codeload.github.com`, `objects.githubusercontent.com`) and `registry.npmjs.org`.
- GLM model references updated to 5.2.

### Fixed

- Reaper now uses `started_at` (not `updated_at`) for task timeout detection, preventing tasks from running indefinitely past their timeout when heartbeats keep `updated_at` current. Lease reclaim also resets `started_at` so reclaimed tasks get a fresh timeout window.
- Host-side MCP tools (`workspace_bash`, `git_*`, `fetch_url`, `deploy_*`) removed from the runner tool set, preventing sandbox escape from the task container.
- Webhook fork/opened PR, issue, and issue-comment triggers now require the author to have write access to the repository, preventing unauthorized contributions from triggering tasks.
- Server auto-creates the configured database on startup (`CREATE DATABASE IF NOT EXISTS`), preventing crash-loop when `DATABASE_DSN` points to a non-existent database (e.g. a fresh TiDB Cloud Starter cluster).
- Bundled local TiDB now starts with the `unistore` engine (no PD/TiKV required) and drops the unusable container healthcheck; MCP server retries until TiDB accepts connections.
- Deploy compose empty-string env var defaults fixed for `chetter-runner-2`: `:-""` → `:-}` to prevent injecting literal `""` into YAML, which caused a crash-loop on config parse.
- RPC harness readiness poll now uses the host-mapped port for gVisor, removing a dead store in the serve loop.
- Integration test suite shares a single TiDB container across all tests via `TestMain`; `ClaimTask` long-poll interval reduced from 30s to 1s for faster test execution.

### Documentation

- README quick start now includes the `./deploy/build.sh` image build step and corrects the `agent_image` example to reference the local `chetter-runner:latest` image.
- Deploy documentation (`deploy/compose.local.yaml`) clarifies the bundled TiDB is the unistore test engine (not production-ready; no vector/HTAP support).
- TiDB Cloud references updated from "Serverless" to "Starter/Essential" naming throughout README and compose files.
- Proxy/DNS allowlist and blocklist configuration documented in `docs/FEATURES.md`.

## 2026-06-17

### Added

- Claude Code harness integration: runners can now use Claude Code instead of OpenCode via `execution.harness: claude-code` config. Adds `SupportsServe()` interface method to distinguish HTTP-serve harnesses (OpenCode) from batch-only harnesses (Claude Code), with MCP config generation (`.claude/mcp.json`), event streaming via stream-json line parsing, and `ANTHROPIC_API_KEY` forwarding. `@anthropic-ai/claude-code` installed in the runner base image.
- Runner image variants for golang, python, node, rust, and minimal environments under `runner/images/`, with CI change detection (`dorny/paths-filter`) to only rebuild images whose inputs changed.
- gVisor sandbox execution support via `USE_GVISOR` config option, providing kernel-level isolation for Docker task containers without the port mapping limitations of Kata Containers.
- Docker socket mount and `USE_GVISOR` flag in deployment compose configuration (`deploy/compose.yaml`).
- Kubernetes deployment manifests under `deploy/k8s/` with namespace, secrets, MCP deployment+service, runner deployment, and gVisor RuntimeClass.
- k3s local testing guide and gVisor sandbox isolation documentation in `README.md`.

### Changed

- Session export rewritten to read directly from the opencode SQLite database (`opencode.db`), replacing the broken HTTP `/export` endpoint. `ReadSessionExport` method added to the `Harness` interface with a no-op implementation for Claude Code.
- Arcane API calls in CI deploy workflow now retry up to 3 times with 5s backoff on server errors (5xx), instead of failing on the first attempt.

### Fixed

- Runner no longer overwrites `started_at` on intermediate status updates; `ended_at` is now set only on terminal statuses (completed, error, cancelled), preventing premature end timestamps on running tasks.
- Deploy compose interpolation fixed: empty-string variable defaults are now quoted (`${VAR:-""}`) everywhere to prevent Docker Compose from treating them as null.
- CI build workflow: `CACHEBUST` build arg added to force full Docker layer rebuilds, ensuring runner images pick up the latest base image on each deployment.
- Runner SSE event parsing uses `bufio.Reader` instead of `bufio.Scanner`, preventing "token too long" errors when opencode emits large event payloads.

### Removed

- Kata Containers/containerd execution backend. gVisor replaces it as the optional sandbox isolation layer without port mapping limitations from the micro-VM.

## 2026-06-16

### Added

- Claude Code harness: runners can use Claude Code instead of OpenCode by setting `execution.harness: claude-code` in task configs. Requires `ANTHROPIC_API_KEY`. Adds `SupportsServe()` to the `Harness` interface to distinguish HTTP-serve harnesses (OpenCode) from batch-only harnesses (Claude Code).
- Session export for completed tasks: `chetter_task_export` MCP tool returns the markdown transcript from a completed OpenCode session, stored in a new `session_export` column on `chetter_tasks` with zero-downtime auto-migration (migration 007). Corresponding `chetter-export` command added to `.opencode/opencode.json`.
- Webhook `/chetter-review` comment trigger now adds the review label and posts an acknowledgment comment before dispatching the review task.

### Changed

- Webhook file-pattern auto-review removed; PRs are labeled only when a trigger actually fires, preventing stale labels on non-matching PRs.
- `prompt` field made optional in `chetter_create_trigger` and `chetter_update_trigger` — `pr_review` triggers fall back to a built-in review template when no prompt is supplied.
- Runner forwards `GITHUB_TOKEN` and `SYNTHETIC_API_KEY` to task containers alongside provider API keys, enabling `gh` CLI usage (e.g. PR creation) from docs/changelog/website task containers.
- Runner uses a dedicated HTTP client with 45s timeout for `ClaimTask` long-poll (was sharing the 10s RPC client, causing timeout warnings on every idle poll).

### Fixed

- Webhook `/chetter-review` handler no longer adds the label before dispatching the review, preventing duplicate tasks triggered by the resulting `pull_request.labeled` webhook event.
- Webhook async context cancel function now properly released, eliminating a context leak.
- Session export rewritten to read opencode's SQLite database directly, replacing the broken HTTP /export endpoint. Compatible with opencode v1.17.4's schema (message/part tables, XDG data directory).
- Task `started_at` preserved from claim time; `ended_at` only set on terminal statuses instead of on every heartbeat.
- Compose file variable interpolation syntax corrected for deployment compatibility. CI workflow adds retry logic to Arcane deploy step and `CACHEBUST` build arg to prevent stale Docker cache layers.
- Refactor: dead code removed (`nullableTime`, `envList`, `extractStatusFromLine`), `NullTimePtr` exported from `store` package, schedule lookups in `DeleteTrigger`/`RunTriggerNow` optimized from linear search to direct SQL query, and async webhook background calls given proper timeouts.

### Removed

- File-pattern auto-review logic (`matchesCodePaths`/`matchesCodePath`) removed from webhook handler, simplifying the PR review decision tree.

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
