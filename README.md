# Chetter

Chetter is an Open Source system written in Go for running autonomous AI development agents using standard harnesses (OpenCode, Claude Code, CodeWhale, Pi, Codex) in standard Docker or Kubernetes environments.
If you are looking for a simple(r) solution to self host standard autonomous coding agents, then Chetter should be interesting. Hop into [our Discord](https://discord.gg/KkZxKwSTvF) if you want to know more.

- **Submit tasks to standard harnesses** against Git repos with LLM agents in isolated containers
- **Track live progress** — streaming events, logs, session transcripts
- **Automation** — cron triggers, generic webhooks, PR review webhooks, GitHub PR/issue/comment responders
- **Pause and resume** — pause agent sessions and resume on the same runner with follow-up prompts
- **Manage via MCP or web UI** — web UI, MCP tools, team tokens, audit log, runner fleet health

## Design Principles

Chetter was built as a reaction towards the solutions available (E2B, Daytona, Modal etc) which often are **not Open Source** or want to lock you into them **hosting your agents**, need relatively **exotic infrastructure** like Firecracker VMs or use **custom harnesses** instead of industry standard harnesses.

Chetter instead ...

- **Is true Open Source.** Chetter was built for organisations that feel that the future of software development should be under their own control.
- **Uses Standard harnesses.** Chetter delegates agent execution to existing CLI tools — primarily OpenCode, with support for Claude Code, CodeWhale, Pi, and Codex. No custom agent runtime. We believe that the popular harnesses have the momentum and we also feel that constructing an autonomous agent should be done with the same tools that you use as an individual developer.
- **Deploys in Docker or Kubernetes.** Both the server and runners run on standard Docker or Kubernetes. No special infrastructure needed. The optional sandboxing is implemented via **gVisor** which is flexible and simple enough to use in Docker or Kubernetes.
- **Relies on GitHub-native orchestration.** Chetter integrates deeply with GitHub and uses PRs, issues, reviews, and comments to drive agent workflows — the same primitives developers already use.
- **Uses plain containers as environments.** The agent runs in a normal Docker container. You define the image with the tools and stack your project needs.
- **Is MCP and API first, web UI for observation.** The server has a full ConnectRPC API exposed also as MCP tools. There is also a web UI primarily for monitoring, inspection, and admin tasks.

## Quick Start

```bash
git clone https://github.com/flatout-works/chetter.git
cd chetter
cp .env.example .env
# Edit .env: set CHETTER_MCP_AUTH_TOKEN and at least one LLM provider key
./deploy/build.sh
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

The MCP server is at `http://localhost:18088`, the web UI at `http://localhost:18090`.
See [docs/MANUAL.md](docs/MANUAL.md) for detailed setup, configuration, and operations.

## Connect Your AI Client

- **OpenCode** — built-in config at `.opencode/opencode.json`; set `CHETTER_MCP_TOKEN` and point the url at your server
- **Claude Code** — `claude mcp add --transport http chetter https://chetter.example.com/mcp --header "Authorization: Bearer $TOKEN"`
- **Any MCP client** — standard remote MCP server format; see [docs/MANUAL.md](docs/MANUAL.md)

## Repository Layout

| Path | Purpose |
|---|---|
| `main.go` | Server entry point |
| `internal/` | Config, store, service, webhook, web UI |
| `cmd/` | `chetterctl` token management CLI |
| `db/` | Migrations and sqlc query files (TiDB/MySQL under `db/migrations/`, PostgreSQL under `db/postgres/`) |
| `proto/` | ConnectRPC service definitions (server ↔ runner) |
| `runner/` | Containerized runner harness |
| `web/` | SvelteKit web UI (Flowbite-Svelte) |
| `deploy/` | Docker Compose and Kubernetes manifests |
| `docs/` | Documentation |

## Docs

- [docs/MANUAL.md](docs/MANUAL.md) — comprehensive operations guide
- [docs/FEATURES.md](docs/FEATURES.md) — shipped feature reference
- [docs/PLAN.md](docs/PLAN.md) — roadmap and milestones
- [docs/README.md](docs/README.md) — full docs index

## Build From Source

```bash
make check && make build
```

## License

MIT
