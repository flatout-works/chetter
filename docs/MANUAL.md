# Chetter Manual

Status: **Current operator guide**

This manual covers setup, configuration, and common operations. For a feature inventory, see [FEATURES.md](FEATURES.md). For roadmap work, see [PLAN.md](PLAN.md).

## Overview

Chetter is a self-hosted MCP server and web control plane for running autonomous development agents on a fleet of runners.

```text
AI client / web UI
      |
      | MCP / HTTP
      v
Chetter server + TiDB
      |
      | ConnectRPC claim, heartbeat, events
      v
Runner fleet -> Docker/gVisor task containers -> agent harness
```

Main binaries:

- `chetter`: server, MCP endpoint, web/API endpoint, triggers, auth, task queue.
- `chetterctl`: token management CLI.
- `runner`: runner harness service in `runner/`.

## Quick Start With Compose

1. Clone and configure:

```bash
git clone https://github.com/flatout-works/chetter.git
cd chetter
cp .env.example .env
```

2. Edit `.env` and set at minimum:

| Variable | Purpose |
|---|---|
| `CHETTER_MCP_AUTH_TOKEN` | External admin bearer token used by Compose and Kubernetes examples. Compose maps it to the server's `MCP_AUTH_TOKEN`. |
| `CHETTER_RUNNER_RPC_TOKEN` | Optional dedicated runner RPC token. If empty in Compose, it defaults to `CHETTER_MCP_AUTH_TOKEN`. |
| Provider key | At least one usable LLM/provider key, depending on selected harness and model. |

3. Build images if needed:

```bash
./deploy/build.sh
```

4. Start with bundled local TiDB:

```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

5. Or start with an external TiDB by setting `DATABASE_DSN` and omitting the local override:

```bash
docker compose --env-file .env -f deploy/compose.yaml up -d
```

6. Verify:

```bash
curl http://localhost:18088/healthz
```

Open the web UI at `http://localhost:18090` and log in with `CHETTER_MCP_AUTH_TOKEN`.

## Ports

| Host port | Container port | Purpose |
|---|---|---|
| `18088` | `8080` | MCP endpoint and health endpoint. |
| `18090` | `8090` | Web UI and ConnectRPC API. |

The underlying server env vars are `HTTP_ADDR` and `WEB_ADDR`.

## Authentication

There are three token contexts to keep distinct:

| Token | Where used | Notes |
|---|---|---|
| `MCP_AUTH_TOKEN` | Server binary admin token. | Required by the server process. Compose/K8s examples set this from external `CHETTER_MCP_AUTH_TOKEN`. |
| `CHETTER_MCP_AUTH_TOKEN` | Deployment-facing admin token and agent MCP token. | Use this in `.env`, Kubernetes secrets, and clients unless running the binary directly. |
| `CHETTER_RUNNER_RPC_TOKEN` | Runner-to-server ConnectRPC token. | Required by the server. Compose falls back to `CHETTER_MCP_AUTH_TOKEN` if this is empty. |

Team tokens are stored hashed in TiDB and belong to a user in a team. Team-scoped tokens can only see their team's tasks, triggers, schedule runs, and sessions.

Create a scoped token with `chetterctl`:

```bash
chetterctl token create --team engineering --user alice --name alice-cli
```

## Environment Variables

### Server

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `HTTP_ADDR` | No | `:8080` | MCP listen address. |
| `WEB_ADDR` | No | `:8090` | Web UI and ConnectRPC API listen address. |
| `MCP_AUTH_TOKEN` | Yes | empty | Server admin bearer token. Empty and `change-me*` values are rejected. |
| `CHETTER_RUNNER_RPC_TOKEN` | Yes | empty | Dedicated runner ConnectRPC token. Empty and `change-me*` values are rejected. |
| `DATABASE_DSN` | Yes for binary | empty | TiDB DSN. Compose local override can provide bundled TiDB. |
| `DEFAULT_AGENT_IMAGE` | No | `ghcr.io/flatout-works/chetter-runner:latest` | Default task runner image. |
| `DEFAULT_TASK_TIMEOUT_SEC` | No | `600` | Default task timeout. |
| `DEFINITIONS_REPO` | No | empty | Git repo for synced model catalog and future definitions. |
| `DEFINITIONS_BRANCH` | No | `main` | Definitions repo branch. |
| `ARCANE_SERVER_URL` | No | empty | Optional Arcane scanner URL. |
| `ARCANE_API_KEY` | No | empty | Optional Arcane API key. |
| `GITHUB_APP_ID` | For GitHub app | `0` | GitHub App ID. |
| `GITHUB_APP_PRIVATE_KEY_B64` | For GitHub app | empty | Base64-encoded GitHub App private key PEM. |
| `GITHUB_INSTALLATION_ID` | For GitHub app | `0` | GitHub App installation ID. |
| `GITHUB_WEBHOOK_SECRET` | For GitHub webhook | empty | HMAC-SHA256 webhook secret. |
| `GITHUB_WEBHOOK_DISABLED` | No | `false` | Webhook kill switch. |

### Runner And Agent Containers

| Variable | Purpose |
|---|---|
| `CHETTER_SERVER_URL` | Server URL used by the runner. |
| `CHETTER_RUNNER_AUTH_TOKEN` | Runner config token env. Compose fills this from `CHETTER_RUNNER_RPC_TOKEN` for current runner fallback compatibility. |
| `CHETTER_MCP_AUTH_TOKEN` | MCP token injected into agents for Chetter MCP tools. |
| `CHETTER_MCP_URL` | MCP URL injected into agents. |
| `USE_GVISOR` | Enables Docker `runsc` execution and checkpoint support when `true`. |
| `CHETTER_PROXY_ALLOWED_DOMAINS` | Optional HTTP/HTTPS egress allowlist. |
| `CHETTER_PROXY_BLOCKED_DOMAINS` | Optional HTTP/HTTPS egress blocklist. |
| `CHETTER_DNS_BLOCKED_DOMAINS` | Optional DNS blocklist. |
| `GITHUB_TOKEN` | GitHub token for cloning private repos and read operations inside tasks. |
| `SYNTHETIC_API_KEY`, `DEEPSEEK_API_KEY`, `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY` | Provider keys forwarded when configured. |
| `MEM9_API_KEY`, `MEM9_API_URL`, `MEM9_DEBUG`, `MEM9_HOME` | Optional Mem9 persistent memory integration. |

## Submit A Task

Use `chetter_submit_task` from an MCP client, the web UI, or an OpenCode command.

Example input:

```json
{
  "prompt": "Add input validation to all API handlers and run the tests.",
  "git_url": "https://github.com/my-org/my-repo",
  "git_ref": "main",
  "agent_image": "chetter-runner:latest",
  "harness": "opencode",
  "timeout_sec": 1800
}
```

For a resumable session:

```json
{
  "prompt": "Create a PR for the next documentation improvement.",
  "git_url": "https://github.com/flatout-works/chetter",
  "git_ref": "main",
  "harness": "opencode",
  "session_mode": "resumable",
  "pause_reason": "waiting_for_pr_feedback",
  "ttl_hours": 72
}
```

## MCP Tool Reference

### Tasks

| Tool | Purpose |
|---|---|
| `chetter_submit_task` | Submit a one-off development task. |
| `chetter_task_status` | Get task status and result details. |
| `chetter_list_tasks` | List recent tasks with optional status filter. |
| `chetter_cancel_task` | Cancel a pending or running task. |
| `chetter_clear_queue` | Admin-only cancellation of all pending tasks. |
| `chetter_task_events` | Full event history for a task. |
| `chetter_task_progress` | Distilled task progress timeline. |
| `chetter_task_latest_event` | Latest task event. |
| `chetter_task_export` | Markdown transcript for a completed task. |

### Sessions

| Tool | Purpose |
|---|---|
| `chetter_list_agent_sessions` | List recent agent sessions. |
| `chetter_agent_session_status` | Get an agent session and its runs. |
| `chetter_resume_agent_session` | Resume a paused session with a follow-up prompt. |

### Triggers And Schedule Runs

| Tool | Purpose |
|---|---|
| `chetter_create_trigger` | Create a cron, PR review, or issue trigger. |
| `chetter_update_trigger` | Update a trigger. |
| `chetter_list_triggers` | List triggers, optionally by type/enabled state. |
| `chetter_delete_trigger` | Delete a trigger. |
| `chetter_run_trigger` | Run a cron trigger immediately. |
| `chetter_list_schedule_runs` | List schedule run history. |

### Runner Fleet

| Tool | Purpose |
|---|---|
| `chetter_runner_health` | Fleet diagnostics and heartbeat ages. |
| `chetter_drain_runner` | Ask a runner to stop claiming new work and exit after current work. |

### GitHub Artifacts

| Tool | Purpose |
|---|---|
| `chetter_create_issue` | Create a GitHub issue with Chetter footer and audit/artifact records. |
| `chetter_issue_comment` | Create an issue or PR comment with Chetter footer. |
| `chetter_create_pr` | Create a GitHub PR with Chetter footer. |
| `chetter_pr_review` | Create a GitHub PR review with Chetter footer. |
| `chetter_list_task_artifacts` | Admin-only artifact browser/filter. |

### Admin, Definitions, And Audit

| Tool | Purpose |
|---|---|
| `chetter_create_token`, `chetter_list_tokens`, `chetter_delete_token` | Admin token management. |
| `chetter_create_team`, `chetter_list_teams`, `chetter_delete_team`, `chetter_list_users` | Admin team/user management. |
| `chetter_get_model_catalog` | Read the active model catalog summary. |
| `chetter_sync_definitions` | Admin manual sync of the definitions repo. |
| `chetter_list_audit_events` | Admin audit log query. |

### Conditional Arcane Tools

Registered only when `ARCANE_SERVER_URL` and `ARCANE_API_KEY` are configured:

- `chetter_arcane_scanner_status`
- `chetter_arcane_environment_summary`
- `chetter_arcane_list_images`
- `chetter_arcane_image_summary`
- `chetter_arcane_list_vulnerabilities`

## Common Operations

### Health

```bash
curl http://localhost:18088/healthz
```

### Logs

```bash
docker compose -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f
docker compose -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f chetter-mcp
```

### Restart After `.env` Changes

```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

### Stop

```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml down
```

### Migrations

```bash
make migrate
make migrate-status
```

## Related Docs

- [FEATURES.md](FEATURES.md) - current capability reference.
- [SCHEDULES.md](SCHEDULES.md) - cron trigger management.
- [REVIEWS.md](REVIEWS.md) - GitHub PR review automation.
- [HARNESSES.md](HARNESSES.md) - harness architecture.
- [PAUSED_SESSIONS.md](PAUSED_SESSIONS.md) - resumable sessions.
- [CONFIG_IN_GIT.md](CONFIG_IN_GIT.md) - configuration-as-code design.
