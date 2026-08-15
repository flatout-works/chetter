# Chetter

Chetter is an Open Source system written in Go for running autonomous AI development agents using standard harnesses (OpenCode, Claude Code, CodeWhale, Pi, Codex) in standard Docker or Kubernetes environments.
If you are looking for a simple(r) solution to self host standard autonomous coding agents, then Chetter should be interesting. Hop into [our Discord](https://discord.gg/KkZxKwSTvF) if you want to know more.

- **Submit tasks to standard harnesses** against Git repos with LLM agents in Docker containers
- **Track live progress** — streaming events, logs, session transcripts
- **Automation** — cron triggers, generic webhooks, PR review webhooks, GitHub PR/issue/comment responders
- **Pause and resume** — pause agent sessions and resume on the same runner with follow-up prompts
- **Manage via MCP or web UI** — web UI, MCP tools, team tokens, audit log, runner fleet health

## Design Principles

Chetter was built as a reaction towards the solutions available (E2B, Daytona, Modal etc) which often are **not Open Source** or want to lock you into them **hosting your agents**, need relatively **exotic infrastructure** like Firecracker VMs or use **custom harnesses** instead of industry standard harnesses.

Chetter instead ...

- **Is true Open Source.** Chetter was built for organisations that feel that the future of software development should be under their own control.
- **Uses Standard harnesses.** Chetter delegates agent execution to existing CLI tools — primarily OpenCode, with support for Claude Code, CodeWhale, Pi, and Codex. No custom agent runtime. We believe that the popular harnesses have the momentum and we also feel that constructing an autonomous agent should be done with the same tools that you use as an individual developer.
- **Deploys in Docker or Kubernetes.** Both the server and runners run on standard Docker or Kubernetes. No special infrastructure is needed for convenience execution. For a task security boundary, enable **gVisor**; plain Docker execution is not sandboxed against a malicious task.
- **Relies on GitHub-native orchestration.** Chetter integrates deeply with GitHub and uses PRs, issues, reviews, and comments to drive agent workflows — the same primitives developers already use.
- **Uses plain containers as environments.** The agent runs in a normal Docker container. You define the image with the tools and stack your project needs.
- **Is MCP and API first, web UI for observation.** The server has a full ConnectRPC API exposed also as MCP tools. There is also a web UI primarily for monitoring, inspection, and admin tasks.

## Quick Start

**Prerequisites**

- **Docker Engine** with the Compose plugin and **BuildKit/buildx** (the
  Dockerfiles use BuildKit-only features). Docker from
  [docs.docker.com](https://docs.docker.com/engine/install/) ships both; on
  Ubuntu's `docker.io` package install `docker-buildx` too:
  `sudo apt-get install -y docker.io docker-compose-v2 docker-buildx`
- `git`
- An LLM provider API key (e.g. `DEEPSEEK_API_KEY`) — set it in `.env`

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

**What you get:** a dev TiDB (bundled via `deploy/compose.local.yaml`,
single-node `unistore`, MySQL-wire compatible — the `chetter` database and
all migrations are created automatically on startup), the MCP server, and two
runners. To run your first task you also need a default **Git identity**
(`chetter_create_git_identity` + `chetter_set_git_identity_default` via any
MCP client).

> **⚠ Security note:** the quickstart runs agent tasks in **plain Docker,
> without gVisor sandboxing** (`compose.local.yaml` sets
> `CHETTER_ALLOW_UNISOLATED=true` and `USE_GVISOR=false`). That is fine for
> development, but it is **not a security boundary** against a malicious
> task. For a hardened deployment, enable gVisor — see
> [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) and "Verifying gVisor" below.

### Verifying gVisor

The server defaults to hardened mode: every task requires gVisor-enforced
isolation unless the deployment opts out. To go from the quickstart to a
sandboxed setup:

1. Install `runsc` on the host and register it with Docker, per
   [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) ("Docker + gVisor").
2. In `deploy/compose.local.yaml`, set `USE_GVISOR: "true"` for the runners
   (or drop the override) and remove `CHETTER_ALLOW_UNISOLATED`.
3. Recreate the stack (`docker compose up -d --force-recreate`).
4. Verify sandboxing is actually in effect:
   - The fleet reports isolation-capable runners (`chetter_runner_health`),
     and
   - a running task's container uses the `runsc` runtime:
     ```bash
     docker inspect <task-container> --format '{{.HostConfig.Runtime}}'   # → runsc
     ```
   - A task submitted with `isolation="required"` runs (instead of failing
     with `isolation_unavailable`).

### Validating the Quick Start

`ops/test-quickstart.sh` runs the full Quick Start from scratch on a
disposable Ubuntu 24.04 KVM VM and asserts every step — ideal before
releases:

```bash
DEEPSEEK_API_KEY=sk-... ./ops/test-quickstart.sh            # plain-Docker quickstart
DEEPSEEK_API_KEY=sk-... ./ops/test-quickstart.sh --gvisor   # ...then gVisor hardening
```

The default run provisions the VM (no Docker preinstalled), installs the
prerequisites, and validates: image build, compose up, database
auto-creation + migrations, MCP connect, fleet health, a real task
execution, and the self-test. `--gvisor` additionally installs `runsc`,
switches the stack to hardened mode and re-validates with
`isolation="required"` tasks (including the `HostConfig.Runtime == runsc`
check). Each step is reported PASS/FAIL; the VM is torn down afterwards
(`QS_KEEP_VM=1` keeps it for debugging). Requires `/dev/kvm`, libvirt
tooling, and sudo. See the script header for all options.

### Next Steps

- Configure the **GitHub App** for PR review and issue automation (webhook, label,
  `/chetter-review`): see [docs/TRIGGERS.md](docs/TRIGGERS.md).
- Point Chetter at a **definitions repo** for agents, skills, triggers, task
  templates, and the model catalog: see [docs/CONFIGURATION.md](docs/CONFIGURATION.md).
- Enable **gVisor** for a task security boundary: see [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).
- For production Kubernetes, see [docs/EKS.md](docs/EKS.md); for local k3s
  validation, [docs/K3S.md](docs/K3S.md).

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

- [docs/README.md](docs/README.md) — full documentation index
- [docs/MANUAL.md](docs/MANUAL.md) — canonical operations guide (setup, config, MCP tools, env vars)
- [docs/FEATURES.md](docs/FEATURES.md) — shipped feature reference
- [docs/PLAN.md](docs/PLAN.md) — roadmap and milestones
- [docs/TRIGGERS.md](docs/TRIGGERS.md) — cron schedules and PR review automation
- [docs/CONFIGURATION.md](docs/CONFIGURATION.md) — definitions repo, model catalog, Git identities
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) — deployment and sandboxing (gVisor)
- [docs/EXECUTION.md](docs/EXECUTION.md) — execution backends: docker, kubernetes, local
- [runner/README.md](runner/README.md) — runner module: setup, resource limits, security model
- [web/README.md](web/README.md) — web UI module: stack and structure
- [CHANGELOG.md](CHANGELOG.md) — what's new

## Build From Source

```bash
make check && make build
```

## License

MIT
