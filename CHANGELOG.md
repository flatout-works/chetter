# Changelog

All notable changes to this project will be documented in this file.

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

## 2026-07-02

### Web UI

- Landing page redesigned with screenshots, GitHub/Discord community links, and dark gradient background. Login form replaced with a hero section showing key features (harnesses, runtime, workflow). Server info polling only starts after authentication.
- Table page layout unified across tasks, sessions, audit, and artifacts: title on its own row, filter bar below with search aligned left. Hand-rolled search inputs replaced with the Flowbite-Svelte `Search` component (built-in magnifier icon, proper dark mode styling). Search field widened from `!w-36` to `!w-72`. Action buttons moved to the title row; audit toggles moved to a separate row below the filter bar.
- npm audit vulnerability fixed: `cookie` dependency overridden to 0.7.2 across the web dependency tree.

### Documentation

- `README.md` rewritten with expanded design principles section and improved repository layout table. `Golang` → `Go`, added `cmd/`, `proto/`, and `web/` directory descriptions.
- Docs structure consolidated: `CONFIG_IN_GIT.md` and `MODEL_CATALOG.md` merged into `CONFIGURATION.md`. `FEATURES.md` and `MANUAL.md` trimmed by replacing inline tables with cross-references. Stale docs (`REVIEWER.md`, `UNIVERSAL_HARNESS.md`, `SNAPSHOTS.md`) moved to `docs/research/`. All cross-references and index files updated.
- `web/README.md` expanded with detailed tech stack rundown.
- `docs/EKS.md` added: EKS installation guide with taints/tolerations, GPU node groups, and IRSA for ECR access.
- `docs/K3s.md` expanded into a full validation guide with gVisor setup, network policy, and debugging instructions.
- `docs/testing/k3s-chetter.md` added: step-by-step k3s cluster setup with gVisor validation.
- `docs/PLAN.md` updated with execution backend separation plan and runtime injection moved to P2.
- Technical presentation (`docs/presentation/index.html`) reworked with expanded content.
- Website (`website/index.html`, `website/technical.html`) updated to reflect transport error diagnostics and session recovery documentation.

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
