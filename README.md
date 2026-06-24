# Chetter

Self-hosted MCP server for running autonomous AI development agents on a fleet of containerized runners.

- **Submit tasks** against Git repos with LLM agents in isolated containers
- **Track progress** — streaming events, logs, session transcripts
- **Automate** — cron triggers, PR review webhooks, issue/comment responders
- **Resume** — pause agent sessions and resume on the same runner with follow-up prompts
- **Manage** — web UI, MCP tools, team tokens, audit log, runner fleet health
- **Powered by TiDB** — MySQL wire protocol, vector search on the roadmap

## Design Principles

- **Standard harnesses.** Chetter delegates agent execution to existing CLI tools — primarily OpenCode, with growing support for Claude Code and Pi. No custom agent runtime.
- **Deploy anywhere.** Both the server and runners run on plain Docker or standard Kubernetes. No special infrastructure beyond a TiDB database.
- **GitHub-native orchestration.** Chetter uses PRs, issues, reviews, and comments to drive agent workflows — the same primitives developers already use.
- **Plain containers as environments.** The agent runs in a normal Docker container. You define the image with the tools and stack your project needs.
- **MCP-first, API+UI for observation.** All interaction flows through MCP tools. The ConnectRPC API and web UI exist primarily for monitoring, inspection, and admin.

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

- **OpenCode** — built-in config at `.opencode/opencode.json`; just set `CHETTER_MCP_TOKEN`
- **Claude Code** — `claude mcp add --transport http chetter https://chetter.example.com/mcp --header "Authorization: Bearer $TOKEN"`
- **Any MCP client** — standard remote MCP server format; see [docs/MANUAL.md](docs/MANUAL.md)

## Repository Layout

| Path | Purpose |
|---|---|
| `main.go` | Server entry point |
| `internal/` | Config, store, service, webhook |
| `db/` | Migrations and sqlc queries |
| `runner/` | Containerized runner harness |
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
