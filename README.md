# Devfleet

Devfleet is a self-hosted MCP (Model Context Protocol) server for running
autonomous AI development agents. It manages a fleet of task runners that can
clone repositories, execute prompts using LLM-powered agents, and report
results — all through standard MCP tools.

Think of it as a CI/CD system for AI development workflows: submit a task,
and Devfleet dispatches it to a runner that spawns an LLM agent (via
OpenCode) to perform code generation, review, testing, documentation, or
any other development work you describe.

## What can you do with it?

- **Submit development tasks** — Describe work in natural language, Devfleet
  runs it in an isolated container with your tools and LLM provider.
- **Schedule recurring tasks** — Cron-backed maintenance: daily code quality
  audits, nightly vulnerability scans, changelog updates, PR reviews.
- **Monitor fleet health** — Track running tasks, runner versions, heartbeat
  staleness.
- **Extend via MCP** — Plug Devfleet into any MCP-capable AI tool (Claude
  Desktop, Cursor, VS Code + Continue, etc.) and let your AI assistant
  delegate work to the runner fleet.
- **Container vulnerability scanning** — Optional Arcane integration for
  dependency and image vulnerability management.

## Quick Start

### Prerequisites

- A Linux server with Docker and Docker Compose
- A TiDB or MySQL database (or use TiDB Cloud free tier)
- LLM provider API keys (OpenAI, DeepSeek, etc.)

### 1. Clone and configure

```bash
git clone https://github.com/flatout-works/devfleet.git
cd devfleet
```

Create a `.env` file:

```bash
# Database
DATABASE_DSN='user:password@tcp(your-db-host:4000)/devfleet?parseTime=true'

# NATS
NATS_TOKEN=change-me

# LLM provider keys (runners need at least one)
OPENAI_API_KEY=sk-...
DEEPSEEK_API_KEY=...
```

### 2. Start the fleet

```bash
docker compose -f deploy/compose.yaml up -d
```

This starts:
- A NATS server with JetStream for task messaging
- The Devfleet MCP server (HTTP on port 18088)
- Two runner containers that pick up and execute tasks

### 3. Connect your AI tooling

Add the Devfleet MCP server to any MCP-compatible client:

```json
{
  "mcpServers": {
    "devfleet": {
      "url": "http://your-server:18088/mcp",
      "headers": {
        "Authorization": "Bearer your-token"
      }
    }
  }
}
```

Set `MCP_AUTH_TOKEN` in your `.env` to require authentication, or leave it
empty for open access (not recommended for production).

### 4. Submit a task

Use any MCP client to call `devfleet_submit_task`:

```json
{
  "prompt": "Add input validation to all API handlers in src/handlers/",
  "git_url": "https://github.com/my-org/my-repo",
  "agent_image": "ghcr.io/devfleet/devfleet-runner:main"
}
```

## Repository Layout

| Path | Purpose |
|---|---|
| `main.go` | MCP/HTTP server entry point |
| `internal/config/` | Environment-backed configuration |
| `internal/store/` | TiDB/MySQL schema and persistence |
| `internal/bus/` | NATS and JetStream transport |
| `internal/service/` | MCP tools and task/schedule orchestration |
| `internal/webhook/` | GitHub webhook handling for PR review automation |
| `deploy/compose.yaml` | Docker Compose deployment |
| `runner/` | Runner runtime — listens for tasks and executes them via OpenCode |
| `schedules/` | Declarative cron task schedule definitions |
| `docs/` | Implementation notes and guides |
| `tools/` | Skills and templates baked into the runner image |

## MCP Tools

| Tool | Purpose |
|---|---|
| `devfleet_submit_task` | Submit a one-off development task |
| `devfleet_task_status` | Fetch persisted task status and result |
| `devfleet_list_tasks` | List recent tasks |
| `devfleet_schedule_task` | Create a cron-backed task schedule |
| `devfleet_run_schedule` | Run a schedule immediately |
| `devfleet_list_schedules` | List cron task schedules |
| `devfleet_delete_schedule` | Delete a schedule by name |
| `devfleet_update_schedule` | Update a schedule |
| `devfleet_cancel_task` | Cancel a pending or running task |
| `devfleet_clear_queue` | Clear queued tasks |
| `devfleet_task_events` | Fetch full event history for a task |
| `devfleet_task_progress` | Fetch distilled task progress |
| `devfleet_task_latest_event` | Fetch latest event for a task |
| `devfleet_runner_health` | Derive fleet health and runner status |
| `devfleet_arcane_scanner_status` | Query vulnerability scanner status |
| `devfleet_arcane_environment_summary` | Environment vulnerability summary |
| `devfleet_arcane_list_images` | List Docker images in environment |
| `devfleet_arcane_image_summary` | Image vulnerability summary |
| `devfleet_arcane_list_vulnerabilities` | List vulnerability findings |

## Configuration

### MCP Server

| Variable | Default | Description |
|---|---|---|
| `DATABASE_DSN` | | TiDB/MySQL DSN. Required |
| `MCP_AUTH_TOKEN` | | Optional bearer token for `/mcp` |
| `NATS_URL` | `nats://localhost:4222` | NATS URL |
| `JETSTREAM_ENABLED` | `true` | Enable JetStream |
| `TASK_SUBJECT` | `devfleet.runner.tasks` | Runner task subject |
| `EVENT_SUBJECT` | `devfleet.tasks.>` | Runner status subject wildcard |
| `HTTP_ADDR` | `:8080` | HTTP bind address |
| `DEFAULT_TASK_TIMEOUT_SEC` | `600` | Default task timeout |
| `DEFAULT_AGENT_IMAGE` | `ghcr.io/devfleet/devfleet-runner:latest` | Default runner image |
| `ARCANE_SERVER_URL` | | Arcane API URL for vulnerability scanning |
| `ARCANE_API_KEY` | | Arcane API key |

### Runner

| Variable | Description |
|---|---|
| `RUNNER_LOCAL` | `true` to run in Docker mode |
| `NATS_URL` | NATS connection URL |
| `TASK_SUBJECT` | Task subject to listen on |
| `RESULT_SUBJECT` | Subject to publish results to |
| `RUNNER_MAX_CONCURRENT` | Max concurrent tasks (default 1) |
| `OPENAI_API_KEY` | OpenAI provider key |
| `DEEPSEEK_API_KEY` | DeepSeek provider key |
| `MEM9_API_KEY` | Mem9 memory provider key |
| `GITHUB_TOKEN` | GitHub token for repository operations |

## Building from source

```bash
make check
make build
```

### Docker images

```bash
make docker-build-mcp        # Build MCP server image
make docker-build-runner      # Build runner image
make docker-build-runner-base # Build base tooling image
```

## Deployment

The CI workflow (`.github/workflows/devfleet.yml`) builds and pushes images to
`ghcr.io/devfleet/`, then triggers a redeploy via webhook. For self-hosted
deployments, use `deploy/compose.yaml` with your own registry or the prebuilt
images from GHCR.

## Environment

Devfleet runners use OpenCode to execute tasks. Each runner container needs at
least one LLM provider API key. Task-specific environment variables can be
passed via `devfleet_submit_task.env` — but runner-owned secrets (API keys)
should be configured on the runner container itself, not in task payloads.
