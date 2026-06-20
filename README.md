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
- Use the embedded web UI for tasks, triggers, runners, sessions, artifacts, and admin actions.
- Expose the whole control plane through a standard HTTP MCP endpoint.

## Why TiDB

Chetter uses [TiDB](https://www.pingcap.com/tidb/) as its sole database. TiDB speaks the MySQL wire protocol, so it works with Go's standard MySQL driver, but adds capabilities Chetter's roadmap depends on: vector search for semantic task/event retrieval, HTAP (Hybrid Transactional/Analytical Processing) via TiFlash for fleet analytics and dashboards, and TiDB Cloud (Starter/Essential) for zero-ops managed deployments. One database, one protocol, room to grow.

> **Local vs. real TiDB.** The bundled database in `deploy/compose.local.yaml` runs TiDB's single-container `unistore` *test* engine — convenient for local dev (it serves Chetter's plain MySQL-protocol workload), but it has no TiFlash, so **vector search and HTAP do not run on it**. To develop or validate those roadmap features — and to run in production — connect to a real TiDB via `DATABASE_DSN`. [TiDB Cloud Starter or Essential](https://www.pingcap.com/tidb-cloud/) give you a fully managed cluster with TiFlash and vector search built in, zero ops.

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

- `CHETTER_MCP_AUTH_TOKEN`
- At least one LLM provider key, such as `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `SYNTHETIC_API_KEY`, or `OPENCODE_API_KEY`

Always set `CHETTER_MCP_AUTH_TOKEN` to a long, random value; the server refuses to start without it.

### 3. Build and start

Images are built locally (publishing to a registry is deferred until proper
releases), so build them first, then start the stack:

```bash
./deploy/build.sh
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

`deploy/build.sh` builds the MCP, runner-base, and runner images in order (the
runner is `FROM chetter-runner-base`, so the base must exist first — a plain
`docker compose up` cannot build it because the base build is profile-gated).

This starts:

- TiDB for Chetter state
- The Chetter MCP server on port `18088`
- The Chetter web UI and ConnectRPC API on port `18089`
- Two runner containers that pick up tasks

The `deploy/compose.local.yaml` override adds the bundled TiDB service. If you
already have a TiDB instance, set `DATABASE_DSN` in `.env` and run only
`-f deploy/compose.yaml`.

### 4. Check It

```bash
curl http://localhost:18088/healthz
curl http://localhost:18089/healthz
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml ps
```

Open `http://localhost:18089` to use the web UI. Log in with the same token you set in `.env` as `CHETTER_MCP_AUTH_TOKEN`.

### 5. Connect Your AI Client

#### OpenCode

This repo includes ready-to-use OpenCode configuration at `.opencode/opencode.json`. It defines:

- **MCP connection** to the Chetter server
- **Slash commands:** `/chetter-status`, `/chetter-tasks`, `/chetter-submit`, `/chetter-triggers`, `/chetter-cancel`
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
  "git_ref": "main"
}
```

`agent_image` defaults to the locally built `chetter-runner:latest` (the
server's `DEFAULT_AGENT_IMAGE`); only set it to use a different runner image.

Set `GITHUB_TOKEN` in `.env` if runners need access to private repositories or need to create branches and pull requests.

## Trying Chetter Locally with k3d

[k3d](https://k3d.io/) runs [k3s](https://k3s.io/) (lightweight Kubernetes) inside Docker containers. It works on Linux, macOS, and Windows, installs in seconds, and cleans up with one command. It's the easiest way to test Chetter on Kubernetes locally.

Why k3d over alternatives:
- **k3s** (bare metal) — Linux-only, installs as a systemd service. Great for servers, but requires root and a Linux host.
- **kind** — Runs Kubernetes in Docker like k3d, but uses nested containerd (no Docker socket passthrough by default). Chetter runners need the Docker socket to launch agent containers.
- **minikube** — Heavier, runs in a VM by default. Works but slower to start and uses more resources.
- **k3d** — Best fit: k3s in Docker, Docker socket passthrough is straightforward, cross-platform, fast.

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (with 4+ GB RAM allocated)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [k3d](https://k3d.io/#installation) (`brew install k3d` or `curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash`)

### Step 1: Create a cluster

```bash
# Create a cluster with Docker socket mounted into agent nodes
k3d cluster create chetter \
  --agents 1 \
  -p "18088:80@loadbalancer" \
  --volume /var/run/docker.sock:/var/run/docker.sock@agent:0

# Verify
kubectl get nodes
```

This creates a 1-server + 1-agent cluster. The Docker socket is mounted into the agent node so the runner can spawn agent containers. Port `18088` maps to the k3d load balancer for MCP access. Use port-forwarding for the web UI while testing, or add a separate ingress that routes to service port `8090`.

### Step 2: Create the namespace and secrets

```bash
kubectl apply -f deploy/k8s/namespace.yaml

kubectl create secret generic chetter-secrets \
  --namespace=chetter \
  --from-literal=CHETTER_MCP_AUTH_TOKEN=your-token \
  --from-literal=DATABASE_DSN='root@tcp(tidb:4000)/chetter?parseTime=true' \
  --from-literal=GITHUB_TOKEN=your-gh-token \
  --from-literal=DEEPSEEK_API_KEY=your-key
```

### Step 3: Deploy TiDB

For local testing, run TiDB in the cluster:

```bash
kubectl apply -n chetter -f deploy/k3d/tidb.yaml
```

### Step 4: Deploy Chetter

```bash
# Apply the MCP server and runner manifests
kubectl apply -f deploy/k8s/mcp-service.yaml
kubectl apply -f deploy/k8s/mcp-deployment.yaml
kubectl apply -f deploy/k8s/runner-deployment.yaml

# Wait for everything to come up
kubectl -n chetter rollout status deployment/chetter-mcp
kubectl -n chetter rollout status deployment/chetter-runner
```

### Step 5: Expose the MCP server

For quick testing, use port-forward:

```bash
kubectl -n chetter port-forward deployment/chetter-mcp 18088:8080 18089:8090 &
curl http://localhost:18088/healthz
curl http://localhost:18089/healthz
```

For a persistent setup, add an Ingress:

```bash
kubectl apply -n chetter -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: chetter-mcp
  annotations:
    ingress.kubernetes.io/ssl-redirect: "false"
spec:
  rules:
  - http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: chetter-mcp
            port:
              number: 8080
EOF
```

Then the MCP server is available at `http://localhost:18088/mcp` (via the load balancer port mapped in step 1). The web UI is available at `http://localhost:18089` when using the port-forward command above, or through a separate ingress that routes to service port `8090`.

### Step 6: Verify

```bash
# Check all pods are running
kubectl -n chetter get pods

# Health check
curl http://localhost:18088/healthz

# Watch runner logs
kubectl -n chetter logs -f deployment/chetter-runner
```

### Step 7: Connect your AI client

Use `http://localhost:18088/mcp` as your MCP endpoint. See [Quick Start](#quick-start) step 5 for client-specific setup.

Use `http://localhost:18089` for the web UI when port-forwarding the web port.

### Optional: Enable gVisor

gVisor requires `runsc` installed on the host. This only works on Linux hosts (not Docker Desktop on macOS/Windows):

```bash
# Install gVisor on the host
curl -fsSL https://gvisor.dev/archive.key | \
  sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
  sudo tee /etc/apt/sources.list.d/gvisor.list
sudo apt-get update && sudo apt-get install -y runsc

# Register runsc with Docker
sudo /usr/bin/runsc install
sudo systemctl restart docker

# Restart the k3d cluster so it picks up the new runtime
k3d cluster stop chetter && k3d cluster start chetter

# Enable gVisor for agent containers
kubectl -n chetter set env deployment/chetter-runner USE_GVISOR=true
```

### Cleaning up

```bash
# Delete the entire cluster (removes all resources)
k3d cluster delete chetter
```

### Using bare k3s instead

If you're on a Linux machine and prefer k3s as a systemd service (e.g., a home server or CI VM):

```bash
curl -sfL https://get.k3s.io | sh -
mkdir -p ~/.kube && sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config && sudo chown $(id -u):$(id -g) ~/.kube/config
kubectl get nodes
```

Then follow steps 2–7 above. The manifests are the same. To uninstall: `/usr/local/bin/k3s-uninstall.sh`.

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
|---|---|---|
| `chetter_submit_task` | Submit a one-off development task |
| `chetter_task_status` | Fetch persisted task status and result |
| `chetter_list_tasks` | List recent tasks |
| `chetter_create_trigger` | Create a trigger (cron schedule or PR review webhook) |
| `chetter_update_trigger` | Update a trigger by name |
| `chetter_list_triggers` | List triggers, optionally filtered by type |
| `chetter_delete_trigger` | Delete a trigger by name |
| `chetter_run_trigger` | Run a cron trigger immediately |
| `chetter_cancel_task` | Cancel a pending or running task |
| `chetter_clear_queue` | Clear queued tasks (admin only) |
| `chetter_task_events` | Fetch full event history for a task |
| `chetter_task_progress` | Fetch distilled task progress |
| `chetter_task_latest_event` | Fetch latest event for a task |
| `chetter_runner_health` | Derive fleet health and runner status |
| `chetter_create_token` | Create an API token for a team and user |
| `chetter_list_tokens` | List API tokens with user and team info |
| `chetter_delete_token` | Delete an API token by name |
| `chetter_create_team` | Create a new team (admin only) |
| `chetter_list_teams` | List all teams (admin only) |
| `chetter_delete_team` | Delete a team and cascade to its users, tokens, tasks, and schedules (admin only) |
| `chetter_list_users` | List users, optionally filtered by team name (admin only) |
| `chetter_list_schedule_runs` | List schedule runs for the current team, optionally filtered by schedule name |

## Configuration

### Main Environment Variables

| Variable | Description |
|---|---|
| `CHETTER_MCP_AUTH_TOKEN` | Bearer token required by `/mcp` |
| `HTTP_ADDR` | MCP, runner RPC, webhook, and health listen address, default `:8080` |
| `WEB_ADDR` | Web UI and ConnectRPC API listen address, default `:8090` |
| `DATABASE_DSN` | Optional TiDB DSN override |
| `DEFAULT_AGENT_IMAGE` | Default dev container image for tasks and triggers |
| `GITHUB_TOKEN` | Optional token for private repos and GitHub write operations |
| `GITHUB_APP_ID` | GitHub App ID for PR review webhooks |
| `GITHUB_APP_PRIVATE_KEY_B64` | Base64-encoded GitHub App private key (PEM) |
| `GITHUB_INSTALLATION_ID` | GitHub App installation ID |
| `GITHUB_WEBHOOK_SECRET` | HMAC-SHA256 secret for webhook signature verification |
| `ARCANE_SERVER_URL` | Arcane platform API URL for image builds |
| `ARCANE_API_KEY` | Arcane platform API key |
| `OPENAI_API_KEY` | Optional OpenAI key for runner agents |
| `DEEPSEEK_API_KEY` | Optional DeepSeek key for runner agents |
| `SYNTHETIC_API_KEY` | Optional Synthetic key for runner agents |
| `OPENCODE_API_KEY` | Optional OpenCode provider key |
| `MEM9_API_KEY` | Optional Mem9 memory provider key |
| `USE_GVISOR` | Set to `true` to run agent containers under gVisor (`--runtime=runsc`) |
| `CHETTER_RUNNER_IMAGE_DIGEST` | Optional pinned image digest for PR signature footers |

If `DATABASE_DSN` is not set, use `deploy/compose.local.yaml` to add the bundled
TiDB service. Production deployments should usually set `DATABASE_DSN` and run
only `deploy/compose.yaml`.

### Runner Environment Variables

| Variable | Description |
|---|---|
| `RUNNER_LOCAL` | Set to `true` for local/Docker execution mode (default) |
| `CHETTER_SERVER_URL` | MCP server URL (e.g. `http://chetter-mcp:8080`) |
| `CHETTER_RUNNER_AUTH_TOKEN` | Auth token for runner → MCP server communication |
| `RUNNER_MAX_CONCURRENT` | Max concurrent tasks per runner (default: 2) |
| `USE_GVISOR` | Set to `true` to run agent containers under gVisor (`--runtime=runsc`) |
| `CHETTER_RUNNER_IMAGE` | Runner image reference for task signature footers |
| `CHETTER_RUNNER_IMAGE_DIGEST` | Pinned image digest for task signature footers |

### Runner Concurrency

Each runner can handle multiple tasks simultaneously via `RUNNER_MAX_CONCURRENT`. Each task gets its own Docker container with its own port, so tasks are process-isolated even within a single runner.

**Higher concurrency per runner (`MAX_CONCURRENT=5`) vs. more runners (`MAX_CONCURRENT=1`):**

| | Multiple tasks per runner | More runners |
|---|---|---|
| Overhead | One process, one heartbeat stream, one Docker connection | N× process overhead, N× heartbeats |
| Resource efficiency | Lower baseline CPU/memory when idle | Each runner consumes resources even when idle |
| Scaling | Change one env var | Provision more pods |
| Task pickup | Semaphore slot frees immediately | New runner must spin up |
| Blast radius | Runner crash/OOM kills all in-flight tasks | Only one task lost per runner failure |
| Isolation | One greedy container can starve others | Full isolation per runner |
| Debugging | Interleaved logs from concurrent tasks | Clean per-runner logs |
| Docker SPOF | Docker hang affects all tasks | Only one task affected |

**Recommended:** `RUNNER_MAX_CONCURRENT=2` or `3` per runner pod. This gives good density without excessive blast radius. For production, a mix works well — e.g., 4 pods with `MAX_CONCURRENT=2` = 8 concurrent tasks, with only 2 tasks lost per pod failure.

## Deploying on Kubernetes

The runner uses a stateless pull model: it connects to the MCP server over HTTP, long-polls `ClaimTask` to pick up work, sends heartbeats, and reports task events. No special protocols, no broker, no runner pre-registration. This maps directly to a Kubernetes Deployment.

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
  replicas: 4   # scale with kubectl scale deployment chetter-runner --replicas=8
  selector:
    matchLabels:
      app: chetter-runner
  template:
    metadata:
      labels:
        app: chetter-runner
    spec:
      runtimeClassName: gvisor   # optional — requires gVisor on the nodes
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
        - name: USE_GVISOR
          value: "true"   # agent containers will use --runtime=runsc
```

Scaling is just `kubectl scale`. Each runner pod independently polls for tasks. Adding or removing runners does not require any coordination — the MCP server's `ClaimTask` uses `SELECT ... FOR UPDATE SKIP LOCKED` for atomic task assignment.

### gVisor on Kubernetes

See the [Sandbox Isolation](#sandbox-isolation) section for the DaemonSet that installs gVisor on cluster nodes and the RuntimeClass registration. On GKE, use [GKE Sandbox](https://cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) instead — no DaemonSet needed.

When `runtimeClassName: gvisor` is set on the runner pod, the runner container itself runs under gVisor. When `USE_GVISOR=true` is also set, agent containers spawned by the runner (via Docker) also use the `runsc` runtime. These are independent: you can run the runner under gVisor while agents use runc, or vice versa.

For local Kubernetes testing, see [Trying Chetter Locally with k3d](#trying-chetter-locally-with-k3d).

## Deploying with Docker + gVisor

When running Chetter via Docker Compose, you can enable gVisor for agent containers by installing `runsc` on the host and setting `USE_GVISOR=true`.

### Install gVisor on the host

```bash
# Add the gVisor repository
curl -fsSL https://gvisor.dev/archive.key | \
  sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
  sudo tee /etc/apt/sources.list.d/gvisor.list
sudo apt-get update && sudo apt-get install -y runsc

# Register runsc with Docker
sudo /usr/bin/runsc install
sudo systemctl restart docker

# Verify
docker run --runtime=runsc --rm alpine dmesg
# Should show "Starting gVisor..."
```

### Enable in Docker Compose

Add `USE_GVISOR=true` to `.env` or the runner service environment:

```yaml
chetter-runner:
  image: ghcr.io/flatout-works/chetter-runner:main
  environment:
    RUNNER_LOCAL: "true"
    USE_GVISOR: "true"   # agent containers will use --runtime=runsc
    # ... other vars
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock   # required for docker run
```

When `USE_GVISOR=true`, the runner passes `--runtime=runsc` to `docker run` when starting agent containers. The runner container itself does not need gVisor — only the host Docker daemon needs the `runsc` runtime registered.

The Docker socket mount is required because the runner shells out to `docker run` to create agent containers. The `runsc` binary is resolved by the host Docker daemon, so it does not need to exist inside the runner container.

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
| `internal/store/` | TiDB schema and persistence |
| `internal/repository/` | sqlc-generated database queries |
| `db/` | goose migrations and sqlc query files |
| `internal/service/` | MCP tools and task orchestration |
| `internal/webhook/` | Optional GitHub webhook handling |
| `deploy/compose.yaml` | Portable Docker Compose stack using published GHCR images |
| `runner/` | Runner runtime, image Dockerfiles, and entrypoint |
| `triggers/` | Active production triggers |
| `triggers-examples/` | Example trigger templates |
| `tools/skills/` | OpenCode skills baked into the runner image |

## Sandbox Isolation

Chetter runners execute AI-generated code. The isolation strategy is driven by three practical requirements:

1. **Works on standard managed Kubernetes** (EKS, GKE) without custom AMIs or node-level modifications beyond a DaemonSet.
2. **Supports streaming interaction with the agent** — real-time events, session management, and progress tracking via the OpenCode serve API. Batch-only runtimes break this.
3. **Provides decent security** — stronger than plain namespace isolation, without requiring VMs or custom host kernels.

### gVisor is the chosen path

Chetter uses [gVisor](https://gvisor.dev/) (`runsc`) as its sandboxed execution runtime. gVisor provides an application kernel (the Sentry) written in Go that intercepts every system call the container makes and implements the Linux ABI in userspace. The application never touches the host kernel directly. This gives kernel-level isolation without VMs, without custom AMIs, and without sacrificing streaming interaction.

The runner integrates gVisor through containerd — the same `ctr` interface already used for other runtimes, just with `--runtime io.containerd.runsc.v1` instead of `--runtime io.containerd.runc.v2`. Normal container networking works, so the interactive `opencode serve` flow (create session, send prompt, watch events, export session) functions exactly as it does with plain Docker.

### Why gVisor over alternatives

| Requirement | gVisor | Kata Containers | Sysbox | BoxLite | Daytona (self-hosted) |
|---|---|---|---|---|---|
| Isolation model | Application kernel (syscall interception) | Micro-VM (dedicated guest kernel) | User namespaces + procfs/sysfs virtualization | Micro-VM (Firecracker-class) | VM + full sandbox lifecycle |
| Streaming interaction | Yes | No (batch only) | Yes | Yes (via exec + port forwarding) | Yes (via Toolbox API) |
| Standard EKS/GKE (no custom AMI) | Yes (DaemonSet install) | No (needs nested virt + custom shim) | No (needs host-installed daemons) | No (library model, KVM required) | No (9+ service control plane) |
| Kernel-level isolation | Yes | Yes | Partial (shared host kernel) | Yes | Yes |
| Integration complexity | Low (swap containerd runtime flag) | High (netns plumbing, no port mapping) | Medium (host install required) | High (new execution mode) | Very high (full platform) |

**Kata Containers** were removed from Chetter. They cannot expose a port from the micro-VM back to the runner host through `ctr`, so the interactive serve flow is impossible — Kata runs the agent as a batch command and scrapes stdout. They also require nested virtualization and custom AMIs on managed Kubernetes, defeating operational simplicity.

**Sysbox** provides good isolation (user namespaces, virtualized `/proc` and `/sys`) and supports streaming, but must be installed on the host (`sysbox-runc`, `sysbox-mgr`, `sysbox-fs`). This rules out managed Kubernetes without custom AMIs.

**BoxLite** offers hardware-level micro-VM isolation with an embeddable library model, but requires KVM on the host and uses a SDK interaction model rather than containerd, requiring a new Chetter execution mode.

**Daytona** (self-hosted) provides the most complete sandboxing platform, but requires deploying its full control plane (PostgreSQL, Redis, MinIO, NestJS API, proxy, SSH gateway, registry). This is a heavy infrastructure dependency for what Chetter needs.

### Enabling gVisor on a standard Kubernetes cluster

gVisor can be installed on any Kubernetes cluster using a DaemonSet — no custom AMIs required. The DaemonSet copies the `runsc` binary and `containerd-shim-runsc-v1` onto each node, updates the containerd configuration, and restarts containerd.

**1. Install gVisor on the nodes**

Apply the DaemonSet that installs `runsc` on every node:

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

**2. Register the RuntimeClass**

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
```

**3. Annotate runner pods**

Set `runtimeClassName: gvisor` on the Chetter runner pod spec. Pods without this annotation continue to use the default `runc` runtime.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: chetter-runner
spec:
  runtimeClassName: gvisor
  containers:
  - name: runner
    image: ghcr.io/flatout-works/chetter-runner:main
    # ...
```

**4. GKE shortcut**

On Google Kubernetes Engine, gVisor is available as a built-in feature. Create a node pool with gVisor enabled and GKE handles the rest — no DaemonSet needed. See [GKE Sandbox](https://cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) for details.

### The trade-off

gVisor adds per-syscall latency because every call is intercepted by the Sentry. For coding agent workloads (file I/O, git, compilation, HTTP calls) this is negligible. For syscall-heavy workloads (databases, high-frequency networking) the overhead can be noticeable. If that becomes a problem, runners can fall back to standard `runc` by omitting `runtimeClassName: gvisor` from the pod spec.

### Network isolation

Regardless of the container runtime, Chetter runners provide outbound network filtering via a transparent HTTP proxy and DNS proxy. The proxy enforces an allowlist of domains and blocks everything else. This is independent of gVisor and works in all execution modes.

## Custom Dev Container Images

Tasks run inside a "dev container" image specified by `agent_image`. Chetter ships several built-in variants, but you can create your own.

### Built-in Variants

| Variant | Image Tag | Contents |
|---------|-----------|----------|
| Golang (default) | `ghcr.io/flatout-works/chetter-runner:main` | Go, buf, sqlc, goose, govulncheck, osv-scanner, gh, hcloud, opencode, claude-code |
| Python | `ghcr.io/flatout-works/chetter-runner:python` | Python 3, pip, venv, ruff, mypy, pytest, opencode, claude-code |
| Node.js | `ghcr.io/flatout-works/chetter-runner:node` | Node 22, pnpm, TypeScript, eslint, prettier, opencode, claude-code |
| Rust | `ghcr.io/flatout-works/chetter-runner:rust` | rustup, cargo, clippy, rustfmt, cargo-audit, opencode, claude-code |
| Minimal | `ghcr.io/flatout-works/chetter-runner:minimal` | opencode, claude-code, git, curl — no language toolchain |

### Using an Image in a Task

Pass the image reference via `agent_image`:

```json
{
  "prompt": "Refactor the FastAPI endpoints and add type hints.",
  "git_url": "https://github.com/my-org/my-api",
  "agent_image": "ghcr.io/flatout-works/chetter-runner:python"
}
```

Or set a default in the server config with `DEFAULT_AGENT_IMAGE`.

### Creating a Custom Image

All images inherit from `chetter-runner-base` (except `minimal` which starts from `debian:bookworm-slim`). The base provides opencode, claude-code, git, and core tooling — you just add your language stack.

**1. Create a Dockerfile**

Put it under `runner/images/<name>/Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7
ARG BASE_IMAGE=ghcr.io/flatout-works/chetter-runner-base:main

# Build stages (copy from any existing variant)
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

# Final image: base + your tools + app layer
FROM ${BASE_IMAGE}

# --- Add your language/tool stack here ---
RUN apt-get update && apt-get install -y --no-install-recommends \
    my-language-runtime \
    && rm -rf /var/lib/apt/lists/*
# ------------------------------------------

# App layer (required — same for all variants)
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

The only hard requirements for the image are:

- `opencode` binary in `$PATH` (or `claude` for the Claude Code harness)
- `HOME=/opt/opencode` set in the environment
- The `chetter-entrypoint` as ENTRYPOINT

**2. Build it**

```bash
# From the repo root
docker build -f runner/images/myvariant/Dockerfile -t my-org/chetter-runner:myvariant .
```

**3. Push to a registry**

```bash
docker push my-org/chetter-runner:myvariant
```

**4. Use it**

```json
{
  "prompt": "Fix the build pipeline.",
  "git_url": "https://github.com/my-org/my-repo",
  "agent_image": "my-org/chetter-runner:myvariant"
}
```

### Adding a Makefile Target

For convenience, add a build target to the root `Makefile`:

```makefile
docker-build-myvariant:
	docker build -f runner/images/myvariant/Dockerfile -t $(RUNNER_IMAGE)-myvariant .
```

### Image Contract

The runner injects these environment variables into every container at runtime:

| Variable | Description |
|----------|-------------|
| `TASK_ID` | The task identifier |
| `WORKSPACE` | Path to the cloned repo (typically `/workspace`) |
| `MCP_SOCKET_PATH` | Unix socket for the runner-bridge MCP server |
| `HOME` | Set to `/opt/opencode` |
| `XDG_CONFIG_HOME` | Set to `/opt/opencode/.config` |
| `CHETTER_AGENT_NAME` | Agent name from the task request |
| `CHETTER_MODEL_ID` | Resolved LLM model identifier |
| `CHETTER_RUNNER_IMAGE` | Image reference of the runner itself |
| `CHETTER_RUNNER_IMAGE_DIGEST` | Digest of the runner image |

Secrets (API keys) are forwarded automatically when set in the runner's environment.

## Build From Source

```bash
make check
make build
```

Open the embedded web UI from a local build:

```bash
MCP_AUTH_TOKEN=admin-token \
CHETTER_TOKEN=admin-token \
CHETTER_WEB_URL=http://localhost:8090 \
./bin/chetterctl web
```

`chetterctl token ...` uses `CHETTER_API_URL` for the ConnectRPC API URL, defaulting to `http://localhost:8090`. `chetterctl web` uses `CHETTER_WEB_URL`, also defaulting to `http://localhost:8090`.

Build images locally:

```bash
make docker-build-mcp
make docker-build-runner-base
make docker-build-runner
```

## License

MIT
