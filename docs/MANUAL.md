# Chetter Manual

## Overview

Chetter is a self-hosted MCP (Model Context Protocol) server that submits software development tasks to a fleet of containerized runners. Each runner clones a repository, starts an OpenCode agent, executes a prompt, and reports progress.

This manual covers setup, configuration, and operation.

---

## Architecture at a Glance

```
┌─────────────┐    MCP/HTTP     ┌──────────────┐    ConnectRPC    ┌─────────────┐
│  AI Client  │◀───────────────▶│ Chetter MCP  │◀────────────────▶│   Runner    │
│ (Claude,    │   (tools)       │   Server     │   (claim task)   │  (Docker)   │
│  Cursor,    │                 │    TiDB      │                  │             │
│  OpenCode)  │                 │              │                  │             │
└─────────────┘                 └──────────────┘                  └─────────────┘
                                        │
                                        ▼
                              ┌──────────────┐
                              │   Cron /     │
                              │   Schedules  │
                              └──────────────┘
```

- **Server** (`chetter` binary): MCP endpoint, task queue, schedule runner, auth
- **Runner** (`runner/` module): Containerized agent harness, polls for tasks
- **CLI** (`chetterctl`): Token management for team/users
- **Database**: TiDB for tasks, schedules, events, tokens, teams

---

## Quick Start

### 1. Clone & Configure

```bash
git clone https://github.com/flatout-works/chetter.git
cd chetter
cp .env.example .env
```

Edit `.env` and set at least:

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `CHETTER_MCP_AUTH_TOKEN` | Yes* | — | Admin bearer token for MCP endpoint |
| `DATABASE_DSN` | No | — | External TiDB DSN override |

\* Required for any shared or public server. Single-user local setups can leave it empty.

### 2. Start with Docker Compose

```bash
# With bundled TiDB
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d

# With external database (set DATABASE_DSN in .env)
docker compose --env-file .env -f deploy/compose.yaml up -d
```

Services started:
- `chetter-mcp` on port `18088` (or `HTTP_ADDR`)
- `tidb` (if using `compose.local.yaml`)
- `chetter-runner` (one or more, depending on compose override)

### 3. Verify

```bash
curl http://localhost:18088/healthz
# → ok

# Check runner health
opencode mcp list
# chetter should be enabled
```

### 4. Submit a Task

```json
{
  "prompt": "Add input validation to all API handlers and run the tests.",
  "git_url": "https://github.com/my-org/my-repo",
  "git_ref": "main",
  "agent_image": "ghcr.io/flatout-works/chetter-runner:main"
}
```

Use the `chetter_submit_task` MCP tool or the `/chetter-submit` OpenCode command.

---

## Environment Variables Reference

### Core Server

| Variable | Required | Default | Description |
|---|---|---|---|
| `HTTP_ADDR` | No | `:8080` | Server listen address |
| `DATABASE_DSN` | Yes | — | TiDB connection string. Example: `root@tcp(host:4000)/chetter?parseTime=true` |
| `MCP_AUTH_TOKEN` | Yes | — | Admin bearer token. Bypasses all team scoping. Empty and `change-me*` values are rejected. |
| `DEFAULT_AGENT_IMAGE` | No | `ghcr.io/flatout-works/chetter-runner:latest` | Default runner image if task does not specify one |
| `DEFAULT_TASK_TIMEOUT_SEC` | No | `600` | Task timeout in seconds |

### Arcane Vulnerability Scanning (Optional)

| Variable | Required | Description |
|---|---|---|
| `ARCANE_SERVER_URL` | No | Arcane vulnerability scanner URL |
| `ARCANE_API_KEY` | No | Arcane API key |

If both are set, five `chetter_arcane_*` MCP tools are registered.

### GitHub Integration (Optional)

| Variable | Required | Description |
|---|---|---|
| `GITHUB_APP_ID` | Yes* | GitHub App ID for PR reviews |
| `GITHUB_INSTALLATION_ID` | Yes* | Installation ID |
| `GITHUB_APP_PRIVATE_KEY_B64` | Yes* | Base64-encoded PEM private key |
| `GITHUB_WEBHOOK_SECRET` | Yes* | HMAC-SHA256 secret for webhook signature verification |
| `GITHUB_WEBHOOK_DISABLED` | No | `true` to disable the webhook (kill switch) |

\* Only required if you want PR review automation via GitHub webhook. See [docs/REVIEWS.md](REVIEWS.md) for full setup.

### Runner Provider Keys

These are passed to runner containers as environment variables so the OpenCode agent inside can call LLM APIs:

| Variable | Purpose |
|---|---|
| `OPENAI_API_KEY` | OpenAI API access |
| `DEEPSEEK_API_KEY` | DeepSeek API access |
| `SYNTHETIC_API_KEY` | Synthetic API access |
| `OPENCODE_API_KEY` | OpenCode API access |
| `GITHUB_TOKEN` | For agents that clone private repos or create PRs |

### Persistent Memory (Optional)

| Variable | Default | Purpose |
|---|---|---|
| `MEM9_API_KEY` | — | Mem9 persistent memory provider key |
| `MEM9_API_URL` | `https://api.mem9.ai` | Mem9 API endpoint |
| `MEM9_DEBUG` | `false` | Debug logging for Mem9 |

---

## Configuration from Environment

All config is loaded from environment variables. There is no config file — set vars in `.env` and source it, or export directly:

```bash
export DATABASE_DSN="root@tcp(127.0.0.1:4000)/chetter?parseTime=true"
export MCP_AUTH_TOKEN="my-secure-token"
export DEFAULT_AGENT_IMAGE="ghcr.io/flatout-works/chetter-runner:main"
```

### `.env.example` Quick Reference

```bash
# Copy and edit
CHETTER_MCP_AUTH_TOKEN=

# Provider keys (at least one)
OPENAI_API_KEY=
DEEPSEEK_API_KEY=
SYNTHETIC_API_KEY=
OPENCODE_API_KEY=

# Optional: external DB instead of bundled TiDB
# DATABASE_DSN=root@tcp(host:4000)/chetter?parseTime=true

# Optional: GitHub for private repos / PR creation
GITHUB_TOKEN=
```

---

## Authentication & Multi-Tenancy

Chetter supports two auth models:

### 1. Admin Token (Global Access)

Set `MCP_AUTH_TOKEN`. This single bearer token bypasses all team scoping. It can see and manage all tasks, schedules, and tokens. Use a long, random value.

```bash
export MCP_AUTH_TOKEN="admin-secret-123"
# Then use: Bearer admin-secret-123
```

### 2. Team Tokens (Scoped Access)

Create scoped tokens for teams using `chetterctl` or the `chetter_create_token` MCP tool:

```bash
# Create a token for team "engineering" and user "alice"
chetterctl token create --team engineering --user alice --name alice-cli
# → Returns a raw token like "chtr_..."
```

- Each token belongs to a user in a team
- Tasks and schedules created with a token are auto-stamped with the team's `team_id`
- Team-scoped tokens can only see their own tasks and schedules
- Admin token (`MCP_AUTH_TOKEN`) sees everything

See `cmd/chetterctl/main.go` for CLI usage or use the MCP tools:
- `chetter_create_token`
- `chetter_list_tokens`
- `chetter_delete_token`

---

## MCP Tools

| Tool | Purpose |
|---|---|
| `chetter_submit_task` | Submit a one-off development task |
| `chetter_task_status` | Get task status and result |
| `chetter_list_tasks` | List recent tasks (filtered by team scope) |
| `chetter_create_trigger` | Create a trigger (cron schedule or PR review webhook) |
| `chetter_update_trigger` | Update a trigger by name |
| `chetter_list_triggers` | List triggers, optionally filtered by type |
| `chetter_delete_trigger` | Delete a trigger by name |
| `chetter_run_trigger` | Run a cron trigger immediately |
| `chetter_cancel_task` | Cancel a pending or running task |
| `chetter_clear_queue` | Cancel all pending tasks |
| `chetter_task_events` | Full event history for a task |
| `chetter_task_progress` | Distilled progress timeline |
| `chetter_task_latest_event` | Latest event for a task |
| `chetter_runner_health` | Runner fleet health and running tasks |
| `chetter_create_token` | Create team/user token (admin only) |
| `chetter_list_tokens` | List tokens (admin only) |
| `chetter_delete_token` | Delete a token (admin only) |
| `chetter_create_team` | Create a team (admin only) |
| `chetter_list_teams` | List all teams (admin only) |
| `chetter_delete_team` | Delete a team and cascade users/tokens/tasks/schedules (admin only) |
| `chetter_list_users` | List users, optionally filtered by team name (admin only) |
| `chetter_list_schedule_runs` | List schedule runs for the current team, optionally filtered by schedule name |
| `chetter_arcane_scanner_status` | Arcane scanner availability |
| `chetter_arcane_environment_summary` | Vulnerability counts across images |
| `chetter_arcane_list_images` | Docker images in Arcane |
| `chetter_arcane_image_summary` | Vulnerability summary per image |
| `chetter_arcane_list_vulnerabilities` | Detailed vulnerability list |

Arcane tools are only available if `ARCANE_SERVER_URL` and `ARCANE_API_KEY` are configured.

---

## Schedules

Chetter supports cron-backed schedules. See [docs/SCHEDULES.md](SCHEDULES.md) for full details.

Quick example:

```json
{
  "name": "nightly-docs-update",
  "cron_expr": "0 4 * * *",
  "prompt": "Review recent changes and update documentation...",
  "git_url": "https://github.com/my-org/my-repo",
  "git_ref": "main",
  "agent_image": "ghcr.io/flatout-works/chetter-runner:main"
}
```

---

## PR Reviews

Chetter can automatically review GitHub pull requests via webhook integration. Four trigger paths: label, fork, `/chetter-review` comment, and manual MCP submission.

See [docs/REVIEWS.md](REVIEWS.md) for full setup.

---

## Common Operations

### Check health
```bash
curl http://localhost:18088/healthz
```

### View logs
```bash
# All services
docker compose -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f

# MCP server only
docker compose -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f chetter-mcp
```

### Restart after `.env` changes
```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

### Stop
```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml down
```

### Database migrations
```bash
# Up
make migrate

# Status
make migrate-status

# Down one
make migrate-down

# Create new migration
make migrate-create
# → prompts for migration name
```

---

## Self-Hosting Checklist

- [ ] Set `CHETTER_MCP_AUTH_TOKEN` (for any non-local deployment)
- [ ] Set `DATABASE_DSN` (or use bundled TiDB)
- [ ] Configure at least one LLM provider key
- [ ] Optionally configure GitHub App for PR reviews
- [ ] Optionally configure Arcane for vulnerability scanning
- [ ] Verify health endpoint: `curl http://host:port/healthz`
- [ ] Verify MCP tools respond
- [ ] Create team tokens via `chetterctl` or `chetter_create_token`

---

## Further Reading

- [docs/SCHEDULES.md](SCHEDULES.md) — Cron schedule management
- [docs/REVIEWS.md](REVIEWS.md) — GitHub PR review automation
- [AGENTS.md](/AGENTS.md) — Developer commands, testing, and architecture notes
