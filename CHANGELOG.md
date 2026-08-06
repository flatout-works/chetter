# Changelog

All notable changes to this project will be documented in this file.

## [0.1.0] - 2026-08-06

First tagged release of Chetter: a self-hosted MCP server and runner fleet for
autonomous AI development tasks.

### Added

- **Tasks & sessions**: submit, track, and resume AI development tasks through MCP tools
  (`chetter_submit_task`, `chetter_recover_task`) or the web UI; resumable agent
  sessions (harness sessions or gVisor checkpoints) with task recovery from session exports.
- **Runner fleet**: Docker and Kubernetes execution with gVisor/runsc sandboxing,
  enforced isolation admission, per-task memory/CPU limits, heartbeat-based fleet
  health, draining, and self-test profiles (`chetter_run_self_test`).
- **Harnesses**: OpenCode, Claude Code, Pi, CodeWhale, and Codex execution with an
  in-session runner bridge exposing task artifacts and GitHub tools.
- **Triggers**: GitHub webhook triggers (push, PR review, issues, comments), cron
  triggers, and manual test runs for external-event triggers.
- **Web UI**: task dashboard, fleet view, trigger management, audit log, diagnostics,
  settings, and OIDC/OAuth SSO (Okta) with admin/team-group mapping.
- **Databases**: MySQL, TiDB, and PostgreSQL support via goose migrations.
- **Operations**: `/healthz`, `/readyz`, and `/api/server-info` endpoints; server
  version (`x.y.z`), git hash, and uptime shown in the UI footer.

### Changed

- Database tables renamed from `chetter_*` prefixes to unprefixed names (migration 052).
- Claiming optimized with an in-process notifier, cutting idle DB polling ~20x.

### Fixed

- Runner claiming on TiDB serverless (`SKIP LOCKED` planner bug) via a `NOT EXISTS`
  rewrite.
- Session timezone bug: TiDB session time zone must be UTC or fleet presence expires.

Detailed per-day history of everything that went into this release is below.

## 2026-08-05

### Added

- OIDC/OAuth SSO for the web UI (Okta) (#94): sign-in via `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `OIDC_REDIRECT_URL`, with admin and team-group mapping via `OIDC_ADMIN_GROUP` and `OIDC_TEAM_GROUP_PREFIX`, and session JWTs via `OIDC_SESSION_SECRET`/`OIDC_SESSION_TTL`. Ships a fake OIDC provider (`internal/oidctest`) used by handler and auth tests. Documented in `docs/MANUAL.md` and `docs/FEATURES.md`.
- Enforced isolation admission (#291, #292): runners advertise enforced gVisor/runsc sandboxing (or a gVisor runtime class on Kubernetes) in claim/heartbeat metadata. Tasks that require isolation — resumable sessions, explicit `isolation: required`, or every task on a hardened fleet — are admitted only to isolation-capable runners and otherwise fail fast with a terminal `isolation_unavailable` error. `CHETTER_ALLOW_UNISOLATED` is the documented escape hatch for single-tenant deployments. New migrations (MySQL/TiDB 051, PostgreSQL 027) add `chetter_runners.isolation_enabled` and `chetter_agent_sessions.isolation_required`.
- Manual test runs for external-event triggers (#271): a new "Test Trigger" action on the web UI trigger detail page exercises `pr_review` and `issue` triggers without waiting for a real GitHub webhook. The new `TriggerService.TestTrigger` RPC validates the simulated event, resolves the repo installation, fetches authoritative PR/issue metadata from GitHub (PR base/head refs and head clone URL, or issue title/body/labels), and dispatches through the same trigger matching and task configuration as the real webhook path. Test-originated tasks are stamped `submission_source = trigger_test` and recorded with a `trigger_test_run` audit event; the resulting task IDs link to the task pages and appear in the trigger's Recent Runs table. Cron triggers keep the existing Run Now path (`chetter_run_trigger` covers cron only).

### Fixed

- Runner claiming on TiDB serverless: the isolation-admission JOIN broke the `SELECT ... FOR UPDATE SKIP LOCKED` claim query on TiDB (`Error 1105`), so the fleet could not claim any task after migration 051. The isolation check is now expressed as a `NOT EXISTS` subquery, preserving the same admission semantics and verified against the live production database.
- Deploy: `runsc` is now bind-mounted read-only into both runner containers (`/usr/bin/runsc:/usr/bin/runsc:ro`) and `docs/DEPLOYMENT.md`'s stale claim corrected — without `runsc` on the runner's PATH no runner advertises isolation and every isolation-requiring task fails with `isolation_unavailable`.
- ops: `tidb-cloud-migrate.sh` is now usable against MySQL targets — drops `--single-transaction` (TiDB Cloud rejects the SAVEPOINT mysqldump uses with it), adds `MIGRATE_DRIVER`/`PREPARE_CMD` and `PRE_IMPORT_SQL`/`POST_IMPORT_SQL` drift hooks, and fixes the verify backtick escaping that made bash execute the table name.
- ops: `tidb-bootstrap.sh` topology template synced with the TiUP 1.17 schema — the old pre-1.17 format (top-level `labels`/`location_labels`) is rejected by TiUP v1.17; now uses TiKV location labels via `config.server.labels`, `server_configs.pd` replication location labels, TiDB UTC time-zone, and the memory caps.

### Changed

- Database table prefix dropped: the 14 `chetter_*` application tables were renamed to unprefixed names (`tasks`, `task_events`, `user_prompts`, `execution_attempts`, `agent_sessions`, `agent_session_checkpoints`, `task_artifacts`, `runners`, `triggers`, `trigger_runs`, `event_callbacks`, `model_catalogs`, `audit_log`, `webhook_deliveries`) via migration 052 (MySQL/TiDB) and 028 (PostgreSQL). Queries and sqlc output regenerated for both dialect trees, and generated struct types drop the prefix (e.g. `repository.ChetterTask` → `repository.Task`). Prometheus metric names are unchanged; index names keep their historical `idx_chetter_*` names so fresh and migrated installs stay identical.

### Documentation

- New `docs/SCHEMA.md`: database schema reference with domain-grouped mermaid ER diagrams covering all 24 application tables (as of migration 051), plus schema-wide notes (no FK constraints, `search_text` FTS columns, UTC time-zone requirement).
- `docs/TIDB-WOWBAGGER.md` rewritten from a migration runbook into a current-state ops document: cluster shape and credentials, DSN routing (MCP server only), the UTC time-zone requirement that broke the fleet (`SYSTEM` tz shifted every `TIMESTAMPDIFF`-based age), migration history and schema drift gotchas, the 051 SKIP LOCKED planner bug, and the runsc isolation requirement. `AGENTS.md` gains matching gotchas.

## 2026-08-02

### Added

- Profile-based deployment self-tests: new `chetter_run_self_test` and `chetter_self_test_status` MCP tools (admin only) backed by `RunSelfTest`/`GetSelfTestStatus` AdminService RPCs. Four profiles — `quick` (default OpenCode harness), `harnesses` (opencode, claude-code, pi, codewhale, codex), `providers` (every model provider enabled for the reference OpenCode harness), and `full` (all harness and provider checks plus a GitHub App credential check when `CHETTER_SELF_TEST_GITHUB_REPO` is set). Each check submits a real task that must call the runner-bridge `chetter_runner_self_test_echo` MCP tool with a signed nonce; a completed task only counts as passed when the server observes that runner-side MCP evidence. Harness checks are pinned to cheap known-good provider/model combinations. Self-test metadata columns on tasks (`self_test_run_id`, `self_test_profile`, `self_test_check`, `self_test_nonce`) via new migrations (MySQL 050, PostgreSQL 026), with `self_test.started` audit events.
- Web UI Diagnostics page (`/diagnostics`) with self-test profile selection, run controls, live polling, and per-check status/evidence display; nav entry placed below Admin.
- In-process claim notifier cutting idle DB polling for task claiming ~20x: `ClaimTask` long-polls are woken immediately by a broadcast whenever work becomes claimable (SubmitTask, RecoverTask, RerunTask, ResumeAgentSession, and the reaper lease-requeue path), and the server safety-net poll interval rose from 1s to 15s. Runners claim via a single per-runner loop instead of one long-polling goroutine per concurrent slot, so an idle fleet costs roughly one `SELECT ... FOR UPDATE SKIP LOCKED` per runner per 15s instead of ~1 tx/s per slot. The single-replica assumption is documented in AGENTS.md.

### Fixed

- Pi harness: removed the `idleTimeout` override from the runner-bridge MCP config — the pi MCP adapter misreads it as a per-server idle override.
- Claude Code harness: `claude-serve-proxy` now passes `--settings` explicitly because Claude Code v2.1.196+ ignores MCP approvals in project settings for untrusted folders, which left `.mcp.json` servers stuck at "pending approval".
- Codex harness: `codex-serve-proxy` creates `CODEX_HOME` when missing (Codex refuses to start if the directory does not exist); native configs default to the `responses` wire API with an `openai-completions` opt-in for providers that only speak chat completions.
- Self-test harness checks: CodeWhale sessions now create threads with `auto_approve` and `allow_shell`, and Codex checks use DeepSeek's Responses API (supports `deepseek-v4-flash`) instead of the synthetic provider.
- OpenCode harness: the runner-bridge MCP server is preserved alongside the Chetter relay in the inline config — some OpenCode versions replace nested MCP maps when applying `OPENCODE_CONFIG_CONTENT`, previously hiding the per-execution runner bridge and its artifact tools.
- Runner: diagnostic events are now published with the task request so they retain correct execution attribution on the Docker and Kubernetes paths.
- Deploy: `deploy/compose.yaml` corrected `HOST_WORKSPACE_ROOT` to point at the `workspaces` subdirectory, and the runner warns when the mapped host workspace mount path does not exist (Docker silently creates missing mounts as empty directories).
- Migrations: self-test metadata column additions split into separate `ALTER TABLE` statements, avoiding TiDB's rejection of multi-column additions whose `AFTER` clause references a column added in the same statement.

### Documentation

- Docs restructured into a MANUAL-backed layout: `SCHEDULES.md` + `REVIEWS.md` merged into `TRIGGERS.md`; `PAUSED_SESSIONS.md` renamed to `SESSIONS.md`; deployment/gVisor content extracted to new `DEPLOYMENT.md`, agent-image content to new `IMAGES.md`, and definitions-repo YAML/Git identities/MCP endpoints moved into `CONFIGURATION.md`; completed design docs (`TASK_SESSION_MODEL_REFACTOR.md`, `REPOSITORY_QUALITY_REVIEW.md`) moved to `docs/research/`; `MANUAL.md` slimmed to the canonical operator guide.
- Root `README.md` gains a "Next Steps" section and a full docs index; new `LASTWEEK.md` with a 7-day feature summary.
- `AGENTS.md` documents the single-replica claim-notifier assumption and the TiDB multi-column `ALTER TABLE` limitation.

## 2026-08-01

### Added

- Task recovery with an editable custom prompt (#272): `RecoverTask` RPC and `chetter_recover_task` MCP tool accept an optional `custom_prompt` used verbatim as the recovery session's prompt while still attaching the previous session export as a workspace file; when omitted, the default recovery instruction is used. The task detail page opens a recovery modal pre-filled with the default instruction.
- Per-task memory/CPU limits on Docker execution (#273): `MaxMemoryMB`/`MaxCPU` on tasks are translated into real Docker container limits (`--memory`, `--memory-swap`, `--cpus`) on the serve, resume, and RPC execution paths, with per-task values taking precedence over the runner config fallback. OOM-killed task containers now fail with a structured `resource_limit` failure category on all three paths, and heartbeats report the effective container memory/CPU limits surfaced in the fleet web UI.
- GitHub multi-installation support: the GitHub App integration resolves the App installation per repository instead of requiring a single configured installation ID, with installation-isolated token and credential caches. Runner GitHub RPCs (issue/PR/comment/review) authenticate against the task repository's installation, and task GitHub metadata (`github_repo`, `github_installation_id`) is stored via new MySQL and PostgreSQL migrations. Legacy single-installation config remains supported via `GITHUB_INSTALLATION_ID`.
- MCP relay rejection observability: runners report a cumulative count of unauthorized MCP relay requests in heartbeats, exposed as a `chetter_mcp_relay_rejected_requests` Prometheus metric.

### Fixed

- Web UI: agent-generated markdown is now sanitized with DOMPurify before rendering, closing a stored XSS vector on task/agent content.
- Runner workspace path hardening: extra files and workspace writes are confined to the task workspace — absolute paths, `..` traversal, and symlink escapes are rejected instead of writing outside the workspace.
- Runner MCP server authentication: per-task MCP servers and the execution-scoped MCP relay now require bearer-token authentication, and credential-bearing config files are written with owner-only (0600) permissions.
- Runner: configured container resource limits now act as hard caps — per-task limits may be stricter but can never raise the configured caps.
- Runner: OOM-style errors are prioritized and classified as `resource_limit` on both runner and server, with OOM taking precedence over deadline classification.
- PostgreSQL: ordered Goose migrations are applied at startup (in `ApplySchema`) before the idempotent bootstrap statements, and `chetter-migrate` handles the postgres dialect.
- Definitions: automatic git maintenance and gc are disabled during definition repo sync to prevent interruption.
- Pi harness: explicitly qualified models are prioritized over catalog defaults.

### Documentation

- New `docs/REPOSITORY_QUALITY_REVIEW.md` — repository quality review.
- New `docs/plans/2026-07-31-001-github-multi-installation-plan.md` — GitHub multi-installation support plan.
- `docs/MANUAL.md`, `AGENTS.md`, `docs/FEATURES.md`, `docs/HARNESSES.md`, and `website/current/index.html` / `website/old/technical.html` updated for custom-prompt task recovery, the PostgreSQL startup migration policy, GitHub multi-installation, and runner MCP capability boundaries.

## 2026-07-30

### Added

- Central server-side task and trigger validation (#80): harness must be empty (runner default) or one of the supported harnesses (opencode, claude-code, pi, codewhale, codex) — unknown harnesses are rejected instead of silently falling back to OpenCode; `session_mode` must be empty, "none", or "resumable"; `timeout_sec` and `ttl_hours` must be non-negative and within operator-configurable limits (`CHETTER_MAX_TASK_TIMEOUT_SEC`, `CHETTER_MAX_SESSION_TTL_HOURS`); GitHub trigger repos (pr_review/issue) must use canonical owner/repo syntax. Validation lives in `internal/validation` and is wired into `Service.SubmitTask` and `Service.CreateTrigger`/`UpdateTrigger`, the single chokepoints for all task and trigger ingress, so MCP, ConnectRPC/web UI, and webhook-triggered submissions share the same rules; invalid requests are rejected before persistence. Agent/skill names remain hints resolved lazily by the runner; MCP endpoint existence continues to be validated centrally in SubmitTask.
- Runner CPU (`--cpus`) and PID (`--pids-limit`) limits for task containers (#54), so a single misbehaving task cannot exhaust host CPU or process IDs. Configurable via `execution.container_cpu`/`container_pids` (YAML) or `CHETTER_CONTAINER_CPU`/`CHETTER_CONTAINER_PIDS`/`CHETTER_CONTAINER_MEMORY` env vars; limits are applied consistently across serve, resume, and RPC container paths, and the memory limit is now also applied to the RPC path for consistency. Flags are only emitted when set, so unset limits leave behavior unchanged. Negative or unparseable env overrides fail runner startup validation. Added to `schemas/runner.schema.json`.

### Fixed

- Cross-team definition confidentiality exposure (#262): definition get/list MCP tools and runner-side agent/skill materialization resolved definitions by name only, ignoring team/repo scope, so any authenticated team token could fetch another team's agent, skill, trigger, task-template, or MCP-endpoint definition content, and a task submitted by one team naming another team's agent received that team's definition (including MCP endpoint references) injected and executed. Both paths now resolve to global plus the caller's own team-scoped definitions; cross-team names resolve as not-found (fail closed), and team-scoped agent definitions override global ones for the owning team's tasks. `getDefinition` resolves from all matching definitions and picks the highest-precedence visible one (global > team > repo, then most recently updated).

### Documentation

- `website/old/technical.html` corrected: failure-category list aligned to the canonical task-level set (timeout, harness_error, runner_lost, internal_error, user_cancelled, quota_exceeded, unknown), and the Kubernetes pod execution backend (`execution.backend: kubernetes`, `KubernetesExecutor`) documented.

## 2026-07-29

### Fixed

- Trigger identity is now stable across definition syncs: synced triggers receive a deterministic identity keyed on (definition source ID, trigger `name`) so re-syncs reuse the same trigger ID instead of minting a fresh random ID each time. In-memory cron registrations are reconciled against the full desired trigger set after each sync, dropping schedules for removed or renamed triggers and for synced triggers that are disabled or no longer cron-typed; API and other-source triggers are left untouched. This stops stale cron entries from firing `runTrigger` against deleted trigger IDs. Renames are treated as delete-old + create-new — trigger run history and usage attribution do not follow a rename (#256).

### Documentation

- Optional Mem9 persistent memory integration for the OpenCode harness documented in `docs/MANUAL.md`: enabled only when the runner starts with a non-empty `MEM9_API_KEY` (runner-wide, not per-task; other harnesses unaffected). Configurable via `MEM9_API_KEY`, `MEM9_API_URL`, `MEM9_DEBUG`, `MEM9_HOME`, and `MEM9_PLUGIN_SPEC`.
- Website redesigned and relaunched: a new modern site under `website/current/` (with an alternate variant under `website/alt/`); the previous site moved to `website/old/`.

## 2026-07-28

### Added

- Kubernetes pod execution backend: `execution.backend: kubernetes` runs each task as an independent Kubernetes Pod instead of a Docker container, with configurable namespace, runtime class (gVisor via `kata` or `run.kata`), image pull policy, service account, workspace PVC or hostPath, node name, and pod readiness timeout. New `KubernetesExecutor` manages pod lifecycle including creation, readiness polling, log streaming, workspace event streaming, cleanup, and task event forwarding. Runner entrypoint generates `kubernetes:` config block from env vars.
- PR review webhook now triggers on `synchronize` (push to PR branch) and `reopened` events in addition to `opened` and the `chetter-review` label.
- Deploy manifests: `deploy/k3s/kubernetes-runner.yaml` for K3s, `deploy/k8s/runner-rbac.yaml` and `deploy/k8s/runner-workspace-pvc.yaml` for generic Kubernetes RBAC and workspace PVC setup.
- Private fork workflow documentation: `docs/PRIVATEFORK.md` covers PR review and issue triggers on forked repositories.
- API token expiry enforcement: nullable `expires_at` column on `api_tokens`; token creation accepts an optional `expires_in_hours` (null = no expiry); the auth resolver rejects expired tokens (`expires_at <= now`); token listing exposes `expires_at`. Auto-migrated on startup via `ensureAPITokenExpiryColumn` (#246).
- `/readyz` endpoint on both MCP and web API muxes that verifies schema application and performs a lightweight DB ping, with Kubernetes `livenessProbe` (`/healthz`) and `readinessProbe` (`/readyz`) added to `deploy/k8s/mcp-deployment.yaml` (#93).
- Prometheus `/metrics` endpoint on the MCP server (no auth, like `/healthz`) exposing Go process/runtime collectors plus `chetter_*` gauges for task counts by status, runner fleet health (active/stale/slots), and webhook delivery status, with bounded cardinality (no task/runner/token/user IDs as labels) (#92).
- Secret redaction in runner output: a transient per-execution set built from sensitive environment variables (keys containing `SECRET`, `TOKEN`, `KEY`, or `PASSWORD`) replaces exact occurrences with `[REDACTED]` before any output is persisted or published — covering task events, terminal summaries/errors, session exports, callback data, and live events. The set is held in memory only and never logged, audited, or persisted; values shorter than 4 characters are skipped (#247).
- Web UI: compact Pi session activity view on the task detail page. The export viewer renders a compact activity transcript for Pi sessions with bounded tool-result and argument previews; "View" is renamed to "View activity" and "Export" to "Download full". A new `compact` flag on the `ExportTask` RPC selects compact versus full export.

### Fixed

- Webhook: Chetter-authored PR comments no longer resume the reviewed session or trigger recursive review requests. `GetAppLogin` uses app JWT directly instead of installation token for reliable bot login detection.
- PR review label events from Chetter's own bot login are ignored, preventing infinite review loops when Chetter adds the review label to a PR it is reviewing.

### Changed

- Valid label check in `triggerActionFromPR` no longer falsely overrides `synchronize` or `reopened` actions when a review label already exists on the PR.

### Documentation

- `docs/EKS.md`, `docs/K3S.md`, `docs/EXECUTION.md`, `docs/MANUAL.md`, `docs/PLAN.md`, `runner/README.md`, `docs/testing/k3d-gvisor.md` updated to document Kubernetes pod execution deployment and configuration.
- `docs/FEATURES.md`, `docs/HARNESSES.md`, `docs/MANUAL.md`, `runner/README.md`: gVisor security boundary clarified — gVisor sandboxes the agent process only, not the network or MCP bridge.
- Website updated to cover per-execution network isolation, runner resource reporting, and API token expiry.
- New `docs/plans/2026-07-28-001-feat-webhook-platform-plan.md` — plan for a unified webhook platform.

## 2026-07-26

### Added

- Event callbacks web UI page at `/event-callbacks` with ConnectRPC `EventCallbackService` for list, create, edit, and delete operations (previously only available via MCP tools). Sidebar Callbacks nav item added (#130).
- Structured task failure classification: `failure_category` column (timeout, harness_error, runner_lost, internal_error, user_cancelled, quota_exceeded, unknown) and `failure_message` column on tasks. Reaper sets failure_category on lease expiry; runner RPC maps error_category to failure_category. Web UI shows colored badges on task list and detail pages with a failure category filter dropdown. `task_status` and `list_tasks` MCP tools include the new fields (#98).
- Reaper garbage collection for expired session checkpoints and exports: `SessionArtifactTTL` config (default 24h, zero disables GC) schedules periodic deletion of on-disk artifacts for terminal agent sessions. New `ClearExpiredSessionCheckpoints`, `ClearExpiredUserPromptExports`, `ClearExpiredExecutionAttemptExports` queries for both MySQL and PostgreSQL (#127).
- PostgreSQL native full-text search using `to_tsvector` / `websearch_to_tsquery` for all four FTS search paths (tasks, sessions, audit log, artifacts), with GIN indexes created at bootstrap (#183).
- PostgreSQL schema parity test comparing bootstrap schema (`ApplySchema`) and Goose migrations via information_schema introspection (#184).

### Fixed

- Live token delta emission: interim task events now include token usage deltas, and `ListTasks` API populates token fields from batch-aggregated execution attempt data, fixing token visibility in the task list (#161).
- Web UI: task templates and repository picker added to the task list page.
- Web UI: effective task harness metadata shown on session and task detail pages.
- PostgreSQL: raw query placeholders properly bound in `searchTasksRaw` and `searchAgentSessionsRaw` FTS queries.
- Migration column ordering fixed for the `add_task_failure_category` migration.

### Documentation

- Website updated to cover task re-run and structured failure classification.

## 2026-07-25

### Added

- Server-side task environment variable validation: blocks dangerous prefixes (`CHETTER_`, `RUNNER_`, `MCP_AUTH`, `DATABASE_`, `GITHUB_APP_`, `ARCANE_`, `LLM_`) and reserved system names (`PATH`, `HOME`, `SHELL`, `LD_PRELOAD`, `LD_LIBRARY_PATH`) at submission time. Enforces limits on count (default 64), name length (256), and value length (4096). Configurable via `CHETTER_ENV_*` env vars. Rejected submissions emit a `task.validation_failed` audit event (#101).
- Task re-execution (re-run) support: `RerunTask` RPC endpoint, `chetter_rerun_task` MCP tool, and Re-run button on the task detail page for terminal tasks (done, error, cancelled). Creates a new task with the same prompt, model, image, env vars, and timeout as the source task. Emits a `task_rerun` audit event recording source and new task IDs. Available in the runner's OpenCode allowed tools and the `chetter-rerun` command template (#107).

### Documentation

- Connection resilience documented in `docs/MANUAL.md`: covers exponential backoff retry for transient errors and startup health checks.
- Website technical page updated to cover storage pruning, DB resilience, and env var validation in the Resilience section.

## 2026-07-24

### Added

- Configurable storage pruning: TTL-based pruning for `chetter_task_events`, `chetter_audit_log`, `chetter_task_artifacts`, and `chetter_agent_sessions`, configurable via `EVENTS_RETENTION_DAYS`, `AUDIT_RETENTION_DAYS`, and `ARTIFACT_RETENTION_DAYS`. All default to 0 (disabled). Deletes run in batches of 1000 to avoid long-running transactions (#112).
- Database connection resilience: transient error detection (connection refused, broken pipe, leader change) with exponential backoff retry (100ms → 5s, up to 3 retries) and ping-based startup health check with up to 60s of backoff (#103).
- TiDB migration tooling for the wowbagger deployment: `ops/tidb-bootstrap.sh`, `ops/tidb-cloud-migrate.sh`, and `ops/tidb-common.sh` with topology review before deployment.

### Fixed

- Runner: RPC harnesses now use context commands (`docker exec` with signal handling) instead of raw commands, enabling proper shutdown signaling for OpenCode, Claude Code, CodeWhale, and Codex.
- Runner: Claude Code, CodeWhale, and Codex harness MCP configs aligned — each now generates consistent `mcp.json` configurations inside the agent container.

### Documentation

- Retention pruning documented in `docs/FEATURES.md` and `docs/MANUAL.md` with env var reference and default-behavior notes.
- TiDB wowbagger migration tooling documented in `docs/TIDB-WOWBAGGER.md` (new), covering bootstrap, cloud migration, and common setup scripts.
- TiDB deployment prerequisite: topology review step added before bootstrap.
- Website updated: technical.html and how-it-works.html cover graceful shutdown, time-aware drain (`CHETTER_DRAIN_TIMEOUT_SEC`), webhook delivery queue with retry/backoff/dead-lettering, and auto-recovery opt-out (`DEFAULT_AUTO_RECOVERY`).

## 2026-07-23

### Added

- Webhook delivery queue: `chetter_webhook_deliveries` table persists deliveries with idempotency deduplication; background worker retries failed deliveries with exponential backoff (1s/5s/15s) and dead-letters after 3 attempts. New `chetter_list_webhook_deliveries` MCP tool lets operators inspect delivery status, attempts, and errors (#102).
- Reaper auto-recovery audit events: `task_recovered` audit events emitted when the reaper auto-reclaims an expired lease, recording runner ID, attempt number, and max attempts for operator traceability (#96).
- Execution attempts: new `chetter_execution_attempts` table, DB migrations, sqlc queries, and facade methods. Attempts are queued before claim, persisted with runtime state, and exposed via API and web UI as execution attempt history on task detail pages.
- Agent session lifecycle: new `lifecycle` column on agent sessions tracking session state transitions (created, started, completed, failed), persisted via DB migration and exposed in the API proto.
- Task event hierarchy: events attributed to execution attempts and user prompts via `execution_attempt_id` and `user_prompt_id` foreign keys, with DB migration and API exposure.
- Task reclaim history: `reclaim_count` and `last_reclaim_reason` columns on tasks, exposed via API and MCP tools.
- Fenced workspace pruning: workspace directories are now pruned per execution attempt rather than per task, preventing premature cleanup of active execution workspaces.
- Artifact attempt attribution: GitHub artifacts linked to execution attempts via `execution_attempt_id`, with DB migration, API exposure, and web UI display.
- Agent session lifecycle persisted: new `lifecycle` column on agent sessions tracking state transitions, with DB migration and API exposure.
- Session restart on cold reclaim: when a runner reclaims a task with a stale session, the session is restarted in a fresh container instead of failing.
- Database migrations run before server startup: new `chetter-migrate` binary and entrypoint script that applies pending Goose migrations before the MCP server starts.
- Backfill migration for legacy execution attempts: populates `execution_attempts` for tasks created before the attempts table existed.
- Trigger run relink after definition sync: migration that re-links trigger runs to their tasks after a definition sync, preserving trigger run history.

### Changed

- Default runner drain timeout reduced from 10m to 30s, configurable via `CHETTER_DRAIN_TIMEOUT_SEC`, aligning with Kubernetes `terminationGracePeriodSeconds` (#97).
- Task session model refactored: execution config moved from tasks to agent sessions, execution runtime state moved to execution attempts, task usage aggregated from execution attempts instead of task-level columns. Session runs renamed to user prompts. Agent sessions made task-owned. Remaining execution ownership columns dropped from tasks.
- Runner harness capabilities split: `ServeCommand`, `DockerConfigPath`, `CompletionAwareHarness` extracted into separate interfaces per harness, reducing per-harness boilerplate.
- Runner Docker serve setup shared across harnesses via `docker_args.go`, eliminating duplicated container argument construction.
- Agent environment policy isolated into `runner/internal/agentenv/` package with dedicated tests.
- Harness transport helpers (HTTP serve, readiness polling) shared via `runner/harness/transport/` package, reducing duplication across opencode, claude, codewhale, and codex harnesses.
- Task usage aggregated from execution attempts instead of task-level columns; old task-level usage columns dropped.
- Task session data sourced from the execution attempt hierarchy instead of task-level columns; old session runtime columns dropped.
- Runner MCP artifact tools (`chetter_create_issue`, `chetter_create_pr`, `chetter_issue_comment`, `chetter_pr_review`) removed from server-side MCP tool list — they remain available only through the runner bridge.

### Fixed

- Webhook delivery reliability: events now persisted with idempotency deduplication before processing, retried with exponential backoff (1s/5s/15s), and dead-lettered after 3 failed attempts. New `chetter_list_webhook_deliveries` MCP tool reports delivery status, attempts, and errors (#102).
- Webhook goroutine leak: `asyncCtx` replaced with `context.AfterFunc`, eliminating one idle goroutine per webhook event (#52). In-flight goroutines tracked via `sync.WaitGroup` and drained on shutdown with configurable deadline (#57).
- Server graceful shutdown: reaper and sync loops abort early on shutdown context via `ReaperStopCh()`; ordered shutdown sequence with second-signal force-exit and explicit DB close. Configurable via `CHETTER_SHUTDOWN_TIMEOUT` (#99).
- Runner SIGTERM handling: signal handler now sets the draining flag and sends an immediate draining heartbeat, then waits for in-flight tasks via the drain protocol with configurable timeout. Exits with code 1 if tasks were force-cancelled (#97).
- Runner drain: drain deadline derived from each task's remaining timeout (clamped by `CHETTER_DRAIN_TIMEOUT_SEC`) instead of a fixed deadline, preventing premature force-cancel of long-running tasks (#160). Resumable sessions are paused (workspace preserved) before force-cancel, enabling later resume by a fresh runner.
- Team authorization scopes: non-admin tokens now correctly scoped to their team's resources across all MCP tools and web API endpoints.
- Web UI task list: responsive updates restored on the task list page (`internal/service/task_list.go` + Svelte store refactor).
- Reaper auto-recovery: new `DEFAULT_AUTO_RECOVERY` config (default true) lets operators opt out of automatic retry. Auto-recovery skips tasks whose runner was deliberately drained, preventing silent re-queue during operator-initiated maintenance (#96).
- Expired leases now fail without auto-recovery when the runner's runner is in a draining/stopping state or auto-recovery is disabled (new `FailExpiredLeases` query).
- False completion lease expiry prevented: OpenCode harness events no longer prematurely clear task lease timestamps.
- Webhook deduplication and event bus lifecycle hardened: shutdown signal propagated through the event stream.
- Database migrations made portable: renumbered and adjusted MySQL/TiDB migrations to produce a clean chain from migration 001, fixing deployment issues on fresh databases.
- Trigger run history preserved after definition sync: new migration re-links trigger runs to their tasks when a definition sync changes task IDs.
- Legacy execution attempts backfilled: migration populates `execution_attempts` for tasks created before the attempts table existed.
- Runner service shutdown cleaned up: MCP server, proxy, and workspace manager now shut down cleanly without resource leaks.
- Runner task lifecycle shutdown hardened: heartbeat, progress watchdog, and process management handle shutdown more reliably.
- Runner superseded helpers removed: 241 lines of dead code from `runner_task.go` deleted.
- Claude Code and CodeWhale harness completion detection hardened: codex-serve-proxy and claude-serve-proxy handle edge cases in completion detection, with comprehensive test coverage.
- Migration chain made portable: MySQL migrations renumbered to produce a clean chain from migration 001, fixing deployment on fresh databases.
- MCP artifact tools (`chetter_create_issue`, `chetter_create_pr`, `chetter_issue_comment`, `chetter_pr_review`) removed from server-side MCP tool list — they remain available only through the runner bridge.
- Web UI: session resume modes clarified with helper text; agent and artifact navigation improved with clearer labels and links; agent detail page loading streamlined.
- Runner: execution IDs now included in terminal task reports, enabling correct execution attempt tracking downstream.
- Runner: terminal task reports now block synchronously with retries instead of fire-and-forget, preventing silent loss of final task status when the server is temporarily unreachable.
- Runner: lease renewed during task finalization via periodic heartbeats, preventing premature lease expiry while container cleanup, session export, and terminal reporting are in progress.
- Runner: OpenCode completion statuses normalized into a shared helper, fixing edge cases where non-standard status values were not recognized as completion.
- OpenCode harness: terminal assistant messages confirmed by polling session status before signaling harness completion, improving detection reliability.
- Task detail page: artifacts now loaded during task runs (not only after terminal status), showing created issues/PRs/comments while the task is still running.

### Documentation

- New `docs/plans/2026-07-23-002-ops-separate-chetter-database-plan.md` — plan for separating Chetter's database from the application database.
- New `docs/plans/2026-07-23-001-quality-hardening-plan.md` — quality hardening roadmap.
- Task session model refactor documentation closed: `docs/TASK_SESSION_MODEL_REFACTOR.md`, `docs/HARNESSES.md`, `docs/MANUAL.md`, `docs/PAUSED_SESSIONS.md`, `docs/PLAN.md` updated to reflect the new execution attempt and agent session architecture.
- Bundled Chetter skill (`SKILL.md`) updated to reflect current MCP tool set and workflow.
- Website how-it-works page now lists PostgreSQL alongside TiDB and MySQL as supported database backends.
- Agent credential forwarder plan simplified in `docs/plans/`.
- `docs/MANUAL.md`: graceful shutdown documentation for server and runner, including `CHETTER_SHUTDOWN_TIMEOUT` and `CHETTER_DRAIN_TIMEOUT_SEC` configuration (#99, #97).

## 2026-07-22

### Added

## 2026-07-21

### Added

- PostgreSQL runtime support as a first-class database backend alongside TiDB/MySQL, with dialect-aware query routing, dedicated schema migrations and sqlc queries (`db/postgres/`), and a native repository facade (`internal/data/`) that selects the correct generated repository at runtime.
- Managed agent Git identities with CRUD MCP tools (`chetter_create_git_identity`, `chetter_list_git_identities`, `chetter_update_git_identity`, `chetter_delete_git_identity`, `chetter_set_git_identity_default`), web UI admin page, and runner injection of the resolved identity into task containers (#180).
- Default Git identity fallback for agent-less tasks, settable via `chetter_set_git_identity_default` MCP tool (#182).

### Fixed

- Web UI: task timeline entries now sorted by timestamp descending with an index tiebreaker, fixing out-of-order display on the task detail page.
- Runner: interactive agent prompts (`AskUserQuestion`/`question`) denied in both OpenCode and Claude Code harnesses, preventing automated tasks from blocking on human input.
- Runner: Git clone credentials no longer written inside the workspace directory, keeping the workspace empty before clone.
- Server: auto-migrates the `git_identity_id` column on `chetter_tasks` for zero-downtime compatibility with existing deployments (#181).
- Runner: remote agent images from `ghcr.io/` are refreshed on every container start via `--pull=always`.
- Claude Code harness: JSONL session exports now handle content blocks (array format) in addition to plain text fields, producing complete markdown transcripts.
- Claude Code harness: session export finds JSONL files in both the project root and session subdirectories; progress watchdog nudges correctly without a nudge callback; stuck-harness detection no longer requires a nudge function.

### Documentation

- New `CHETTER.md` explaining Chetter agent workloads, execution model, and architecture.
- Bot identity setup guide added to `docs/MANUAL.md` (#186).

## 2026-07-18

### Documentation

- Homepage feature card uses full "Claude Code" harness name instead of "Claude" (#176).

## 2026-07-17

### Documentation

- Homepage and technical page updated to surface pause/resume sessions as a feature card and a numbered step in the architecture walkthrough, respectively (#174).
- Homepage provider kind terminology aligned across stat cards and feature cards to use the three canonical kinds (`openai_compatible`, `native`, `aws_bedrock`) with specific provider examples (#172, #173).

## 2026-07-14

### Added

- `chetter_usage_summary` MCP tool returning aggregated token usage and cost totals grouped by team, trigger (name/type), and repository, with time-window filtering (`since_hours`, `since`, `until`, default 30 days) and optional filters for team, trigger, and repo. Admin tokens see all teams; team-scoped tokens see only their data. Repository extraction handles both HTTPS and SSH git URL formats.

### Changed

- Whoami response no longer includes the `repos` field. A new `GET /api/v1/repos` endpoint (and `ListRepos` RPC in the AdminService proto) provides distinct repository names from `chetter_task_artifacts`. Web UI fetches repo options from the new endpoint instead of reading them from the Whoami response.

### Documentation

- Website updated to document task trigger provenance, aggregate cost/token summaries on the MCP tools page, and the `submission_source`, `trigger_name`, and `trigger_type` fields in the task envelope JSON.

## 2026-07-13

### Added

- `submission_source` column on tasks, stamped at all entry points (webhook, cron, API, MCP tools). Task origin (trigger name, trigger type, submission source) exposed in the Task proto and displayed in task list, task detail, and session views.

### Fixed

- Pi harness: always write pi-mcp-adapter extension path in settings, fixing extension loading inside the agent container
- Claude Code harness: export direct project sessions instead of returning "no session subdirectory" error
- Runner: progress watchdog detects and recovers stalled harness tasks that stop producing progress events while the server remains alive
- Webhook: allow PR review dispatch on bot-authored PRs

### Web UI

- Task origin indicators (trigger name, trigger type, submission source) shown in task list, task detail, and session views
- Raw events lazy-loaded behind a "Load raw events" button instead of always fetching, with simplified timeline rendering for improved performance
- Timeline deduplication improved and noise entries (empty summaries, single-word Pi fragments) filtered server-side

## 2026-07-12

### Added

- `ExtendTask` ConnectRPC endpoint for extending deadlines of pending or running tasks by an arbitrary number of seconds. Available via web API, with audit event tracking (`task_timeout_extended`).

### Fixed

- Reaper now recovers from panics per-cycle instead of dying permanently: a single bad cycle (DB error, nil pointer) no longer kills the reaper goroutine. Panicked cycles are logged with stack traces and reaping continues on the next tick. A new `lastReapAt` field in `/api/server-info` enables operators to detect a stalled reaper.

### Web UI

- Task detail page shows full task configuration in a "Task Configuration" card: git URL, git ref, skills, pause reason, session expiry, and environment variables. Metadata stat cards now include Provider, Variant, Harness, and Session Mode.

## 2026-07-11

### Added

- AWS Bedrock provider kind (`aws_bedrock`) with per-harness model overrides. Claude Sonnet 4 added to the default catalog with AwsProfile/AwsRegion fields.
- LiteLLM / custom OpenAI-compatible provider support through per-harness API transport mapping: provider harnesses gain `api` (`openai-completions | anthropic-messages`) and `auth_header` fields, enabling one logical provider to serve different wire protocols per harness.
- Pi harness generates `.pi/agent/models.json` for custom OpenAI-compatible providers at startup, registering provider baseUrl, api type, apiKey, and authHeader from the resolved catalog.
- Runner injects the catalog-selected `ProviderAPIKeyEnv` credential into the agent environment automatically, eliminating per-key hard-coding in runner environment allowlists. The managed-key is also filtered from task-supplied environment to prevent overrides.
- Codex harness fully wired across the stack: JSON schemas and trigger schemas accept `"codex"`, MCP tool descriptions list Codex, `definitions.isSupportedHarness` accepts it, task form harness dropdown includes Codex, runner `selectHarnessByName` returns the codex harness, and the agent base image builds the `codex-serve-proxy` binary.

### Documentation

- New `docs/PROVIDERS.md`: harness×provider support matrix, catalog kind→protocol mapping table, and LiteLLM configuration notes.
- `docs/CONFIGURATION.md`: LiteLLM provider configuration section, Bedrock YAML example per-harness API transport block, and Provider Kinds reference table.
- `docs/HARNESSES.md`: Codex section with pros, constraints, and when-to-use guidance; comparison table column; future candidates list updated.
- `docs/FEATURES.md`, `README.md`, `docs/MANUAL.md`, `AGENTS.md` updated to list Codex alongside existing harnesses.
- `website/technical.html` updated to list Codex alongside existing harnesses and describe provider kinds (`openai_compatible`, `native`, `aws_bedrock`) with per-harness API transport mapping.

## 2026-07-10

### Added

- Codex harness option (`harness: codex`) backed by a pinned Codex App Server bridge. It supports gVisor-isolated task containers, MCP configuration, streamed progress and token usage, turn interruption, markdown exports, and resumable Codex thread sessions. The agent base image now installs `@openai/codex@0.144.1` and `codex-serve-proxy`.

## 2026-07-06

### Added

- `CHETTER_IMAGE_REGISTRY` environment variable for resolving unqualified agent images via a configurable registry prefix. Images without a registry host (e.g. `my-agent:latest`) are prefixed automatically.
- Audit log now captures and displays the token identity (id/name) that performed each action, with a new Token column in the audit table UI showing the token name.

### Changed

- OpenCode harness switched from a blocking long-lived HTTP POST to `prompt_async` + status polling every 2s, preventing EOF/timeout failures on long-running tasks (45+ min) caused by gVisor connection drops, proxy timeouts, or network hiccups. Session export also uses the async path.
- Streaming events from OpenCode, Claude, and CodeWhale harnesses are now accumulated and batched between 3-second publish windows instead of being silently dropped, improving real-time progress reporting.
- Sidebar and audit log filters moved from client-side to server-side: exclude_types, team_ids, and repo filtering are now applied via raw SQL before LIMIT/OFFSET on the audit log, tasks, sessions, and triggers pages, fixing page-size shrinkage when toggling exclusions.
- Website redesigned with SVG logo, matching subpages (how-it-works, technical), and shared marketing CSS. Documentation content updated to emphasize gVisor sandboxing and reword UI section.
- Admin icon changed from gear to shield-check for distinctiveness from the Settings icon.

### Fixed

- OpenCode serve binds to `0.0.0.0` instead of `127.0.0.1`, fixing `connection reset by peer` errors under gVisor where Docker port-mapped traffic arrives on a non-loopback interface.
- `--hostname` Docker flag correctly placed before the image name in `docker run` arguments, fixing CodeWhale harness failure (the flag was treated as a command argument).
- Nav highlighting picks the most specific (longest) matching href, preventing `/admin` from highlighting on `/admin/audit` and similar sub-paths.
- Svelte 5 `$state` arrays no longer mutated in place on sessions and dashboard pages, fixing sorting UI bugs that broke reactivity.
- CodeWhale harness: duplicate `turn.completed` event handling removed.

### Web UI

- Audit log Token column showing which API token performed each action, with full token_id as title tooltip.
- FilterBar repo dropdown showing known repos populated from distinct `chetter_task_artifacts.repo` values, alongside the free-text repo input.
- Artifacts page simplified: Task ID and Repository filter inputs removed (redundant with global repo filtering in FilterBar).
- Sidebar active state correctly highlights current page in expanded state.

## 2026-07-03

### Added

- CodeWhale harness option (`execution.harness: codewhale`) with HTTP/SSE runtime API support (`app-server --http`, `/v1/threads`, `/v1/threads/{id}/turns`, SSE events until `turn.completed`, turn-interrupt cancellation). Bearer-token auth via `CODEWHALE_RUNTIME_TOKEN`, observed-turn markdown export fallback, and harness tests. `codewhale` npm package installed in base and minimal runner images.
- CodeWhale harness defaults in the model catalog (`deepseek/deepseek-chat`); default provider/model pair added to `Default()` catalog, CONFIGURATION.md, and example model-catalog.yaml.
- Optional `container_memory` execution config field (`execution.container_memory`) for Docker memory limits (`--memory` / `--memory-swap`) on both new task and resume containers.

### Changed

- Website public pages refreshed: new screenshot section with three annotated screenshots, Discord link in hero and nav, terminal-style MCP tool call replaced with dashboard screenshot, metrics bar removed. Web UI dashboard simplified (community hero section and social link cards removed, streamlined stat card layout).
- Website marketing copy simplified throughout: removed hardcoded tool counts (48 core + 5 Arcane), removed Arcane/Trivy references, replaced "One server. Docker Compose or Kubernetes. No message broker." with deploy-agnostic phrasing. Technical architecture page similarly updated.
- Runner dumps container logs (last 500 lines with timestamps) to `docker-container.log` in the workspace directory on transport error, preserving diagnostic data before the container is stopped or removed.
- Codex harness stub removed from harness selection (`selectHarnessByName`) and catalog resolution (`catalogHarnessName`); Codex remains listed only as a future candidate in documentation.
- All runner image variants now include `codewhale` alongside `opencode` and `claude-code`.

### Fixed

- Claude Code serve-proxy (`claude-serve-proxy`) now reads `CLAUDE_SERVE_PROXY_TOKEN` env var for auth (was generating its own random password); matches the token configured by the runner, fixing proxy authentication.
- Claude Code `handleSendPrompt` now blocks until the Claude process exits, accumulates text deltas, and surfaces non-zero exit errors. Returns `{"status": "completed", "summary": ...}` instead of immediately responding `{"status": "started"}`.
- Minimal runner image now includes the `claude-serve-proxy` binary (was missing), required for Claude Code harness operation.

### Documentation

- `docs/HARNESSES.md` updated: new CodeWhale section with rationale, pros/cons, and comparison table column. MiMo Code and Codex added to future candidates table.
- `docs/FEATURES.md`, `docs/MANUAL.md`, `docs/research/UNIVERSAL_HARNESS.md` updated to list codewhale alongside opencode/claude-code/pi.

## 2026-06-30

### Added

- New `transport_error` error category for opencode prompt transport failures (EOF, connection reset, broken pipe, server closed, connection refused). Both server-side (`runner_rpc.go`) and runner-side (`classifyErrorCategory`) recognize it alongside `timeout` as a recoverable prompt error, preserving workspace and marking agent sessions as recoverable for retry.

### Changed

- Runner publishes diagnostic events on transport failure: Docker container inspect (status, exit code, OOM), HTTP `/config` probe, and the last 200 lines of container logs, aiding operators in debugging network-level failures during prompt execution.

### Documentation

- Website architecture pages (`website/index.html`, `website/technical.html`) updated to reflect current feature set: 48 core MCP tools (was 40), Pi harness coverage, issue triggers, event callbacks, full-text search, token tracking, definitions/proposal tools, GitOps CI deployment, and session management entities.

## 2026-06-26

### Added

- Full-text search across all table pages (tasks, sessions, audit log, task artifacts) using TiDB FULLTEXT indexes with CONCAT/LIKE fallback. All four `chetter_list_*` MCP tools expose a `search` parameter for server-side text search.
- RecoverTask endpoint, `chetter_recover_task` MCP tool, web API handler, and Recover button on task detail page for recovering terminal tasks using the previous session export as recovery context. `chetter-recover` opencode command added.
- Extra files support (`extra_files` on TaskRequest/RPC): runners write specified files to the workspace before the agent starts, enabling recovery context and other workspace seeding.
- URL state persistence: audit log filter state, task/session filter state, and sidebar navigation preserve and restore URL search params across navigation and browser refresh.
- Audit UI filter improvements: added missing event type options (`trigger_run`, `trigger_updated`, `task_cancelled`, `github_artifact_created`) and source type options (`api`, `cron`, `rpc`).

### Changed

- All paginated table pages harmonized with page size select (10/25/50/100) replacing raw limit text inputs.
- Audit UI toggles now default to ON (previously OFF) so no event types are hidden by default; total count display simplified to show page number, event count, and limit.
- K8s runner deployment volume changed from emptyDir to hostPath, persisting runner identity (`.runner-id`) and workspace directory across pod restarts for pinned/resume task continuity.
- Webhook issue opened/reopened author write-access gate removed: triage triggers (read-only analysis) now fire regardless of the author's repository permissions.

### Fixed

- Webhook issue opened/reopened events no longer block bot-authored issues and triage triggers no longer require write access.

### Web UI

- Search input with magnifier icon positioned leftmost in the filter bar on all table pages (tasks, sessions, artifacts, audit).
- Page size selector (10/25/50/100) on all paginated tables instead of raw limit inputs.
- Filter state persists in URL search params on audit log, tasks, sessions, and artifacts pages.
- Sidebar remembers the last URL (with params) per page; clicking the currently active sidebar link preserves query params instead of navigating to the clean base path.
- Recover button on task detail page for terminal tasks (error, done, cancelled).
- Audit filter type dropdowns include all event types and source types; toggles default to ON.

## 2026-06-25

### Changed

- GitHub artifact signature simplified from multi-line format (Session, Run, Task, Agent, Model, Runner, Digest) to single-line `Task: [task_xxx](URL) | Agent: <name> | Model: <model>`. Runner, Session, and Run fields removed — they are navigable from the task URL. Agent field only shown when non-empty (named agent definitions).
- Runner persists auto-generated ID to `.runner-id` in the workspace root, reusing it across same-node restarts for pinned session continuity.

### Added

- Task detail page shows GitHub context card (repo, issue/PR link) extracted from environment variables.
- Runner fleet page auto-refreshes every 10 seconds for a live dashboard view.
- Audit log compact linkification: source/target columns link to their respective detail pages, repo column is a clickable GitHub link, detail text has expandable "Show more" for long entries.
- Triggers page redesigned with paginated table and a standalone detail page (`/triggers/[name]`) showing config, enable/disable toggle, run history with pagination and token totals, and run/delete actions.
- Sessions list now shows a run count per session via batch lookup on `ListSessions`.
- `scripts/cloc-chetter.sh`: utility for counting repo lines excluding generated code.
- Periodic workspace pruning every 10 minutes to prevent orphaned workspace accumulation.

### Fixed

- Webhook bot-authored event handling: bot's own issue/PR/comments now skip the author write-access gate silently instead of logging noisy `webhook_author_gate_denied` audit entries. Bot-comment filter in `handleIssueComment` moved before the author gate — was dead code because the gate ran first and rejected bots before the filter could apply.
- Runner `NO_PROXY` env var includes `0.0.0.0` so opencode self-requests to the MCP server bypass the HTTP proxy.
- Webhook-triggered tasks (issue/PR) now record trigger runs and update `last_run_at`; previously only cron triggers recorded them. Added unique index `idx_trigger_runs_dedup` with `INSERT IGNORE` for safe deduplication.
- Artifact deduplication: added unique index `idx_task_artifacts_dedup` on `(task_id, artifact_type, repo, number)` with `INSERT IGNORE` to prevent duplicates from MCP tool recording and webhook discovery.
- Duplicate rows are cleaned up before creating unique indexes on startup, preventing crash on pre-existing duplicate data.
- Audit log empty `sourceType`/`eventType` filter serialization sending `""` instead of omitting the field, causing incorrect query results.
- Runner resume: `docker stop` and session export only run on the error path (was unconditional); `Sending prompt` status published before goroutine starts for correct status timing.

### Web UI

- Audit log table: added Repo column between Event Type and Source, title tooltips on truncated source/target IDs and detail text, `webhook_author_gate_denied` added to the event type filter dropdown.
- Triggers page: accordion layout replaced with paginated table and inline enable/disable toggles; trigger names link to new detail page.
- Audit log source/target columns now link to `/tasks/[id]`, `/triggers/[name]`, `/sessions/[id]`, or GitHub URLs; detail text shows "Show more" expand/collapse for entries over 60 characters.
- Task detail: GitHub context card showing linked issue/PR from env vars.
- Live update mechanism refactored from imperative `auth.subscribe()` to Svelte 5 `$derived`/`$effect` runes.

## 2026-06-24

### Added

- Token consumption tracking: parsed from OpenCode SSE `message.part.updated` events, accumulated per step, forwarded to server via protobuf, stored in new `chetter_tasks` columns (`total_input_tokens`, `total_output_tokens`, `total_cache_read_tokens`, `total_cache_write_tokens`, `total_reasoning_tokens`, `cost_cents`). Web UI shows token breakdown on task detail, session totals, and per-run token column on trigger runs.
- Claude serve-proxy binary (`claude-serve-proxy`): Go HTTP server wrapping the Claude CLI behind the same serve API used by OpenCode (`/health`, `/config`, `/session`, `/event`, `/abort`, `/export`). Supports session resume via `--resume`. Built and installed in the runner base image via a multi-stage proxy-builder.
- Universal harness architecture: `ServeCommand(port)` and `DockerConfigPath(wsDir)` methods on the `Harness` interface replace hardcoded entrypoint/config path detection. `UNIVERSAL_HARNESS.md` documents the serve-proxy pattern for unifying all harnesses under HTTP serve mode with Docker/gVisor isolation.
- Git hash injection: runner and MCP images now receive `GITHUB_SHA` and `CHETTER_RUNNER_IMAGE_DIGEST` at build time via `GIT_HASH` build arg from CI. Heartbeats report the correct commit hash instead of `unknown`.
- Settings page with timezone, time format, and theme preferences stored in `localStorage`.
- Runner fleet page redesign with runner-specific stat cards (Runners, Active, Draining, Capacity, Busy, Idle). Cards are clickable to filter the runner list. Each row shows uptime and last heartbeat.
- Sidebar with SVG icons, mobile toggle, responsive wrapping, and configurable API proxy target.
- Audit log toggle filters to hide noisy event types (`definitions_synced`, `trigger_run`, `session_resumed`).
- Trigger type filter toggles (Cron / Issue / PR Review) on the triggers page.
- "Agent says:" markdown prefix for done timeline entries.
- Last heartbeat age display on running task detail pages.
- Per-trigger-type environment variable reference table in `docs/MANUAL.md`.

### Changed

- MCP JSON-RPC server replaced with the official Go MCP SDK (`github.com/mark3labs/mcp-go`). Unix-socket connections handled via `server.Connect()` + `IOTransport`. Tool handlers adapted via `adaptHandler` bridge. `ToolDefinitions` return typed `ToolDef` structs with `Name`/`Description`/`InputSchema`.
- Batch dispatch removed: `runBatchAgent`, `readBatchOutput`, `eventDetail`, and `SupportsServe` checks deleted. All harnesses now use serve mode or RPC mode.
- Signature format expanded: `Task: task_xxx | Agent: <name> | Model: <model>` with an optional `[View task](CHETTER_WEB_URL)` deep link when `CHETTER_WEB_URL` is configured. `stripExistingChetterSignature` regex updated for both old and new formats.
- Task deep link inlined into the `Task:` label in GitHub artifact signatures.
- MCP URL now uses the runner's own IP on the Docker network directly, avoiding gVisor hostname resolution issues. Dev containers (gVisor and non-gVisor) placed on the same Docker network as the runner.
- Trigger cards switched to vertical layout with expandable detail panel, always-visible action buttons.
- Task status filter persisted across page navigation via shared writable store instead of component-local state.
- Timeline shows all raw events with array-index tiebreaker for same-microsecond entries; 1200-char payload truncation removed.
- Heartbeat events filtered from the merged timeline display.
- Audit log event-type toggles flipped to ON = show (Syncs OFF by default, Triggers/Resumes ON).
- Theme toggle now syncs with the Settings page store.
- Session ID truncated to 11 chars in task detail view; git hash shown in both collapsed and expanded sidebar states.
- Redundant workspace MCP tools (`workspace_read_file`, `workspace_write_file`, `workspace_list_directory`) removed from the runner bridge — OpenCode has built-in equivalents.
- API proxy uses `^/api` regex instead of `/api` glob to correctly match `/api.v1.*` paths.
- Runner runner IP added to proxy allowlist so MCP traffic passes through gVisor.

### Fixed

- MCP bridge server: tools capability declaration added to initialize response, and `notifications/initialized` handled silently. Without the capabilities declaration, MCP clients skipped `tools/list` discovery.
- Abort OpenCode session before `docker stop` on task timeout via new `AbortSession` harness method. Prevents corrupted opencode.db state on resume. Claude and Pi get no-op stubs.
- MCP tool permissions added to generated opencode config (`mcp__runner-bridge__*`) — deny-by-default was silently blocking agents from calling `chetter_create_issue`, `chetter_create_pr`, `chetter_issue_comment`, `chetter_pr_review`.
- `--pure` flag removed from opencode serve arguments, restoring MCP bridge loading (`mcp-bridge`) and all 4 GitHub tools for agent discovery.
- MCP URL: fall back to `dockerGatewayIP` when the runner is not on the gVisor network (`hostIP()` returns empty).
- MCP listener forced to `tcp4` to avoid gVisor containers being unable to reach IPv6 listeners.
- MCP port extraction uses `net.SplitHostPort` for IPv6-safe parsing of addresses like `[::]:39633`.
- Git hash injected into MCP image via `--build-arg GIT_HASH` in the Dockerfile (was using plain `go build` without ldflags).
- Session export captured on timeout for Pi/RPC and batch agents (previously only on successful completion).
- Pi/RPC timeout abort reads session transcript with `get_messages` before tearing down.
- Batch mode passes accumulated stdout as `sessionExport` on all terminal statuses, not only success.
- Code review findings: `StartPackageDB` returns nil when TiDB unavailable (guarded `TestMain`), periodic `cleanupHeartbeatSeen` prevents unbounded memory growth, request context threaded through auth interceptor instead of `context.Background()`, `parseTime` errors logged at debug level, unused imports and dead guard lines removed from `streaming.go`.
- Duplicate timeline keys fixed by using `entry.index` in `progressKey`.
- Progress entry timestamps use `RFC3339Nano` to avoid duplicate keys.
- Impossible nil check (`def.InputSchema` is `map[string]any`, never nil) removed — fixes `SA4023` lint.
- MCP command format in opencode config changed to array format (`["mcp-bridge", "/socket"]`) — the old string-with-`args` format was silently stripped by OpenCode.
- Settings theme sync: `toggleTheme()` now writes `chetter-settings` so the Settings page stays in sync with the sidebar toggle.
- Audit log toggles and trigger page layout: closed unclosed HTML, fixed per-trigger enable toggle to match gray/small flowbite style.
- Session ID sidebar: git hash only shown in expanded state (collapsed sidebar is 64px with no room).
- Task view: session ID truncated with `truncate+shrink-0` so the Resumable badge fits.

### Documentation

- `docs/UNIVERSAL_HARNESS.md` — describes the serve-proxy pattern for unifying all harnesses under HTTP serve mode with Docker/gVisor isolation.
- `docs/HARNESSES.md` — updated for new `ServeCommand`/`DockerConfigPath` interface, batch mode references removed, Claude section updated to reflect serve-proxy execution model.
- `docs/MANUAL.md` — per-trigger-type environment variable reference table added.

## 2026-06-23

### Added

- CatalogService ConnectRPC endpoint exposing the active model catalog. Web UI task submit form now shows model, provider, and harness dropdowns populated from the catalog.
- Audit events for all manual API actions: task submission, session resume, task cancellation, queue clear, trigger create/update, token create/delete, and model catalog sync, recorded to `chetter_audit_log`.
- Session run chain display on the task detail page, showing the sequence of runs within a multi-run session.
- Runner GitHub MCP tools (`chetter_create_issue`, `chetter_issue_comment`, `chetter_create_pr`, `chetter_pr_review`) exposed as local MCP tools routed via ConnectRPC to the server, so runners need no GitHub token.
- TiDB quota exhaustion detection: the server pings the database on each reaper cycle and sets an `atomic.Bool` flag; the web UI displays a banner when the database is in a quota-exhausted state and clears it when restored.
- Pause reason displayed in the trigger detail panel.
- Session mode, pause reason, and TTL hours fields on the manual task submit form.
- `prompt` field added to `SessionRunRecord` for tracking the resume prompt text.
- `recoverable` session status alongside the rename of `paused_waiting_review` to `paused`.

### Changed

- `paused_waiting_review` agent session status renamed to `paused` (migration 018). A new `recoverable` boolean status distinguishes sessions that can be resumed.
- Skills moved from `tools/skills/` into the chetter-config definitions repository. `ScanDefinitions` now recursively walks skill directories to capture all files (SKILL.md, references/, scripts/).
- `gh` wrapper now also blocks `gh api` (the generic passthrough previously bypassed subcommand-level checks).
- Claude Code harness: fixed Claude Code npm install path, added Synthetic provider environment variable mapping (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_DEFAULT_*_MODEL`), added unit tests and harness support matrix documentation.
- `make check` runs `check-root`, `web-check`, and `runner-check` in parallel with `make -j3`.
- CI pipeline speed improvements: `.dockerignore` excludes unnecessary files, Dockerfile layer ordering optimized, runner variant images no longer force-reinstall `@anthropic-ai/claude-code` on every build, image build script removes `--pull` flag.
- `AGENTS.md` updated with Flowbite component usage conventions (Card, Input, Button, Badge, Table, Modal, Toast, etc.) and a checklist for verifying no raw HTML elements remain.

### Fixed

- Reaper now syncs session runs and agent sessions when tasks are marked terminal, preventing orphaned session runs from remaining in a running state indefinitely.
- Orphaned session runs are reverted to `pending` when a task's lease is reclaimed, so they can be retried on the next claim.
- Resume prompt now sent with role `user` to the OpenCode session (was sent as `assistant`), fixing the conversation turn order.
- Web UI lock caused by Svelte 5 `$effect` reactivity: replaced `$effect`-based store subscriptions with explicit `subscribe`/`unsubscribe` in task detail and timeline stores.
- Flowbite Card padding: replaced invalid `contentClass` prop with `class` across all card usages.
- Confirm dialog "Run" button works on subsequent clicks (not only the first).
- Task metadata is re-fetched when the event stream reports a terminal status, ensuring the UI reflects the final state immediately.
- Trigger page: closed unclosed HTML elements in the expanded-content section and conditional block.
- Create trigger form card width fixed with `max-w-none`.
- Pause reason alert only rendered when the session is actually paused.
- Various web UI polish: artifacts filter bar alignment, stat card padding, session link navigation, merged timeline view, panel spacing, submit form textarea width, trigger card width, running task timeline controls.

### Documentation

- `docs/MANUAL.md` updated: `gh` wrapper now documents `gh api` blocking; Claude Code harness support matrix table added with per-harness capability comparison (execution model, config generation, streaming, resume, session export).
- `docs/FEATURES.md` updated with the `gh api` block.

## 2026-06-22

### Added

- Definition registry: Git-backed definitions materialized into `definition_sources`, `definitions`, and `definition_sync_runs` DB tables with SHA-256 content hashing, source commit tracking, and sync-run history. Periodic auto-sync every 5 minutes. New MCP tools: `chetter_list_definition_sources`, `chetter_get_definition_source`, `chetter_sync_definition_source`, `chetter_list_definitions`, `chetter_get_definition`.
- Definition change proposal tooling: MCP tools `chetter_create_definition_proposal`, `chetter_list_definition_proposals`, `chetter_get_definition_proposal` create GitHub PRs proposing definition file changes. `chetter_definition_change_proposals` DB table tracks proposals with PR URLs and live status from GitHub.
- Resumable agent sessions with gVisor checkpoint/restore: `session_mode: resumable` creates gVisor checkpoints preserving the task container for later resume. `pull_request_review` and `pull_request_review_comment` webhook events can resume paused sessions. `Session:` and `Run:` footer metadata added to GitHub artifact signatures. Session mode, pause reason, and TTL hours propagated through trigger config into review/issue submit paths.
- Web UI: pagination (page selector, prev/next, configurable page size) on task list, sessions, schedule runs, audit log, and artifacts tables. Clickable column header sorting on all tables. Expand/collapse for raw event payloads in task detail view. Human-readable timeline descriptions. Task duration display with live timer for running tasks. Session export viewer modal rendering markdown transcript inline.
- Weekly task improver trigger (`chetter-weekly-task-improver`) and agent definition that analyzes task outcomes, session exports, and definition files to propose evidence-backed improvements via PRs.
- `match_labels` field on issue triggers for filtering GitHub label events.
- `GITHUB_TOKEN`/`CHETTER_GITHUB_TOKEN` env-based auth embedded in definitions repo clone URL for private repo access.

### Changed

- Model catalog resolution moved server-side: the runner RPC now resolves harness-specific provider/model at claim time and passes resolved provider name, base URL, and API key env var in the task proto. Runner no longer imports or parses `pkg/modelcatalog` YAML. `COPY pkg/` removed from runner Dockerfile.
- MCP server base image switched from `gcr.io/distroless/static-debian12:nonroot` to `debian:bookworm-slim` with git and openssh-client for definitions repository git clone/pull operations.
- `GITHUB_TOKEN` environment variable mapped to MCP server container for definitions repo authentication.

### Fixed

- Checkpoint restore uses Docker HTTP API v1.43 directly via Unix socket, bypassing a `docker start --checkpoint` containerd content store bug (`content sha256 already exists`).
- Runner recreates the kernel-level network namespace handle before checkpoint restore by reconnecting paused gVisor containers to their Docker network bridge.
- Checkpoints stored in workspace directory instead of a dedicated checkpoint path; netns path cleared after restore.
- `sessionRun` passed to `githubToolSignature` in definition proposal tools so GitHub artifact tracking functions correctly.

### Documentation

- Milestone 1 documentation pass: `PLAN.md` (comprehensive roadmap), `README.md` (docs index), `FEATURES.md` (rewritten as current-state reference replacing the old complete list), `MANUAL.md` (env and tool fixes), `PAUSED_SESSIONS.md` (resumable session model updated), `GVISOR.md`, `REVIEWER.md`, `SNAPSHOTS.md`, `TRIGGERS_PROPOSAL.md` updated.
- CI/CD section added to `AGENTS.md` covering the three-job workflow (check, detect-changes, arcane-build-deploy).
- Design principles section added to `README.md`.
- Research documents (`DAYTONA.md`, `GVISOR.md`, `OPENHANDS.md`) moved to `docs/research/` subdirectory.
- Website and technical architecture page updated for new MCP tools, web UI, model catalog, definition registry, and session management features.

## 2026-06-21

### Added

- GitHub MCP tools: `chetter_create_issue`, `chetter_issue_comment`, `chetter_create_pr`, and `chetter_pr_review` create GitHub artifacts with server-side signature footer, audit log entries, and artifact tracking in `chetter_task_artifacts`. Existing footers stripped to avoid duplication.
- Data-driven model catalog: YAML-based `pkg/modelcatalog` replaces hardcoded provider config (`addDeepSeekProvider`, `addOpenCodeProvider`, `addSyntheticProvider`). Catalog can be sourced from a Git definitions repo via `DEFINITIONS_REPO` env var with periodic auto-sync every 5 minutes, or loaded from a local file. Built-in default catalog includes Synthetic (GLM-5.2), OpenCode Zen (deepseek-v4-flash-free), DeepSeek, Z.ai, and Anthropic providers with per-harness overrides. New MCP tools: `chetter_sync_definitions` (admin), `chetter_list_model_catalogs` (admin), `chetter_get_model_catalog` (any authenticated user).
- Trigger run history and artifact browser in the web UI: triggers page now shows recent 25 schedule runs per trigger with links to task detail pages; new admin/artifacts page lists task-created GitHub artifacts with filtering by task ID, repo, and artifact type.
- Task attribution indexing: DB index on `trigger_name`/`trigger_type` columns for efficient trigger-scoped task queries in MCP tools and web UI.
- `CONFIG_IN_GIT.md` documentation describing the definitions repo workflow (model catalog, agents, triggers synced from Git).
- `GVISOR.md` research document covering 12 gVisor feature categories for production deployment considerations.
- `WEB_ADDR` environment variable to configure the web UI/ConnectRPC API listen address (default `:8090`); K8s manifests updated to expose both MCP (8080) and web (8090) ports.

### Changed

- Runner `gh` wrapper: `/usr/local/bin/gh` now blocks write subcommands (`issue create`, `issue comment`, `pr create`, `pr review`, `pr comment`) and directs agents to use Chetter MCP tools instead. The real `gh` binary is at `/usr/local/bin/gh-real`. Set `CHETTER_ALLOW_GH_WRITES=1` to bypass for manual debugging.
- All trigger prompts migrated from raw `gh` CLI commands to Chetter MCP tools (`chetter_create_pr`, `chetter_create_issue`, `chetter_issue_comment`, `chetter_pr_review`). Manual footer instructions removed since tools append signatures server-side.
- Agent and trigger model configs standardized: `model: provider/id` split into separate `provider` and `model` front-matter fields. Provider references migrated from `opencode-go` to `opencode`. Models defaulted to `opencode/deepseek-v4-flash-free`.
- Chetter web UI listen port changed to `:18090` to avoid conflict with external services.
- CI workflow (`chetter.yml`) now includes web build and check steps with Node.js 24 setup and npm dependency caching. `make check` target includes `web-check`.
- Web UI auth hardened: admin login link added, token stores validated, streaming endpoint auth strengthened.

### Fixed

- Server-side prompt placeholder expansion: `$CHETTER_*` and `${CHETTER_*}` variable references in trigger prompts are replaced server-side at submission time instead of passing literal references to agents.
- Entrypoint digest fallback: `:-` substitution replaces `:=` so an empty-string env var from compose.yaml defaults to `"unknown"` instead of remaining empty.
- `CHETTER_TASK_ID` environment variable now injected into Docker resume and batch agent execution paths, making the task ID available in all execution modes.
- Stable MCP trigger response type: `TriggerToolRecord` decouples the MCP JSON schema from `store.ScheduleRecord`, preventing future DB schema changes (additional columns) from causing `"must NOT have additional properties"` errors in MCP clients.
- Runner Dockerfile copies `pkg/` directory for modelcatalog dependency; compose.yaml DNS configuration fixed for TiDB hostname resolution.

### Documentation

- `README.md` updated: web UI and ConnectRPC API documented in quick start and K8s deployment sections; `HTTP_ADDR`, `WEB_ADDR`, `DEFAULT_AGENT_IMAGE` env vars documented; web port 8090 exposed in K8s service manifest example.
- `docs/MODEL_CATALOG.md` updated: describes Git-sourced model catalog with `DEFINITIONS_REPO` auto-sync, viewing via `chetter_get_model_catalog`, and harness-specific overrides.
- `docs/CONFIG_IN_GIT.md` expanded: definitions repo workflow, periodic sync, and manual sync via `chetter_sync_definitions`.
- `docs/FEATURES.md` updated: GLM-5.2 model reference, opencode provider name.
- `.opencode/skill/chetter/SKILL.md` updated: trigger attribution docs, trigger-scoped task querying, `trigger_name` filter on `chetter_list_tasks`.
- `.env.example` updated: `HTTP_ADDR` and `WEB_ADDR` documented.
- `deploy/k8s/mcp-deployment.yaml` and `mcp-service.yaml`: web port 8090 added to service and deployment specs.

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

[0.1.0]: https://github.com/flatout-works/chetter/releases/tag/v0.1.0
