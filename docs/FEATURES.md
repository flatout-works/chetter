# Chetter Feature Reference

Complete list of everything Chetter can do today.

---

## Task Management

### Submit Tasks

Submit development tasks to a fleet of runner machines. Each task runs an AI coding agent (OpenCode or Claude Code) in an isolated container with a cloned repository.

- **Prompt-based**: Describe what you want done in natural language.
- **Git integration**: Clone any repo (`git_url`) at a specific branch/tag/commit (`git_ref`) before the agent starts.
- **Agent selection**: Choose `opencode` (default) or `claude-code` harness via the `agent` field.
- **Model selection**: Override the LLM with `provider_id`, `model_id`, and `variant_id`.
- **Skill hints**: Pass skill names to prepend specialized instructions to the agent prompt.
- **Custom environment**: Inject non-secret env vars into the agent container via the `env` map.
- **Timeout**: Per-task timeout in seconds (default 600, configurable via `DEFAULT_TASK_TIMEOUT_SEC`).

### Monitor Tasks

- **Status tracking**: Tasks progress through `pending` → `running` → `done` / `error` / `cancelled`.
- **Event history**: Full chronological event stream for any task (`task_events`).
- **Progress timeline**: Distilled status-change timeline with summaries (`task_progress`).
- **Latest event**: Quick check on a task's last activity with staleness detection (`task_latest_event`).
- **Session export**: Markdown transcript of the completed agent session (`task_export`).

### Control Tasks

- **Cancel**: Cancel a pending or running task by ID.
- **Clear queue**: Cancel all pending tasks in one call.

### List & Filter

- List recent tasks, optionally filtered by status.
- Team-scoped: non-admin tokens see only their team's tasks.

---

## Triggers & Schedules

### Cron Triggers

Schedule recurring tasks with standard five-field cron expressions (e.g., `0 9 * * 1` for every Monday at 9am) or descriptors like `@hourly`.

- Create, update, delete, and list cron triggers.
- Enable/disable triggers without deleting them.
- `next_run_at` is tracked and updated after each activation.
- Execution history is recorded in `chetter_schedule_runs`.
- **Run now**: Fire any cron trigger immediately with `run_trigger`.

### PR Review Triggers

Automatically submit review tasks when a pull request needs attention.

- Configured per repository (e.g., `flatout-works/chetter`).
- Activated by the GitHub webhook when:
  - A PR is opened, synchronized, or reopened with the `chetter-review` label.
  - A PR is opened from a fork (external contributor).
  - Someone comments `/chetter-review` on a PR (requires write access).
- Each trigger can use a custom prompt or the built-in review template.
- The built-in template instructs the agent to understand the PR, review changed files, verify compilation/tests, and post a structured review via `gh pr review`.

---

## Runner Fleet

### Runner Registration

Runners register with the server on startup via the `RegisterRunner` RPC and send heartbeats every 5 seconds. The server tracks:

- Runner status, image reference/digest, version
- Max concurrent tasks and current slot availability
- Lifetime counters (started, completed, errors)
- Currently running task IDs

### Task Claiming

Runners claim pending tasks using `SELECT ... FOR UPDATE SKIP LOCKED` for atomic, lock-free claiming. The claim includes a lease (default 60s, renewable).

### Lease & Heartbeat System

- Runners renew their task leases on each heartbeat.
- If a heartbeat is missed, the lease expires.
- The **reaper** (runs every 30s) reclaims expired leases back to `pending` (for re-claim) or marks them `error` if max attempts exceeded.
- Stale tasks (no update for `timeout_sec + 120s` grace) are also reaped.

### Runner Health

The `runner_health` tool provides fleet-wide diagnostics:

- Running and stale task counts across the fleet.
- Active runner image versions.
- Per-task heartbeat age (with `include_tasks`).

---

## Agent Harnesses

### OpenCode Harness (default)

Runs [OpenCode](https://opencode.ai) as the coding agent.

**Two execution modes:**

- **Serve mode** (preferred): Starts `opencode serve`, creates a session, sends prompts via HTTP API, watches events via SSE, and exports the session transcript.
- **Batch mode**: Runs `opencode run` as a one-shot CLI command, parses JSONL output.

**Configuration:**

- Generates `.opencode.json` with MCP servers (`runner-bridge` + `chetter`), permissions, and provider settings.
- Auto-detects and copies opencode auth state, model state, and ripgrep binary.
- Adds providers: DeepSeek, OpenCode Zen, Synthetic (based on available API keys).
- Optional MEM9 plugin integration.
- Model resolution: request fields → agent config → env vars → default (`synthetic/hf:zai-org/GLM-5.1`).

### Claude Code Harness

Runs Anthropic's Claude Code CLI as the agent.

- Batch-only (no serve mode).
- Command: `claude --bare -p <prompt> --output-format stream-json --permission-mode bypassPermissions --max-turns 100`.
- Default model: `claude-sonnet-4-5`.
- Generates `.claude/settings.json` with permission allowlist/denylist.
- Generates `.claude/mcp.json` with `runner-bridge` and `chetter` MCP servers.
- Resolves agent system prompts from `.claude/agents/<name>.md` or `.opencode/agent/<name>.md`.

### MCP Bridge

A tiny binary that bridges stdio MCP to a Unix socket, enabling OpenCode to use the runner's local MCP tools.

---

## Execution Environments

### Kata Containers (default)

Tasks run in Kata Containers via containerd with full network isolation:

- Linux bridges, veth pairs, and network namespaces per task.
- Subnet allocation from `10.200.X.0/24` (200 subnets available).
- iptables rules block cloud metadata (169.254.169.254) and restrict egress.
- Transparent HTTP/HTTPS proxy with domain allowlist/blocklist.
- DNS proxy with domain filtering and IPv6 suppression (avoids Kata VM stalls).

### Docker

Tasks run in standard Docker containers with the runner's Docker daemon.

### Local

Tasks run directly on the host (no containerization). Useful for development.

---

## Runner-Local MCP Tools

Tools available to the agent inside the container:

### Workspace

| Tool | Description |
|------|-------------|
| `workspace_read_file` | Read a file from the workspace |
| `workspace_write_file` | Write a file to the workspace |
| `workspace_list_directory` | List directory contents |
| `workspace_bash` | Execute shell commands (configurable timeout, default 60s) |

### Git

| Tool | Description |
|------|-------------|
| `git_status` | Show git status (porcelain format) |
| `git_pull` | Pull from remote |
| `git_push` | Push to remote |
| `git_commit` | Commit changes |

### HTTP

| Tool | Description |
|------|-------------|
| `fetch_url` | HTTP fetch with configurable method, headers, and body |

### Deployment

| Tool | Description |
|------|-------------|
| `deploy_build` | Build Docker image from workspace Dockerfile |
| `deploy_push` | Push image to container registry |
| `deploy_run` | Run a container (local Docker or preview provider) |
| `deploy_status` | Check container status and ports |
| `deploy_stop` | Stop a running container |
| `deploy_logs` | Get container logs |
| `deploy_list` | List deployment containers |
| `deploy_versions` | List built image versions |
| `deploy_rollback` | Roll back to a previous image tag |

---

## Auth & Teams

### Admin Token

Single static bearer token set via `MCP_AUTH_TOKEN`. Grants unrestricted access to all resources and admin-only operations.

### Team Tokens

SHA-256 hashed tokens stored in the `api_tokens` table. Each token belongs to a user in a team.

- Tasks and schedules are automatically scoped to the team.
- Team tokens can only see and manage their own team's resources.
- Admin tokens bypass team scoping.

### Admin-Only Operations

- Create, list, and delete API tokens.
- Create, list, and delete teams.
- List users (optionally filtered by team).

### No-Auth Mode

When `MCP_AUTH_TOKEN` is empty, authentication is disabled entirely. All requests pass through with admin scope.

---

## GitHub Integration

### GitHub App Webhook

When configured, Chetter processes GitHub webhook events:

- **PR events**: opened, synchronize, reopened, labeled.
- **Comment events**: PR comments with `/chetter-review`.

### PR Review Flow

1. GitHub webhook delivers event to `/webhook/github`.
2. Signature is verified with `GITHUB_WEBHOOK_SECRET`.
3. Duplicate deliveries are filtered (5-min TTL, 4096-entry cache).
4. For comment triggers, write access is verified before proceeding.
5. Matching `pr_review` triggers are found for the repo.
6. The `chetter-review` label is added if not present.
7. A review task is submitted for each matching trigger.
8. On failure, a comment is posted: `Chetter review could not start`.

### Built-in Review Prompt

When no custom prompt is set on the trigger, the agent uses a structured template that:

1. Understands the PR context.
2. Reviews changed files.
3. Verifies compilation and tests.
4. Posts a review via `gh pr review` with a footer showing agent name, model, and runner image.

---

## Arcane Vulnerability Scanning

Optional integration with Arcane for container image security scanning. Only active when `ARCANE_SERVER_URL` and `ARCANE_API_KEY` are configured.

| Tool | Description |
|------|-------------|
| `arcane_scanner_status` | Check Trivy scanner availability |
| `arcane_environment_summary` | Aggregated vulnerability counts across all images |
| `arcane_list_images` | List all Docker images with IDs and tags |
| `arcane_image_summary` | Vulnerability summary for a specific image |
| `arcane_list_vulnerabilities` | Detailed CVE list with severity filtering and pagination |

Severity levels: CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN.

---

## CLI (`chetterctl`)

Token management CLI for admin operations.

| Command | Description |
|---------|-------------|
| `chetterctl token create --team X --user Y --name Z` | Create a new API token |
| `chetterctl token list` | List all tokens |
| `chetterctl token delete --name X` | Delete a token by name |

Server URL and auth token can be set via flags or env vars (`CHETTER_SERVER_URL`, `CHETTER_TOKEN`).

---

## MCP Tool Reference (26 tools)

### Task Management (7)

| Tool | Description |
|------|-------------|
| `chetter_submit_task` | Submit a development task |
| `chetter_task_status` | Get task status and result |
| `chetter_list_tasks` | List recent tasks (status filter, team-scoped) |
| `chetter_cancel_task` | Cancel a pending/running task |
| `chetter_task_export` | Get markdown session transcript |
| `chetter_clear_queue` | Cancel all pending tasks |
| `chetter_task_events` | Get full event history |

### Task Monitoring (2)

| Tool | Description |
|------|-------------|
| `chetter_task_progress` | Distilled progress timeline |
| `chetter_task_latest_event` | Most recent event with staleness check |

### Triggers (5)

| Tool | Description |
|------|-------------|
| `chetter_create_trigger` | Create cron or PR review trigger |
| `chetter_update_trigger` | Update trigger fields |
| `chetter_list_triggers` | List triggers (type/enabled filter) |
| `chetter_delete_trigger` | Delete a trigger |
| `chetter_run_trigger` | Fire a cron trigger immediately |

### Fleet Health (1)

| Tool | Description |
|------|-------------|
| `chetter_runner_health` | Fleet diagnostics with per-task heartbeat age |

### Token Management (3) — Admin

| Tool | Description |
|------|-------------|
| `chetter_create_token` | Create API token (auto-creates team/user) |
| `chetter_list_tokens` | List all tokens |
| `chetter_delete_token` | Delete token by name |

### Team/User Management (3) — Admin

| Tool | Description |
|------|-------------|
| `chetter_create_team` | Create a team |
| `chetter_list_teams` | List all teams |
| `chetter_delete_team` | Delete team (cascades to users, tokens, tasks, schedules) |
| `chetter_list_users` | List users (team filter) |

### Schedule Runs (1)

| Tool | Description |
|------|-------------|
| `chetter_list_schedule_runs` | Schedule execution history |

### Arcane Scanning (5) — Conditional

| Tool | Description |
|------|-------------|
| `chetter_arcane_scanner_status` | Trivy availability check |
| `chetter_arcane_environment_summary` | Aggregated vulnerability counts |
| `chetter_arcane_list_images` | List Docker images |
| `chetter_arcane_image_summary` | Per-image vulnerability summary |
| `chetter_arcane_list_vulnerabilities` | Detailed CVE list |

---

## Configuration

### Server Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_ADDR` | `:8080` | Listen address |
| `MCP_AUTH_TOKEN` | (empty) | Admin bearer token; empty = auth disabled |
| `DATABASE_DSN` | (required) | TiDB connection string |
| `DEFAULT_AGENT_IMAGE` | `ghcr.io/flatout-works/chetter-runner:latest` | Default runner image |
| `DEFAULT_TASK_TIMEOUT_SEC` | `600` | Default task timeout |
| `ARCANE_SERVER_URL` | (empty) | Arcane vulnerability scanner URL |
| `ARCANE_API_KEY` | (empty) | Arcane API key |
| `GITHUB_APP_ID` | `0` | GitHub App ID |
| `GITHUB_APP_PRIVATE_KEY_B64` | (empty) | GitHub App RSA private key (PEM, base64) |
| `GITHUB_WEBHOOK_SECRET` | (empty) | GitHub webhook HMAC secret |
| `GITHUB_WEBHOOK_DISABLED` | `false` | Kill switch for webhook processing |
| `GITHUB_INSTALLATION_ID` | `0` | GitHub App installation ID |

### Runner Configuration

Configured via YAML (`runner.yaml`) with env var fallbacks:

| Setting | Env Var | Default | Description |
|---------|---------|---------|-------------|
| `server.url` | `CHETTER_SERVER_URL` | (required) | Server URL |
| `server.auth_token` | `CHETTER_RUNNER_AUTH_TOKEN` | (empty) | Auth token |
| `runner.workspace_root` | — | `/var/lib/runner` | Workspace directory |
| `runner.max_concurrent` | — | `10` | Max concurrent tasks |
| `proxy.listen_addr` | — | `:18080` | HTTP proxy address |
| `proxy.allowed_domains` | — | (empty) | Egress allowlist |
| `proxy.blocked_domains` | — | (empty) | Egress blocklist |
| `dns.listen_addr` | — | `:53` | DNS proxy address |
| `dns.upstream` | — | `8.8.8.8:53` | Upstream DNS |
| `execution.harness` | — | (empty) | `claude-code`, `codex`, or default=opencode |
| `deploy.provider` | — | `local` | `local` or `preview` |
| `deploy.registry` | — | (empty) | Container registry |
| `chetter_mcp.url` | — | (empty) | MCP server URL injected into agents |

### Secrets Forwarded to Agent Containers

`ANTHROPIC_API_KEY`, `GITHUB_TOKEN`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `OPENCODE_API_KEY`, `SYNTHETIC_API_KEY`, `MEM9_API_KEY`, `MEM9_API_URL`, `MEM9_DEBUG`, `MEM9_HOME`
