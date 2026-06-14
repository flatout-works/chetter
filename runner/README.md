# Chetter Runner

Runs agent harnesses (OpenCode, Niffler) inside Kata Containers (micro-VMs) for strong isolation while proxying privileged operations (git and HTTP) through a runner-managed MCP server.

## Architecture

```
Worker Node (Ubuntu 24.04, KVM enabled, containerd + Kata pre-installed)
│
├── containerd (systemd service, /run/containerd/containerd.sock)
├── Kata 3.30.0 (/opt/kata, /dev/kvm)
├── iptables kernel modules
└── Runner Container (--privileged, mounts host resources)
    ├── ConnectRPC client → Chetter control plane
    ├── Git engine (SSH keys / PAT)
    ├── MCP Server (Unix socket per task)
    ├── Transparent HTTP Proxy (:18080)
    │
    └── ctr → host containerd → containerd-shim-kata-v2 → QEMU/KVM VM
                                           │
                                    ┌──────┴──────┐
                                    │   Agent     │ (OpenCode serve / Niffler)
                                    │ inside Kata │
                                    │  micro-VM   │
                                    └─────────────┘
```

> **Important:** The runner **does not bundle** containerd or Kata. It uses the **host node's** containerd socket and KVM device. Every worker node must have these installed before the runner starts. See "Worker Node Requirements" below.

## Prerequisites

### Hardware Requirements

| Requirement | Why |
|-------------|-----|
| **KVM** (`/dev/kvm`) | Kata uses QEMU/KVM micro-VMs |
| >4 GB RAM free per task | Each Kata VM needs memory |
| x86_64 or ARM64 | Kata supported architectures |

Verify KVM:
```bash
ls /dev/kvm && echo "KVM available" || echo "KVM missing — enable in BIOS and load kvm_intel/kvm_amd module"
```

### Software Prerequisites (Host Installation)

The following must be installed on the **host machine** (not inside the runner container). The runner must run as **root** (or with `CAP_NET_ADMIN` + access to `/run/containerd/containerd.sock`).

#### 1. containerd

```bash
sudo apt-get update
sudo apt-get install -y containerd

# Configure containerd for systemd cgroups
sudo mkdir -p /etc/containerd
sudo containerd config default | sudo tee /etc/containerd/config.toml
sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml

# Start and enable
sudo systemctl restart containerd
sudo systemctl enable containerd
```

Verify:
```bash
sudo ctr version
```

#### 2. Kata Containers (via GitHub Release)

```bash
cd /tmp
KATA_VERSION=3.30.0

# Download static release (correct URL: amd64, .tar.zst)
wget https://github.com/kata-containers/kata-containers/releases/download/${KATA_VERSION}/kata-static-${KATA_VERSION}-amd64.tar.zst

# Install zstd if needed
sudo apt-get install -y zstd

# Extract to /opt/kata
sudo tar --zstd -xvf kata-static-${KATA_VERSION}-amd64.tar.zst -C /

# Create symlinks
sudo mkdir -p /usr/local/bin
sudo ln -sf /opt/kata/bin/containerd-shim-kata-v2 /usr/local/bin/containerd-shim-kata-v2
sudo ln -sf /opt/kata/bin/kata-runtime /usr/local/bin/kata-runtime
```

Verify:
```bash
kata-runtime version
```

#### 3. Configure containerd for Kata Runtime

```bash
sudo tee -a /etc/containerd/config.toml << 'EOF'

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
  privileged_without_host_devices = true
EOF

sudo systemctl restart containerd
```

#### 4. Pull Test Image and Verify Kata

```bash
# Pull an image first (ctr does not auto-pull)
sudo ctr image pull docker.io/library/alpine:latest

# Run a test container inside a Kata VM
sudo ctr run --runtime io.containerd.kata.v2 --rm docker.io/library/alpine:latest test-kata uname -a
```

Expected output includes a Kata guest kernel (version will vary):
```
Linux fc6eb5c2bf6a 6.18.15 #1 SMP ... x86_64 Linux
```

#### 5. Network Tools (for runner)

```bash
sudo apt-get install -y iptables iproute2 socat
```

#### 6. Chetter Control Plane

Start the Chetter MCP server and configure `server.url` in `runner.yaml`, or set
`CHETTER_SERVER_URL` when using the container entrypoint.

## Building the Runner

```bash
cd runner/
go mod tidy
go build -o runner ./cmd/runner
```

## Running the Runner (Development / Local Mode)

For testing **without Kata** (spawns plain local processes, no VM isolation):

```bash
export RUNNER_LOCAL=true
./runner -config runner.yaml
```

Useful for development and CI smoke tests where Kata is not available.

## Running the Runner (Production / Kata Mode)

```bash
# MUST run as root (for iptables, containerd socket, network namespaces)
sudo ./runner -config runner.yaml
```

Or as a privileged container:

```bash
# Build image from the repository root
docker build -f runner/Dockerfile.runner -t chetter/runner .

# Run with host containerd socket and KVM device.
docker run -d --name chetter-runner \
  --privileged \
  -e CHETTER_SERVER_URL=http://host.docker.internal:8080 \
  -v /run/containerd/containerd.sock:/run/containerd/containerd.sock \
  -v /dev/kvm:/dev/kvm \
  -v /var/lib/runner:/var/lib/runner \
  -v "$PWD/runner.docker.yaml:/etc/runner/runner.yaml:ro" \
  -p 18080:18080 \
  chetter/runner
```

The image sets `TMPDIR=/var/lib/runner/tmp` because `ctr` creates temporary mount points before asking host containerd to mount snapshots. That temp path must live on a bind mount that exists on both the runner container and the host.

If the container exits immediately, check `docker logs chetter-runner`. Common causes are a missing `server.url`, a stale image that does not include `ctr`, or lack of access to the mounted containerd socket. If you see `ctr not found in PATH`, rebuild the image from the current `Dockerfile.runner`.

If a task fails with `failed to mount /tmp/containerd-mount...`, rebuild the image so it uses the shared `TMPDIR`, then recreate the container.

## Sending a Task

Submit tasks through the Chetter MCP server using `chetter_submit_task`. Runners
claim queued tasks from the control plane over ConnectRPC.

## Supported Harnesses

| Harness | Mode | Status |
|---------|------|--------|
| **OpenCode** | `opencode run` (non-interactive via model flag) | **In progress — local mode works** |
| **Niffler** | MCP socket integration | Planned — library patch to add `--mcp-socket` agent mode |

Unmodified harnesses work for public workflows (HTTP through proxy, workspace access, bash). Private git push requires harness to call MCP tools (`git_push`).

## Security Model

| Layer | Implementation |
|-------|---------------|
| VM Isolation | Kata micro-VM (QEMU/KVM) |
| Network Lockdown | iptables REDIRECT + DNS proxy |
| No Credentials in VM | Git/SSH keys stay in runner |
| LLM Key | Inside VM (known tradeoff: prompt exfiltration possible) |
| Proxy Filtering | SNI-based allowlist/blocklist |

## Troubleshooting

**`ctr: runtime "io.containerd.kata.v2" not supported`**
→ Check `containerd-shim-kata-v2` is installed on the host and visible to the host containerd service: `which containerd-shim-kata-v2`

**`qemu-system-x86_64: could not open /dev/kvm`**
→ Ensure KVM is enabled in BIOS and modules loaded:
```bash
sudo modprobe kvm_intel  # or kvm_amd
sudo usermod -aG kvm $USER
```

**`ctr: image "...": not found`**
→ Pull the image first: `sudo ctr image pull docker.io/library/alpine:latest`

**`ctr: connection error: dial unix /run/containerd/containerd.sock: permission denied`**
→ Run as root, or add your user to the group that owns the socket:
```bash
ls -la /run/containerd/containerd.sock  # check group
sudo usermod -aG <group> $USER
```

**`iptables: Permission denied` in runner**
→ Runner must run as root.

## Development Plan

| Phase | Status | Description |
|-------|--------|-------------|
| 1 — Core + Proxy | Done | MCP server, workspace, proxy, config |
| 2 — containerd/Kata client | Done | `ctr` wrapper, VM spawn, wait-for-exit |
| 3 — Network isolation | Done | Per-task bridge, iptables REDIRECT, DNS proxy |
| 4 — OpenCode Adapter | Done | `opencode run` in local + Docker + Kata mode |
| 5 — Skills + Backend Harness | Done | Agent skill injection, backend developer Docker image |
| 6 — Niffler Patch | Planned | MCP client agent mode |
