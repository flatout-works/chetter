# Chetter

Chetter is a self-hosted MCP (Model Context Protocol) server for running autonomous AI development agents. It gives your AI tooling a way to submit software development work to a fleet of containerized runners.

A Chetter runner can clone a repository, start an OpenCode agent, execute a prompt, stream progress events, and persist the final result. You interact with it through MCP tools from clients such as Claude Desktop, Cursor, Continue, or your own agent stack.

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
git clone https://github.com/flatout-works/chetter.git
cd chetter
```

### 2. Configure

```bash
cp .env.example .env
```

Edit `.env` and set at least:

- `NATS_TOKEN`
- `MYSQL_PASSWORD`
- `MYSQL_ROOT_PASSWORD`
- `CHETTER_MCP_AUTH_TOKEN`
- At least one LLM provider key, such as `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `SYNTHETIC_API_KEY`, or `OPENCODE_API_KEY`

For public servers, always set `CHETTER_MCP_AUTH_TOKEN`.

### 3. Start

```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

This starts:

- MySQL for Chetter state
- NATS with JetStream for task messaging
- The Chetter MCP server on port `18088`
- Two runner containers that pick up tasks

The `deploy/compose.local.yaml` override adds the bundled MySQL service. If you
already have MySQL or TiDB, set `DATABASE_DSN` in `.env` and run only
`-f deploy/compose.yaml`.

### 4. Check It

```bash
curl http://localhost:18088/healthz
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml ps
```

### 5. Connect Your AI Client

#### OpenCode

This repo includes ready-to-use OpenCode configuration at `.opencode/opencode.json`. It defines:

- **MCP connection** to the Chetter server
- **Slash commands:** `/chetter-status`, `/chetter-tasks`, `/chetter-submit`, `/chetter-schedules`, `/chetter-cancel`
- **Skill** at `.opencode/skill/chetter/SKILL.md` with workflows and schedule management guidance

To set up in your own OpenCode project:

1. Copy `.opencode/opencode.json` (or the relevant `mcp` and `command` blocks) into your project's `opencode.json`.
2. Copy `.opencode/skill/chetter/SKILL.md` to your project's `.opencode/skill/chetter/SKILL.md`.
3. Set the token:
   ```bash
   export CHETTER_MCP_TOKEN=your-token
   ```
4. Verify:
   ```bash
   opencode mcp list
   # Should show "chetter" as enabled
   ```

If you cloned this repo, the config is already in place; just set `CHETTER_MCP_TOKEN`.

#### Claude Code

Claude Code supports remote MCP servers. Add the server:

```bash
claude mcp add --transport http chetter https://chetter.flatout.works/mcp \
  --header "Authorization: Bearer $CHETTER_MCP_TOKEN"
```

Or create a project-scoped `.mcp.json`:
```json
{
  "mcpServers": {
    "chetter": {
      "type": "http",
      "url": "https://chetter.flatout.works/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      }
    }
  }
}
```

For similar command workflows, translate the OpenCode command templates into your Claude Code command setup.

Verify:
```bash
claude mcp list
# Should show chetter
```

#### Other MCP Clients (Cursor, Continue, Claude Desktop)

Use the standard MCP remote server format:

```json
{
  "mcpServers": {
    "chetter": {
      "url": "https://chetter.flatout.works/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_CHETTER_MCP_AUTH_TOKEN"
      }
    }
  }
}
```

If you self-host Chetter, use `http://YOUR_SERVER:18088/mcp` instead.

### 6. Submit A Task

Call the `chetter_submit_task` MCP tool from your AI client, or use `/chetter-submit` in OpenCode:

```json
{
  "prompt": "Add input validation to all API handlers and run the tests.",
  "git_url": "https://github.com/my-org/my-repo",
  "git_ref": "main",
  "agent_image": "ghcr.io/flatout-works/chetter-runner:main"
}
```

Set `GITHUB_TOKEN` in `.env` if runners need access to private repositories or need to create branches and pull requests.

## Common Commands

```bash
# Follow all service logs
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f

# Follow only the MCP server
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f chetter-mcp

# Follow runner logs
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f chetter-runner chetter-runner-2

# Restart after editing .env
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d

# Stop the stack
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml down
```

## MCP Tools

| Tool | Purpose |
|---|---|
| `chetter_submit_task` | Submit a one-off development task |
| `chetter_task_status` | Fetch persisted task status and result |
| `chetter_list_tasks` | List recent tasks |
| `chetter_schedule_task` | Create a cron-backed task schedule |
| `chetter_run_schedule` | Run a schedule immediately |
| `chetter_list_schedules` | List cron task schedules |
| `chetter_delete_schedule` | Delete a schedule by name |
| `chetter_update_schedule` | Update a schedule |
| `chetter_cancel_task` | Cancel a pending or running task |
| `chetter_clear_queue` | Clear queued tasks |
| `chetter_task_events` | Fetch full event history for a task |
| `chetter_task_progress` | Fetch distilled task progress |
| `chetter_task_latest_event` | Fetch latest event for a task |
| `chetter_runner_health` | Derive fleet health and runner status |

## Configuration

### Main Environment Variables

| Variable | Description |
|---|---|
| `CHETTER_MCP_AUTH_TOKEN` | Bearer token required by `/mcp` |
| `NATS_TOKEN` | NATS auth token used by the local stack |
| `MYSQL_PASSWORD` | Password for the local `chetter` MySQL user |
| `MYSQL_ROOT_PASSWORD` | Root password for the local MySQL container |
| `DATABASE_DSN` | Optional external MySQL/TiDB DSN override |
| `GITHUB_TOKEN` | Optional token for private repos and GitHub write operations |
| `OPENAI_API_KEY` | Optional OpenAI key for runner agents |
| `DEEPSEEK_API_KEY` | Optional DeepSeek key for runner agents |
| `SYNTHETIC_API_KEY` | Optional Synthetic key for runner agents |
| `OPENCODE_API_KEY` | Optional OpenCode provider key |
| `MEM9_API_KEY` | Optional Mem9 memory provider key |
| `CHETTER_RUNNER_IMAGE_DIGEST` | Optional pinned image digest for PR signature footers |

If `DATABASE_DSN` is not set, use `deploy/compose.local.yaml` to add the bundled
MySQL service. Production deployments should usually set `DATABASE_DSN` and run
only `deploy/compose.yaml`.

## Arcane Deployment

Chetter's production deployment uses Arcane GitOps and Arcane image builds on
wowbagger. GitHub Actions does not build Docker images.

The deployment flow is:

1. Push to `main`.
2. GitHub Actions runs `make check`.
3. The workflow calls Arcane's API to sync GitOps, build images on wowbagger,
   push them to GHCR, and redeploy the Chetter project.
4. Arcane redeploys containers from the GHCR images.

Required GitHub repository secrets:

| Secret | Description |
|---|---|
| `ARCANE_URL` | Arcane base URL, for example `https://wowbagger.krampe.se` |
| `ARCANE_API_KEY` | Arcane API key with project build/deploy permissions |
| `ARCANE_CHETTER_PROJECT_ID` | Arcane Chetter project ID |
| `ARCANE_CHETTER_GITOPS_ID` | Arcane GitOps sync ID |

Optional GitHub repository variable:

| Variable | Description |
|---|---|
| `ARCANE_ENVIRONMENT_ID` | Arcane environment ID, defaults to `0` |

Arcane must have GHCR registry credentials configured in Arcane's registry
settings. Do not store `GHCR_TOKEN` in GitHub Actions for this deployment path.

Arcane GitOps must use:

- Compose path: `compose.yaml`
- Directory sync: enabled

The root `compose.yaml` is for Arcane GitOps and uses `build:` directives with
explicit GHCR image tags. The `deploy/compose.yaml` file is the portable
self-hosted compose stack that pulls the published GHCR images.

## Repository Layout

| Path | Purpose |
|---|---|
| `compose.yaml` | Arcane GitOps compose file with build directives |
| `main.go` | MCP/HTTP server entry point |
| `internal/config/` | Environment-backed configuration |
| `internal/store/` | MySQL/TiDB schema and persistence |
| `internal/bus/` | NATS and JetStream transport |
| `internal/service/` | MCP tools and task orchestration |
| `internal/webhook/` | Optional GitHub webhook handling |
| `deploy/compose.yaml` | Portable Docker Compose stack using published GHCR images |
| `runner/` | Runner runtime, image Dockerfiles, and entrypoint |
| `schedules/` | Active production schedules |
| `schedules-examples/` | Example schedule templates |
| `tools/` | Agent support files baked into the runner image |

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
