# Chetter Feature Reference

Status: **Current shipped capabilities**

This is the current-state feature reference. For setup instructions, use [MANUAL.md](MANUAL.md). For roadmap and consolidation work, use [PLAN.md](PLAN.md).

## Task Management

Chetter submits development tasks to a fleet of runners. Each task can clone a repository, start an agent harness, stream events, store a transcript, and report terminal status.

Supported task inputs include:

- `prompt`: natural-language task instructions.
- `git_url` and `git_ref`: optional repository and ref to clone.
- `agent_image`: runner image override, falling back to `DEFAULT_AGENT_IMAGE`.
- `agent`: agent definition name.
- `harness`: agent CLI harness, currently `opencode`, `claude-code`, or `pi`.
- `provider_id`, `model_id`, and `variant_id`: model selection overrides.
- `skills`: skill names or hints passed to the runner.
- `env`: non-secret environment variables.
- `timeout_sec`: per-task timeout.
- `session_mode`, `pause_reason`, and `ttl_hours`: resumable session controls.

Task monitoring includes:

- Status via `chetter_task_status` and `chetter_list_tasks`.
- Full event history via `chetter_task_events`.
- Distilled progress via `chetter_task_progress`.
- Latest activity via `chetter_task_latest_event`.
- Markdown transcript via `chetter_task_export`.
- Cancellation via `chetter_cancel_task` or admin queue clearing via `chetter_clear_queue`.

## Agent Sessions

Chetter now tracks long-lived agent sessions separately from individual task runs.

- Every session has one or more `session_runs`.
- `session_mode: resumable` creates a gVisor checkpoint-backed session.
- Resumable sessions are pinned to the runner that created the checkpoint.
- `chetter_list_agent_sessions` lists sessions.
- `chetter_agent_session_status` returns a session with its runs.
- `chetter_resume_agent_session` queues a follow-up run for a paused session.

See [PAUSED_SESSIONS.md](PAUSED_SESSIONS.md) for the current model and remaining work.

## Triggers And Schedules

Chetter uses a unified trigger system. Cron schedules are `trigger_type: cron`; PR review automations are `trigger_type: pr_review`.

Cron triggers:

- Use standard five-field cron expressions or descriptors like `@hourly`.
- Track `next_run_at` and run history in `chetter_schedule_runs`.
- Can be run manually with `chetter_run_trigger`.
- Are team-scoped when created with a team token.

PR review triggers:

- Watch one GitHub repository per trigger.
- Fire for labeled PRs, fork PRs from users with sufficient access checks, and `/chetter-review` comments by users with write access.
- Use either a custom prompt or the built-in review template.
- Can have multiple triggers per repo for different reviewer agents.

Trigger tools:

- `chetter_create_trigger`
- `chetter_update_trigger`
- `chetter_list_triggers`
- `chetter_delete_trigger`
- `chetter_run_trigger`
- `chetter_list_schedule_runs`

## GitHub Artifacts

Chetter exposes server-side GitHub tools so agents do not need direct `gh` write access for common artifact creation.

- `chetter_create_issue`
- `chetter_issue_comment`
- `chetter_create_pr`
- `chetter_pr_review`

These tools append a canonical Chetter signature footer, strip duplicate existing footers, write audit events, and record rows in `chetter_task_artifacts`. The runner image wraps `gh` and blocks write commands (`gh api`, `gh issue create`, `gh issue comment`, `gh pr create`, `gh pr comment`, `gh pr review`) unless `CHETTER_ALLOW_GH_WRITES=1` is set for manual debugging.

Artifact browsing is available with `chetter_list_task_artifacts` and in the web UI.

## Runner Fleet

Runners register through ConnectRPC, poll for tasks, and heartbeat while work is in progress.

- Task claiming uses `SELECT ... FOR UPDATE SKIP LOCKED`.
- Claims have renewable leases.
- The reaper reclaims expired leases and marks stale tasks terminal when retries are exhausted.
- `chetter_runner_health` reports fleet-wide status and optional per-task heartbeat age.
- `chetter_drain_runner` asks a runner to stop claiming new work, finish in-flight tasks, and exit for rollout.

Runner RPC uses a dedicated token (`CHETTER_RUNNER_RPC_TOKEN` on the server side). Compose currently passes it to runner containers through `CHETTER_RUNNER_AUTH_TOKEN` for compatibility with runner config fallback order.

## Agent Harnesses

The runner drives agent CLIs through harness implementations.

| Harness | Execution model | Notes |
|---|---|---|
| `opencode` | HTTP serve mode | Default and richest integration. Supports event streaming, session export, and per-task Docker/gVisor containers. |
| `claude-code` | Batch subprocess | Anthropic Claude Code CLI. Simpler integration, no persistent harness session export. |
| `pi` | JSONL RPC subprocess | Provider-rich harness with streaming control, steering, abort, and session messages. |

See [HARNESSES.md](HARNESSES.md) for details and guidance on adding new harnesses.

## Execution Environments

Chetter currently supports:

- Docker task containers.
- Optional gVisor isolation with `USE_GVISOR=true`.
- Local runner execution for development.

gVisor uses Docker's `runsc` runtime to give agent containers stronger isolation. The runner can also create Docker checkpoints for resumable sessions when gVisor is enabled.

Networking uses Docker bridge networks plus optional proxy and DNS filtering. Legacy Kata/containerd execution and host network namespace management have been removed.

## Runner-Local MCP Tools

The runner exposes only workspace file I/O tools to agents:

| Tool | Description |
|---|---|
| `workspace_read_file` | Read a file from the workspace. |
| `workspace_write_file` | Write or overwrite a file in the workspace. |
| `workspace_list_directory` | List workspace files and directories. |

Host-side command and deployment tools such as `workspace_bash`, `git_*`, `fetch_url`, and `deploy_*` are intentionally not exposed by the runner MCP bridge. Agents can run commands inside their own sandbox through the harness environment.

## Auth And Teams

Chetter supports:

- Admin token auth for global access.
- Team tokens stored hashed in TiDB.
- Automatic `team_id` stamping for tasks, triggers, schedule runs, and sessions.
- Team-scoped reads for non-admin tokens.

The server binary reads the admin token from `MCP_AUTH_TOKEN`. Docker Compose and Kubernetes examples use `CHETTER_MCP_AUTH_TOKEN` externally and map it to `MCP_AUTH_TOKEN` inside the server container.

Admin/team tools:

- `chetter_create_token`
- `chetter_list_tokens`
- `chetter_delete_token`
- `chetter_create_team`
- `chetter_list_teams`
- `chetter_delete_team`
- `chetter_list_users`

## Configuration From Git

Chetter can sync definitions from a Git repository configured by `DEFINITIONS_REPO` and `DEFINITIONS_BRANCH`.

Implemented today:

- Git-backed model catalog loading.
- Git-backed agents, skills, triggers, task templates, and MCP profiles.
- Five-minute auto-sync.
- Manual sync via `chetter_sync_definitions`.
- Read access via `chetter_get_model_catalog`, `chetter_list_definitions`, and `chetter_get_definition`.
- Task and trigger `mcp_profiles` attachment. Selected profiles are rendered into harness-native MCP config before agent startup.

Planned next:

- Scoped MCP tokens or proxy enforcement for untrusted/multi-tenant MCP profile use.
- Immutable definition hashes recorded on task/session runs.

See [CONFIG_IN_GIT.md](CONFIG_IN_GIT.md) and [MODEL_CATALOG.md](MODEL_CATALOG.md).

## Web UI And API

Chetter has two listen addresses:

- `HTTP_ADDR`: MCP server, default `:8080`.
- `WEB_ADDR`: web UI and ConnectRPC API, default `:8090`.

The Compose deployment maps these to host ports `18088` and `18090` respectively.

The web UI includes task views, trigger run history, and an admin artifact browser.

## Arcane Vulnerability Scanning

If `ARCANE_SERVER_URL` and `ARCANE_API_KEY` are set, Chetter registers Arcane/Trivy tools:

- `chetter_arcane_scanner_status`
- `chetter_arcane_environment_summary`
- `chetter_arcane_list_images`
- `chetter_arcane_image_summary`
- `chetter_arcane_list_vulnerabilities`

Severity filters: `CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, `UNKNOWN`.

## Audit And Observability

Chetter records server-side audit events for webhook receipts, trigger matches, task submissions, GitHub artifact creation, session resume, task cancellation, queue clear, trigger create/update, token create/delete, and model catalog sync.

Tools:

- `chetter_list_audit_events`
- `chetter_list_task_artifacts`
- `chetter_runner_health`

Task events are kept separately in `chetter_task_events` and are exposed through task event/progress/latest tools.

## MCP Tool Reference

Unconditional tools:

| Group | Tools |
|---|---|
| Tasks | `chetter_submit_task`, `chetter_task_status`, `chetter_list_tasks`, `chetter_cancel_task`, `chetter_clear_queue`, `chetter_task_events`, `chetter_task_progress`, `chetter_task_latest_event`, `chetter_task_export` |
| Sessions | `chetter_list_agent_sessions`, `chetter_agent_session_status`, `chetter_resume_agent_session` |
| Triggers | `chetter_create_trigger`, `chetter_update_trigger`, `chetter_list_triggers`, `chetter_delete_trigger`, `chetter_run_trigger`, `chetter_list_schedule_runs` |
| Runner fleet | `chetter_runner_health`, `chetter_drain_runner` |
| GitHub artifacts | `chetter_create_issue`, `chetter_issue_comment`, `chetter_create_pr`, `chetter_pr_review`, `chetter_list_task_artifacts` |
| Teams and tokens | `chetter_create_token`, `chetter_list_tokens`, `chetter_delete_token`, `chetter_create_team`, `chetter_list_teams`, `chetter_delete_team`, `chetter_list_users` |
| Definitions and catalog | `chetter_get_model_catalog`, `chetter_sync_definitions` |
| Audit | `chetter_list_audit_events` |

Conditional tools:

| Condition | Tools |
|---|---|
| Arcane configured | `chetter_arcane_scanner_status`, `chetter_arcane_environment_summary`, `chetter_arcane_list_images`, `chetter_arcane_image_summary`, `chetter_arcane_list_vulnerabilities` |

## Environment Reference

Server:

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `:8080` | MCP listen address. |
| `WEB_ADDR` | `:8090` | Web UI and ConnectRPC API listen address. |
| `MCP_AUTH_TOKEN` | required | Server admin bearer token. Compose maps external `CHETTER_MCP_AUTH_TOKEN` to this. |
| `CHETTER_RUNNER_RPC_TOKEN` | required | Dedicated runner ConnectRPC token. |
| `DATABASE_DSN` | required by binary | TiDB DSN. Compose local override can provide bundled TiDB. |
| `DEFAULT_AGENT_IMAGE` | `ghcr.io/flatout-works/chetter-runner:latest` | Default runner image for submitted tasks. |
| `DEFAULT_TASK_TIMEOUT_SEC` | `600` | Default task timeout. |
| `DEFINITIONS_REPO` | empty | Optional Git definitions repository. |
| `DEFINITIONS_BRANCH` | `main` | Definitions repo branch. |
| `ARCANE_SERVER_URL` | empty | Optional Arcane server URL. |
| `ARCANE_API_KEY` | empty | Optional Arcane API key. |
| `GITHUB_APP_ID` | `0` | GitHub App ID. |
| `GITHUB_APP_PRIVATE_KEY_B64` | empty | GitHub App private key, base64 PEM. |
| `GITHUB_INSTALLATION_ID` | `0` | GitHub App installation ID. |
| `GITHUB_WEBHOOK_SECRET` | empty | GitHub webhook HMAC secret. |
| `GITHUB_WEBHOOK_DISABLED` | `false` | Webhook kill switch. |

Runner and agent env:

| Variable | Purpose |
|---|---|
| `CHETTER_SERVER_URL` | Server URL used by the runner. |
| `CHETTER_RUNNER_AUTH_TOKEN` | Runner config fallback for the RPC token; Compose fills it from `CHETTER_RUNNER_RPC_TOKEN`. |
| `CHETTER_MCP_AUTH_TOKEN` | Admin token that explicit trusted MCP profiles may reference with `${env:CHETTER_MCP_AUTH_TOKEN}`. It is not mounted into every task by default. |
| `CHETTER_MCP_URL` | Optional deployment value for explicit trusted MCP profile definitions. It is not injected into agents by default. |
| `USE_GVISOR` | Enables Docker `runsc` runtime and checkpoint support when `true`. |
| `CHETTER_PROXY_ALLOWED_DOMAINS` | Optional HTTP/HTTPS egress allowlist. |
| `CHETTER_PROXY_BLOCKED_DOMAINS` | Optional HTTP/HTTPS egress blocklist. |
| `CHETTER_DNS_BLOCKED_DOMAINS` | Optional DNS blocklist. |
| `SYNTHETIC_API_KEY`, `DEEPSEEK_API_KEY`, `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY` | Provider credentials forwarded to task containers when configured. Host `GITHUB_TOKEN` / `GH_TOKEN` values are not forwarded; GitHub task auth is injected per task when authorized. |
| `MEM9_API_KEY`, `MEM9_API_URL`, `MEM9_DEBUG`, `MEM9_HOME` | Optional Mem9 integration. |
