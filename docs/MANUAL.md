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

## Why TiDB

Chetter uses [TiDB](https://www.pingcap.com/tidb/) as its sole database. TiDB speaks the MySQL wire protocol, so it works with Go's standard MySQL driver, but adds capabilities Chetter's roadmap depends on: vector search for semantic task/event retrieval, HTAP via TiFlash for fleet analytics, and TiDB Cloud for zero-ops managed deployments.

> **Local vs. real TiDB.** The bundled database in `deploy/compose.local.yaml` runs TiDB's single-container `unistore` *test* engine — convenient for local dev (it serves Chetter's plain MySQL-protocol workload), but it has no TiFlash, so vector search and HTAP do not run on it. Connect to a real TiDB via `DATABASE_DSN` for those features and for production.

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

## Deploying On Kubernetes

The runner uses a stateless pull model: it connects to the MCP server over HTTP, long-polls `ClaimTask` to pick up work, sends heartbeats, and reports task events. No special protocols, no broker, no runner pre-registration.

### MCP Server

```yaml
apiVersion: v1
kind: Service
metadata:
  name: chetter-mcp
spec:
  selector:
    app: chetter-mcp
  ports:
  - name: mcp
    port: 8080
    targetPort: 8080
  - name: web
    port: 8090
    targetPort: 8090
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chetter-mcp
spec:
  replicas: 2
  selector:
    matchLabels:
      app: chetter-mcp
  template:
    metadata:
      labels:
        app: chetter-mcp
    spec:
      containers:
      - name: mcp
        image: ghcr.io/flatout-works/chetter-mcp:main
        ports:
        - containerPort: 8080
        - containerPort: 8090
        envFrom:
        - secretRef:
            name: chetter-secrets
        env:
        - name: HTTP_ADDR
          value: ":8080"
        - name: WEB_ADDR
          value: ":8090"
        - name: DEFAULT_AGENT_IMAGE
          value: ghcr.io/flatout-works/chetter-runner:main
```

### Runners

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chetter-runner
spec:
  replicas: 4
  selector:
    matchLabels:
      app: chetter-runner
  template:
    metadata:
      labels:
        app: chetter-runner
    spec:
      containers:
      - name: runner
        image: ghcr.io/flatout-works/chetter-runner:main
        envFrom:
        - secretRef:
            name: chetter-secrets
        env:
        - name: CHETTER_SERVER_URL
          value: "http://chetter-mcp:8080"
        - name: RUNNER_LOCAL
          value: "true"
        - name: RUNNER_MAX_CONCURRENT
          value: "2"
```

Scaling is `kubectl scale deployment chetter-runner --replicas=8`. Each runner pod independently polls for tasks. The MCP server's `ClaimTask` uses `SELECT ... FOR UPDATE SKIP LOCKED` for atomic task assignment.

### gVisor On Kubernetes

See the [Sandbox Isolation](#sandbox-isolation) section for the DaemonSet that installs gVisor on cluster nodes and the RuntimeClass registration. On GKE, use [GKE Sandbox](https://cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) instead.

When `runtimeClassName: gvisor` is set on the runner pod, the runner container itself runs under gVisor. When `USE_GVISOR=true` is also set, agent containers spawned by the runner (via Docker) also use the `runsc` runtime.

### Local Kubernetes Testing With k3d

See [docs/testing/k3d-gvisor.md](testing/k3d-gvisor.md) for a complete walkthrough of deploying Chetter on a local k3d cluster with optional gVisor.

## Deploying With Docker + gVisor

Install `runsc` on the host and set `USE_GVISOR=true` to enable gVisor for agent containers.

### Install gVisor On The Host

```bash
curl -fsSL https://gvisor.dev/archive.key | \
  sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
  sudo tee /etc/apt/sources.list.d/gvisor.list
sudo apt-get update && sudo apt-get install -y runsc
sudo /usr/bin/runsc install
sudo systemctl restart docker
docker run --runtime=runsc --rm alpine dmesg  # verify: should show "Starting gVisor..."
```

### Enable In Compose

Add `USE_GVISOR=true` to `.env`:

```yaml
chetter-runner:
  environment:
    RUNNER_LOCAL: "true"
    USE_GVISOR: "true"
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
```

The runner passes `--runtime=runsc` to `docker run` when creating agent containers. Only the host Docker daemon needs `runsc` registered — the binary does not need to exist inside the runner container. The Docker socket mount is required because the runner shells out to `docker run`.

## Custom Dev Container Images

Tasks run inside a dev container image specified by `agent_image`. Chetter ships several variants, and you can create your own.

### Built-in Variants

| Variant | Image Tag | Contents |
|---------|-----------|----------|
| Golang (default) | `ghcr.io/flatout-works/chetter-runner:main` | Go, buf, sqlc, goose, govulncheck, osv-scanner, gh, hcloud, opencode, claude-code |
| Python | `ghcr.io/flatout-works/chetter-runner:python` | Python 3, pip, venv, ruff, mypy, pytest, opencode, claude-code |
| Node.js | `ghcr.io/flatout-works/chetter-runner:node` | Node 22, pnpm, TypeScript, eslint, prettier, opencode, claude-code |
| Rust | `ghcr.io/flatout-works/chetter-runner:rust` | rustup, cargo, clippy, rustfmt, cargo-audit, opencode, claude-code |
| Minimal | `ghcr.io/flatout-works/chetter-runner:minimal` | opencode, claude-code, git, curl — no language toolchain |

Use `agent_image` in a task, or set `DEFAULT_AGENT_IMAGE` on the server for a default.

### Creating A Custom Image

All images inherit from `chetter-runner-base` (except `minimal` which starts from `debian:bookworm-slim`). The base provides opencode, claude-code, git, and core tooling.

Create `runner/images/<name>/Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7
ARG BASE_IMAGE=ghcr.io/flatout-works/chetter-runner-base:main

FROM golang:1.26-bookworm AS runner-builder
ARG CACHEBUST
WORKDIR /src
COPY go.mod go.sum* ./
COPY gen/ ./gen/
COPY runner/go.mod runner/go.sum* ./runner/
WORKDIR /src/runner
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
WORKDIR /src
COPY runner/ ./runner/
WORKDIR /src/runner
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/runner ./cmd/runner

FROM golang:1.26-bookworm AS mcp-bridge-builder
WORKDIR /build
COPY runner/harness/mcp-bridge/main.go ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/mcp-bridge ./main.go

FROM ${BASE_IMAGE}

RUN apt-get update && apt-get install -y --no-install-recommends \
    my-language-runtime \
    && rm -rf /var/lib/apt/lists/*

COPY --from=runner-builder /out/runner /usr/local/bin/runner
COPY --from=mcp-bridge-builder /out/mcp-bridge /usr/local/bin/mcp-bridge
COPY runner/chetter-entrypoint.sh /usr/local/bin/chetter-entrypoint
COPY tools/skills/ /opt/opencode/.agents/skills/
COPY .opencode/agent/ /opt/opencode/.config/opencode/agent/
RUN chmod +x /usr/local/bin/runner /usr/local/bin/mcp-bridge /usr/local/bin/chetter-entrypoint \
    && chmod -R 755 /opt/opencode/.agents/skills /opt/opencode/.config/opencode/agent

ENV RUNNER_LOCAL=true
ENV RUNNER_WORKSPACE_ROOT=/var/lib/chetter-runner/workspaces
WORKDIR /var/lib/chetter-runner/workspaces
ENTRYPOINT ["chetter-entrypoint"]
```

Requirements:
- `opencode` in `$PATH` (or `claude` for Claude Code harness)
- `HOME=/opt/opencode`
- `chetter-entrypoint` as ENTRYPOINT

Build with `docker build -f runner/images/myvariant/Dockerfile -t my-org/chetter-runner:myvariant .` from the repo root.

### Image Contract

The runner injects these environment variables into every container:

| Variable | Description |
|----------|-------------|
| `TASK_ID` | Task identifier |
| `WORKSPACE` | Path to the cloned repo (typically `/workspace`) |
| `MCP_SOCKET_PATH` | Unix socket for the runner-bridge MCP server |
| `HOME` | Set to `/opt/opencode` |
| `XDG_CONFIG_HOME` | Set to `/opt/opencode/.config` |
| `CHETTER_AGENT_NAME` | Agent name from the task request |
| `CHETTER_MODEL_ID` | Resolved LLM model identifier |
| `CHETTER_RUNNER_IMAGE` | Image reference of the runner |
| `CHETTER_RUNNER_IMAGE_DIGEST` | Digest of the runner image |

Secrets (API keys) are forwarded automatically when set in the runner's environment.

## Arcane Deployment

Chetter's production deployment uses Arcane GitOps. GitHub Actions does not build Docker images.

Deployment flow:
1. Push to `main`.
2. GitHub Actions runs `make check`.
3. The workflow calls Arcane's API to sync GitOps, build images on wowbagger, push to GHCR, and redeploy the Chetter project.
4. Arcane redeploys containers from GHCR images.

Required GitHub repository secrets:
- `ARCANE_URL` — Arcane base URL (e.g. `https://wowbagger.krampe.se`)
- `ARCANE_API_KEY` — API key with project build/deploy permissions
- `ARCANE_CHETTER_PROJECT_ID` — Chetter project ID
- `ARCANE_CHETTER_GITOPS_ID` — GitOps sync ID

Arcane GitOps must use Compose path `compose.yaml` with directory sync enabled.

## Runner Concurrency

Each runner can handle multiple tasks simultaneously via `RUNNER_MAX_CONCURRENT`. Each task gets its own Docker container with its own port, so tasks are process-isolated even within a single runner.

| | Multiple tasks per runner | More runners |
|---|---|---|
| Overhead | One process, one heartbeat stream, one Docker connection | N× process overhead, N× heartbeats |
| Resource efficiency | Lower baseline CPU/memory when idle | Each runner consumes resources even when idle |
| Task pickup | Semaphore slot frees immediately | New runner must spin up |
| Blast radius | Runner crash/OOM kills all in-flight tasks | Only one task lost per runner failure |
| Debugging | Interleaved logs from concurrent tasks | Clean per-runner logs |

**Recommended:** `RUNNER_MAX_CONCURRENT=2` or `3` per runner pod. For production, 4 pods with `MAX_CONCURRENT=2` = 8 concurrent tasks, with only 2 tasks lost per pod failure.

## Sandbox Isolation

Chetter uses [gVisor](https://gvisor.dev/) (`runsc`) as its sandboxed execution runtime. gVisor provides an application kernel (the Sentry) written in Go that intercepts every system call the container makes and implements the Linux ABI in userspace. The application never touches the host kernel directly.

### Why gVisor Over Alternatives

| Requirement | gVisor | Kata Containers | Sysbox | Daytona |
|---|---|---|---|---|
| Isolation model | Application kernel | Micro-VM | User namespaces | VM + sandbox lifecycle |
| Streaming interaction | Yes | No (batch only) | Yes | Yes |
| Standard EKS/GKE (no custom AMI) | Yes (DaemonSet) | No (needs nested virt) | No (host daemon) | No (9+ service CP) |
| Kernel-level isolation | Yes | Yes | Partial | Yes |
| Integration complexity | Low | High | Medium | Very high |

**Kata Containers** were removed from Chetter — they cannot expose a port from the micro-VM for the interactive serve flow and require nested virtualization.

### Enabling gVisor On Kubernetes

Install with a DaemonSet that copies `runsc` onto each node and updates containerd:

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: gvisor-installer
  namespace: kube-system
spec:
  selector:
    matchLabels:
      app: gvisor-installer
  template:
    metadata:
      labels:
        app: gvisor-installer
    spec:
      hostPID: true
      containers:
      - name: installer
        image: gcr.io/gvisor-containers/runsc:latest
        securityContext:
          privileged: true
        volumeMounts:
        - name: host-bin
          mountPath: /host-bin
        - name: host-containerd
          mountPath: /etc/containerd
        command: ["/bin/sh", "-c"]
        args:
        - |
          cp /usr/local/bin/runsc /host-bin/runsc
          cp /usr/local/bin/containerd-shim-runsc-v1 /host-bin/containerd-shim-runsc-v1
          if ! grep -q "runsc" /etc/containerd/config.toml; then
            cat >> /etc/containerd/config.toml <<'EOF'

          [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
            runtime_type = "io.containerd.runsc.v1"
          EOF
            nsenter -t 1 -m systemctl restart containerd
          fi
          sleep infinity
      volumes:
      - name: host-bin
        hostPath:
          path: /usr/local/bin
      - name: host-containerd
        hostPath:
          path: /etc/containerd
```

Register the RuntimeClass:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
```

Set `runtimeClassName: gvisor` on runner pods. On GKE, use [GKE Sandbox](https://cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) instead — no DaemonSet needed.

### Trade-off

gVisor adds per-syscall latency because every call is intercepted by the Sentry. For coding agent workloads (file I/O, git, compilation, HTTP calls) this is negligible. For syscall-heavy workloads (databases, high-frequency networking) the overhead can be noticeable. Runners can fall back to standard `runc` by omitting `runtimeClassName: gvisor` from the pod spec.

### Network Isolation

Regardless of the container runtime, Chetter runners provide outbound network filtering via a transparent HTTP proxy and DNS proxy. The proxy enforces an allowlist of domains and blocks everything else.

## Related Docs

- [FEATURES.md](FEATURES.md) — current capability reference.
- [SCHEDULES.md](SCHEDULES.md) — cron trigger management.
- [REVIEWS.md](REVIEWS.md) — GitHub PR review automation.
- [HARNESSES.md](HARNESSES.md) — harness architecture.
- [PAUSED_SESSIONS.md](PAUSED_SESSIONS.md) — resumable sessions.
- [CONFIG_IN_GIT.md](CONFIG_IN_GIT.md) — configuration-as-code design.
