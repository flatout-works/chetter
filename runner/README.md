# Chetter Runner

Runs agent harnesses (OpenCode) inside Docker containers with optional gVisor sandboxing for strong isolation, while proxying privileged operations (git and HTTP) through a runner-managed MCP server.

## Architecture

```
Worker Node (Docker installed, optional gVisor/runsc)
│
├── Docker daemon (/var/run/docker.sock)
├── [optional] runsc runtime (gVisor, installed via DaemonSet)
├── iptables kernel modules
└── Runner Container (--privileged, mounts host resources)
    ├── ConnectRPC client → Chetter control plane
    ├── Git engine (SSH keys / PAT)
    ├── MCP Server (Unix socket per task)
    ├── Transparent HTTP Proxy (:18080)
    │
    └── docker → Docker daemon → [runsc | runc] → Agent Container
                                          │
                                   ┌──────┴──────┐
                                   │   Agent     │ (OpenCode serve)
                                   │  Container  │
                                   └─────────────┘
```

> **Important:** The runner requires Docker on the host. In Kubernetes, mount the Docker socket from the node. For sandbox isolation, install gVisor (`runsc`) on worker nodes — the runner passes `--runtime=runsc` when `USE_GVISOR=true`.

## Prerequisites

### Hardware Requirements

| Requirement | Why |
|-------------|-----|
| >2 GB RAM free per task | Each agent container needs memory |
| x86_64 or ARM64 | Docker supported architectures |
| **gVisor**: x86_64 or ARM64 Linux only | `runsc` does not support macOS/Windows |

### Software Prerequisites (Host Installation)

The following must be installed on the **host machine** (not inside the runner container). The runner must run as **root** (or with `CAP_NET_ADMIN` + access to `/var/run/docker.sock`).

#### 1. Docker

```bash
curl -fsSL https://get.docker.com | sh
sudo systemctl enable docker
```

Verify:
```bash
docker version
```

#### 2. gVisor (Optional — for sandbox isolation)

gVisor provides kernel-level isolation by intercepting syscalls in userspace. No KVM required.

```bash
# Install runsc
curl -fsSL https://gvisor.dev/archive.key | sudo gcr-keyring add -
sudo add-apt-repository "deb https://storage.googleapis.com/gvisor/releases stable main"
sudo apt-get update && sudo apt-get install -y runsc

# Configure Docker to use runsc
sudo runsc install
sudo systemctl restart docker
```

Verify:
```bash
docker run --runtime=runsc --rm alpine uname -a
```

For Kubernetes, install gVisor via DaemonSet (see `deploy/k8s/gvisor-runtimeclass.yaml`).

#### 3. Network Tools (for runner)

```bash
sudo apt-get install -y iptables iproute2 socat
```

#### 4. Chetter Control Plane

Start the Chetter MCP server and configure `server.url` in `runner.yaml`, or set
`CHETTER_SERVER_URL` when using the container entrypoint.

## Building the Runner

```bash
cd runner/
go mod tidy
go build -o runner ./cmd/runner
```

## Running the Runner (Development / Local Mode)

For testing **without Docker** (spawns plain local processes, no container isolation):

```bash
export RUNNER_LOCAL=true
./runner -config runner.yaml
```

Useful for development and CI smoke tests where Docker is not available.

## Running the Runner (Production / Docker Mode)

### Without gVisor (default)

```bash
sudo ./runner -config runner.yaml
```

Or as a privileged container:
```bash
# Build image from the repository root
docker build -f runner/Dockerfile.runner -t chetter/runner .

# Run with host Docker socket.
docker run -d --name chetter-runner \
  --privileged \
  -e CHETTER_SERVER_URL=http://host.docker.internal:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/runner:/var/lib/runner \
  -v "$PWD/runner.docker.yaml:/etc/runner/runner.yaml:ro" \
  -p 18080:18080 \
  chetter/runner
```

### With gVisor sandboxing

Set `USE_GVISOR=true` to make the runner pass `--runtime=runsc` to Docker. This runs each agent container inside a gVisor sandbox with its own userspace kernel.

```bash
docker run -d --name chetter-runner \
  --privileged \
  -e CHETTER_SERVER_URL=http://host.docker.internal:8080 \
  -e USE_GVISOR=true \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/runner:/var/lib/runner \
  -v "$PWD/runner.docker.yaml:/etc/runner/runner.yaml:ro" \
  -p 18080:18080 \
  chetter/runner
```

> **Note:** gVisor only works on Linux hosts. It is not available on Docker Desktop for macOS or Windows.

If the container exits immediately, check `docker logs chetter-runner`. Common causes are a missing `server.url` or lack of access to the mounted Docker socket.

## Sending a Task

Submit tasks through the Chetter MCP server using `chetter_submit_task`. Runners
claim queued tasks from the control plane over ConnectRPC.

## Supported Harnesses

| Harness | Mode | Status |
|---------|------|--------|
| **OpenCode** | `opencode serve` (interactive, HTTP API) | **Working — Docker + local mode** |
| **Niffler** | MCP socket integration | Planned — library patch to add `--mcp-socket` agent mode |

Unmodified harnesses work for public workflows (HTTP through proxy, workspace access, bash). Private git push requires harness to call MCP tools (`git_push`).

## Execution Modes

| Mode | Runtime | Isolation | Interactive | Platform |
|------|---------|-----------|-------------|----------|
| `local` | Subprocess | None | Yes (opencode serve) | Any |
| `docker` | Docker CLI + runc | Process | Yes (opencode serve) | Any |
| `docker` + gVisor | Docker CLI + runsc | Kernel (syscall filter) | Yes (opencode serve) | Linux only |

## Security Model

| Layer | Implementation |
|-------|---------------|
| Container Isolation | Docker (runc) or gVisor (runsc) |
| Network Lockdown | iptables REDIRECT + DNS proxy |
| No Credentials in Container | Git/SSH keys stay in runner |
| LLM Key | Inside container (known tradeoff: prompt exfiltration possible) |
| Proxy Filtering | SNI-based allowlist/blocklist |

## Runner Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CHETTER_SERVER_URL` | (required) | Control plane URL |
| `CHETTER_RUNNER_AUTH_TOKEN` | | Auth token (also checks `MCP_AUTH_TOKEN`, `CHETTER_MCP_AUTH_TOKEN`) |
| `RUNNER_LOCAL` | `false` | Run agents as local subprocesses (no Docker) |
| `USE_GVISOR` | `false` | Pass `--runtime=runsc` to Docker for gVisor sandboxing |
| `MAX_CONCURRENT` | `10` | Max parallel tasks |

## Troubleshooting

**`Cannot connect to the Docker daemon`**
→ Ensure Docker is running and the socket is mounted:
```bash
docker info
ls -la /var/run/docker.sock
```

**`docker: Error: runtime "runsc" not found`**
→ Install gVisor on the host:
```bash
sudo apt-get install -y runsc
sudo runsc install
sudo systemctl restart docker
```

**`iptables: Permission denied` in runner**
→ Runner must run as root or with `CAP_NET_ADMIN`.

**Agent container cannot reach proxy**
→ Check iptables rules and that the proxy is listening on `:18080`:
```bash
sudo iptables -t nat -L -n | grep 18080
```

## Development Plan

| Phase | Status | Description |
|-------|--------|-------------|
| 1 — Core + Proxy | Done | MCP server, workspace, proxy, config |
| 2 — Docker execution | Done | Docker CLI, container spawn, interactive serve |
| 3 — Network isolation | Done | Per-task bridge, iptables REDIRECT, DNS proxy |
| 4 — OpenCode Adapter | Done | `opencode serve` in local + Docker mode |
| 5 — Skills + Backend Harness | Done | Agent skill injection, backend developer Docker image |
| 6 — gVisor Sandbox | Done | `--runtime=runsc` flag, K8s DaemonSet installer |
| 7 — Niffler Patch | Planned | MCP client agent mode |
