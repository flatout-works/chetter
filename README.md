# Devfleet

Devfleet is a self-hosted MCP (Model Context Protocol) server for running autonomous AI development agents. It gives your AI tooling a way to submit software development work to a fleet of containerized runners.

A Devfleet runner can clone a repository, start an OpenCode agent, execute a prompt, stream progress events, and persist the final result. You interact with it through MCP tools from clients such as Claude Desktop, Cursor, Continue, or your own agent stack.

## What It Can Do

- Submit one-off development tasks against a Git repository.
- Run LLM agents in isolated runner containers.
- Track task status, logs, progress, and result details.
- Run recurring cron-backed maintenance jobs.
- Cancel pending or running tasks.
- Inspect runner health and heartbeat freshness.
- Expose the whole control plane through a standard HTTP MCP endpoint.

## Quick Start

These steps are intended for a fresh Linux cloud machine with Docker installed.

### 1. Clone

```bash
git clone https://github.com/flatout-works/devfleet.git
cd devfleet
```

### 2. Configure

```bash
cp .env.example .env
```

Edit `.env` and set at least:

- `NATS_TOKEN`
- `MYSQL_PASSWORD`
- `MYSQL_ROOT_PASSWORD`
- `DEVFLEET_MCP_AUTH_TOKEN`
- At least one LLM provider key, such as `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `SYNTHETIC_API_KEY`, or `OPENCODE_API_KEY`

For public servers, always set `DEVFLEET_MCP_AUTH_TOKEN`.

### 3. Start

```bash
docker compose --env-file .env -f deploy/compose.yaml up -d
```

This starts:

- MySQL for Devfleet state
- NATS with JetStream for task messaging
- The Devfleet MCP server on port `18088`
- Two runner containers that pick up tasks

### 4. Check It

```bash
curl http://localhost:18088/healthz
docker compose --env-file .env -f deploy/compose.yaml ps
```

### 5. Connect Your MCP Client

Use this MCP server URL:

```text
http://YOUR_SERVER:18088/mcp
```

Example MCP client config:

```json
{
  "mcpServers": {
    "devfleet": {
      "url": "http://YOUR_SERVER:18088/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_DEVFLEET_MCP_AUTH_TOKEN"
      }
    }
  }
}
```

### 6. Submit A Task

Call the `devfleet_submit_task` MCP tool from your AI client:

```json
{
  "prompt": "Add input validation to all API handlers and run the tests.",
  "git_url": "https://github.com/my-org/my-repo",
  "git_ref": "main",
  "agent_image": "ghcr.io/flatout-works/devfleet-runner:main"
}
```

Use `GITHUB_TOKEN` in `.env` if runners need access to private repositories or need to create branches and pull requests.

## Common Commands

```bash
# Follow all service logs
docker compose --env-file .env -f deploy/compose.yaml logs -f

# Follow only the MCP server
docker compose --env-file .env -f deploy/compose.yaml logs -f devfleet-mcp

# Follow runner logs
docker compose --env-file .env -f deploy/compose.yaml logs -f dev-runner dev-runner-2

# Restart after editing .env
docker compose --env-file .env -f deploy/compose.yaml up -d

# Stop the stack
docker compose --env-file .env -f deploy/compose.yaml down
```

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

## Configuration

### Main Environment Variables

| Variable | Description |
|---|---|
| `DEVFLEET_MCP_AUTH_TOKEN` | Bearer token required by `/mcp` |
| `NATS_TOKEN` | NATS auth token used by the local stack |
| `MYSQL_PASSWORD` | Password for the local `devfleet` MySQL user |
| `MYSQL_ROOT_PASSWORD` | Root password for the local MySQL container |
| `DATABASE_DSN` | Optional external MySQL/TiDB DSN override |
| `GITHUB_TOKEN` | Optional token for private repos and GitHub write operations |
| `OPENAI_API_KEY` | Optional OpenAI key for runner agents |
| `DEEPSEEK_API_KEY` | Optional DeepSeek key for runner agents |
| `SYNTHETIC_API_KEY` | Optional Synthetic key for runner agents |
| `OPENCODE_API_KEY` | Optional OpenCode provider key |
| `MEM9_API_KEY` | Optional Mem9 memory provider key |

If `DATABASE_DSN` is not set, `deploy/compose.yaml` uses the included MySQL service.

## Repository Layout

| Path | Purpose |
|---|---|
| `main.go` | MCP/HTTP server entry point |
| `internal/config/` | Environment-backed configuration |
| `internal/store/` | MySQL/TiDB schema and persistence |
| `internal/bus/` | NATS and JetStream transport |
| `internal/service/` | MCP tools and task orchestration |
| `internal/webhook/` | Optional GitHub webhook handling |
| `deploy/compose.yaml` | Docker Compose stack |
| `runner/` | Runner runtime, image Dockerfiles, and entrypoint |
| `schedules/` | Example declarative schedules |
| `tools/` | Skills and templates baked into the runner image |

## Build From Source

```bash
make check
make build
```

Build images locally:

```bash
make docker-build-mcp
make docker-build-runner-base
make docker-build-runner
```

## License

MIT
