# Chetter Runner

Runs agent harnesses inside Docker containers. Plain Docker execution is for trusted or convenience workloads; enable gVisor for a task security boundary.

## Architecture

```
Worker Node (Docker installed, optional gVisor/runsc)
│
├── Docker daemon (/var/run/docker.sock)
├── [optional] runsc runtime (gVisor, installed via DaemonSet)
└── Runner Container (mounts the Docker socket and workspace storage)
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

> **Security:** The runner requires Docker on the host. In Kubernetes, mount the Docker socket from the node. Plain Docker execution is not a security boundary, even when proxy or DNS filtering is configured. For untrusted tasks, install gVisor (`runsc`) on worker nodes and set `USE_GVISOR=true`; the runner then passes `--runtime=runsc`.

## Prerequisites

### Hardware Requirements

| Requirement | Why |
|-------------|-----|
| >2 GB RAM free per task | Each agent container needs memory |
| x86_64 or ARM64 | Docker supported architectures |
| **gVisor**: x86_64 or ARM64 Linux only | `runsc` does not support macOS/Windows |

### Software Prerequisites (Host Installation)

The following must be installed on the **host machine** (not inside the runner container). The runner needs access to `/var/run/docker.sock`.

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

#### 3. Chetter Control Plane

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
export EXECUTION_BACKEND=local
./runner -config runner.yaml
```

Useful for development and CI smoke tests where Docker is not available.

## Running the Runner (Production / Docker Mode)

### Without gVisor (default)

Use this mode only for trusted workloads or local convenience. A plain Docker
container does not provide Chetter's task security boundary.

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

Use this mode for untrusted workloads and when tasks need an isolation boundary.

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

## Container resource limits

Optionally cap the memory, CPU, and PID count of every agent task container so a single misbehaving task cannot exhaust the host. Limits are applied consistently to the serve, resume, and RPC container paths and only emit a Docker flag when the value is set, so unset limits leave behavior unchanged.

Configure them in `runner.yaml`:

```yaml
execution:
  container_memory: 512m   # passed to docker --memory and --memory-swap
  container_cpu: 2          # passed to docker --cpus (decimal allowed, e.g. 1.5)
  container_pids: 256       # passed to docker --pids-limit
```

Or via environment variables (which override the YAML values when set):

| Variable | Default | Description |
|----------|---------|-------------|
| `CHETTER_CONTAINER_MEMORY` | (unset) | Memory limit, e.g. `512m`, `2g` |
| `CHETTER_CONTAINER_CPU` | (unset) | CPU quota in cores, e.g. `1.5` |
| `CHETTER_CONTAINER_PIDS` | (unset) | Maximum number of PIDs, e.g. `256` |

**Requirements:**

- `container_memory`, `container_cpu`, and `container_pids` must be greater than or equal to 0; negative values and unparseable environment overrides fail configuration validation at startup.
- `--cpus` and `--pids-limit` are supported by the Docker daemon and are compatible with the gVisor (`runsc`) runtime.
- A `container_cpu` of `0` means "unset" (no `--cpus` flag); use a positive value to enforce a quota.

## Sending a Task

Submit tasks through the Chetter MCP server using `chetter_submit_task`. Runners
claim queued tasks from the control plane over ConnectRPC.

## Supported Harnesses

| Harness | Mode | Status |
|---------|------|--------|
| **OpenCode** | `opencode serve` (interactive, HTTP API) | **Working - Docker, Kubernetes, and local mode** |
| **Niffler** | MCP socket integration | Planned — library patch to add `--mcp-socket` agent mode |

Unmodified harnesses work for public workflows (HTTP through proxy, workspace access, bash). Private git push requires harness to call MCP tools (`git_push`).

## Execution Modes

| Mode | Runtime | Isolation | Interactive | Platform |
|------|---------|-----------|-------------|----------|
| `local` | Subprocess | None | Yes (opencode serve) | Any |
| `docker` | Docker CLI + runc | Convenience only; not a security boundary | Yes (opencode serve) | Any |
| `docker` + gVisor | Docker CLI + runsc | Task security boundary (userspace kernel) | Yes (opencode serve) | Linux only |
| `kubernetes` + gVisor | Kubernetes Pod + runsc | Task security boundary (userspace kernel) | Yes (opencode serve) | Linux cluster |

## Security Model

| Layer | Implementation |
|-------|---------------|
| Task security boundary | gVisor (`runsc`) only |
| Plain Docker or local mode | Trusted/convenience execution only |
| No Credentials in Container | Git/SSH keys stay in runner |
| LLM Key | Inside container (known tradeoff: prompt exfiltration possible) |
| Proxy/DNS filtering | Operational controls; not a sandbox boundary |

## Runner Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CHETTER_SERVER_URL` | (required) | Control plane URL |
| `CHETTER_RUNNER_AUTH_TOKEN` | | Auth token (also checks `CHETTER_RUNNER_RPC_TOKEN`, `MCP_AUTH_TOKEN`, `CHETTER_MCP_AUTH_TOKEN`) |
| `EXECUTION_BACKEND` | `docker` | `docker`, `kubernetes`, or development-only `local` execution |
| `USE_GVISOR` | `false` | Pass `--runtime=runsc` to Docker for gVisor sandboxing |
| `MAX_CONCURRENT` | `10` | Max parallel tasks |
| `CHETTER_CONTAINER_MEMORY` | (unset) | Memory limit passed to `docker --memory`/`--memory-swap` (see [Container resource limits](#container-resource-limits)) |
| `CHETTER_CONTAINER_CPU` | (unset) | CPU quota in cores passed to `docker --cpus` (decimal allowed) |
| `CHETTER_CONTAINER_PIDS` | (unset) | PID cap passed to `docker --pids-limit` |

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

## Development Plan

| Phase | Status | Description |
|-------|--------|-------------|
| 1 — Core + Proxy | Done | MCP server, workspace, proxy, config |
| 2 — Docker execution | Done | Docker CLI, container spawn, interactive serve |
| 3 — Network controls | Done | Optional proxy and DNS filtering (not a security boundary) |
| 4 — OpenCode Adapter | Done | `opencode serve` in local + Docker mode |
| 5 — Skills + Backend Harness | Done | Agent skill injection, backend developer Docker image |
| 6 — gVisor Sandbox | Done | `--runtime=runsc` flag, K8s DaemonSet installer |
| 7 — Niffler Patch | Planned | MCP client agent mode |
