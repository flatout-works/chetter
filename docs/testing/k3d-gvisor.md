# Testing Chetter in Kubernetes with k3d and gVisor

This guide walks through deploying Chetter locally on a k3d Kubernetes cluster with
optional gVisor sandboxing for agent workloads. No external cloud services needed —
everything runs on a single machine.

## Prerequisites

You need Docker, k3d, kubectl, Go, and a MySQL client (for talking to TiDB's MySQL-compatible port). Install them once:

```bash
# Docker
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER  # then log out and back in

# k3d — lightweight k3s in Docker
curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | sudo bash

# kubectl — Kubernetes CLI
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -m 755 kubectl /usr/local/bin/kubectl && rm kubectl

# Go 1.24+ (for goose migrations via `go run`)
# See https://go.dev/dl/ or use your package manager

# MySQL client (for talking to TiDB's MySQL-compatible port)
sudo apt-get install -y mysql-client   # Debian/Ubuntu
# sudo dnf install -y mysql             # Fedora/RHEL
```

Verify everything is installed:

```bash
docker version        # 28.x+
k3d version           # v5.8+
kubectl version       # client v1.32+
go version            # go1.24+
mysql --version       # any recent version
```

## Installing gVisor (for Phase B only)

gVisor provides a userspace kernel that intercepts syscalls, giving you stronger
isolation than plain containers. Installation takes ~2 minutes:

```bash
# 1. Add the gVisor repository
curl -fsSL https://gvisor.dev/archive.key | \
  sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
  sudo tee /etc/apt/sources.list.d/gvisor.list

# 2. Install runsc
sudo apt-get update && sudo apt-get install -y runsc

# 3. Register runsc as a Docker runtime
sudo runsc install

# 4. Restart Docker to pick up the new runtime
sudo systemctl restart docker
```

Verify gVisor is registered and working:

```bash
docker info | grep runsc
# Expected output:  runsc

docker run --runtime=runsc --rm alpine uname -a
# Should show a gVisor kernel version in the output
```

> **Note:** gVisor works on Linux x86_64 and ARM64 only. It is not available on
> macOS or Windows. If you're on macOS, skip Phase B.

## Overview

Two test phases, each self-contained:

| Phase | Agent execution | gVisor | What it validates |
|-------|----------------|--------|-------------------|
| A | Subprocess inside runner pod | No | Chetter k8s deployment works |
| B | Docker container on host | Yes | Runner → gVisor agent path in k8s |

## Phase A: Chetter in k3d (local mode)

### 1. Create the cluster

```bash
k3d cluster create chetter \
  -p "18088:80@loadbalancer" \
  --agents 1
```

### 2. Create namespace

```bash
kubectl create namespace chetter
```

### 3. Deploy TiDB

```bash
kubectl apply -f deploy/k3d/tidb.yaml
```

Wait for TiDB to be ready:

```bash
kubectl -n chetter wait --for=condition=ready pod -l app=tidb --timeout=120s
```

### 4. Run database migrations

Migrations must be run from the host against the TiDB service. Use `kubectl port-forward`
to expose TiDB, then run `goose` locally:

```bash
# In one terminal, forward TiDB port:
kubectl -n chetter port-forward svc/tidb 4400:4000

# In another terminal, create the database and run migrations:
mysql -h 127.0.0.1 -P 4400 -u root -e "CREATE DATABASE IF NOT EXISTS chetter"
go run github.com/pressly/goose/v3/cmd/goose@latest \
  -dir db/migrations mysql "root@tcp(127.0.0.1:4400)/chetter?parseTime=true" up
```

> **Note:** The runner image includes the `goose` binary but not the `db/migrations/`
> directory (migrations live in the repository root). Running migrations from the host
> via port-forward is the simplest approach for local testing.

### 5. Build and import images

Build the MCP and runner images locally, tag them as `:local`, and import into k3d:

```bash
# Build images
make docker-build-mcp MCP_IMAGE=ghcr.io/flatout-works/chetter-mcp:local
make docker-build-runner RUNNER_IMAGE=ghcr.io/flatout-works/chetter-runner:local

# Import into k3d
k3d image import ghcr.io/flatout-works/chetter-mcp:local \
                ghcr.io/flatout-works/chetter-runner:local \
                -c chetter
```

> **Note:** If the runner base image hasn't been built yet, build it first with
> `make docker-build-runner-base RUNNER_BASE_IMAGE=ghcr.io/flatout-works/chetter-runner-base:local`.

### 6. Deploy Chetter

```bash
kubectl -n chetter apply -f deploy/k8s/secrets.yaml
kubectl -n chetter apply -f deploy/k8s/mcp-service.yaml
kubectl -n chetter apply -f deploy/k8s/mcp-deployment.yaml
kubectl -n chetter apply -f deploy/k8s/runner-deployment.yaml

# Patch deployments to use the local images
kubectl -n chetter set image deployment/chetter-mcp mcp=ghcr.io/flatout-works/chetter-mcp:local
kubectl -n chetter set image deployment/chetter-runner runner=ghcr.io/flatout-works/chetter-runner:local
```

Wait for pods:

```bash
kubectl -n chetter wait --for=condition=ready pod -l app=chetter-mcp --timeout=120s
kubectl -n chetter wait --for=condition=ready pod -l app=chetter-runner --timeout=120s
```

### 7. Smoke test

Verify the MCP server is healthy and submit a test task via curl:

```bash
# Health check
kubectl -n chetter port-forward svc/chetter-mcp 9080:8080 &
sleep 2
curl -s http://localhost:9080/healthz
# Expected: "ok"

# Submit a task (using MCP JSON-RPC protocol with the admin token)
TASK_ID=$(curl -s -X POST http://localhost:9080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer change-me" \
  -d '{
    "jsonrpc": "2.0",
    "id": "1",
    "method": "tools/call",
    "params": {
      "name": "chetter_submit_task",
      "arguments": {
        "prompt": "Create a file called hello.txt containing Hello from k3d!",
        "timeout_sec": 120
      }
    }
  }' | jq -r '.result.structuredContent.task.id')
echo "Task ID: $TASK_ID"

# Check task status (wait a few seconds for the runner to pick it up)
sleep 10
curl -s -X POST http://localhost:9080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer change-me" \
  -d "{
    \"jsonrpc\": \"2.0\",
    \"id\": \"2\",
    \"method\": \"tools/call\",
    \"params\": {
      \"name\": \"chetter_task_status\",
      \"arguments\": {
        \"task_id\": \"$TASK_ID\"
      }
    }
  }" | jq '.result.structuredContent.task | {status, summary}'

kill %1 2>/dev/null
```
> **Using an MCP client:** Configure any MCP client with
> `http://localhost:9080/mcp` and bearer token `change-me`. The `chetter_submit_task`,
> `chetter_task_status`, `chetter_list_tasks`, and other tools are available.

### Cleanup

```bash
k3d cluster delete chetter
```

---

## Phase B: gVisor (Docker mode)

gVisor runs agent containers inside a userspace kernel for stronger isolation.
The runner delegates to the **host's** Docker daemon via the mounted socket,
launching each agent container with `--runtime runsc`.

> **Prerequisite:** gVisor must be installed on the host — see [Installing gVisor](#installing-gvisor-for-phase-b-only) above.

### 1. Start TiDB (if not already running)

```bash
docker rm -f chetter-tidb 2>/dev/null
docker run -d --name chetter-tidb -p 4400:4000 pingcap/tidb:v8.5.1 --store=unistore

# Wait for TiDB to be ready (takes ~20s)
until mysql -h 127.0.0.1 -P 4400 -u root -e "SELECT 1" > /dev/null 2>&1; do sleep 3; done
echo "TiDB ready"

# Create database and apply schemax
mysql -h 127.0.0.1 -P 4400 -u root -e "CREATE DATABASE IF NOT EXISTS chetter"
# Extract only the Up section from each migration (skipping Down)
for f in db/migrations/0*.sql; do
  sed -n '/+goose Up/,/+goose Down/p' "$f" | grep -v '^-- +goose' | mysql -h 127.0.0.1 -P 4400 -u root chetter
done
echo "Migrations applied"
```

### 2. Build and tag images locally

```bash
make docker-build-mcp MCP_IMAGE=ghcr.io/flatout-works/chetter-mcp:local
make docker-build-runner RUNNER_IMAGE=ghcr.io/flatout-works/chetter-runner:local

# Tag agent image so Docker can find it locally
docker tag ghcr.io/flatout-works/chetter-runner:local ghcr.io/flatout-works/chetter-runner:main
```

### 3. Start MCP server

```bash
docker run -d --name chetter-mcp --network host \
  -e DATABASE_DSN="root@tcp(127.0.0.1:4400)/chetter?parseTime=true" \
  -e MCP_AUTH_TOKEN=test-token \
  -e HTTP_ADDR=:9080 \
  -e DEFAULT_AGENT_IMAGE=ghcr.io/flatout-works/chetter-runner:main \
  ghcr.io/flatout-works/chetter-mcp:local && sleep 3

curl -s http://localhost:9080/healthz
# Expected: "ok"
```

### 4. Start gVisor runner

The runner connects to the MCP server via Docker's `host.docker.internal` and
mounts the host Docker socket. Agent containers are spawned on the default
Docker bridge with `--runtime=runsc`:

```bash
mkdir -p /tmp/chetter-gvisor-test

docker run -d --name chetter-runner-gvisor \
  --privileged \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /tmp/chetter-gvisor-test:/var/lib/chetter-runner \
  --add-host host.docker.internal:host-gateway \
  -e CHETTER_SERVER_URL=http://host.docker.internal:9080 \
  -e CHETTER_RUNNER_AUTH_TOKEN=test-token \
  -e RUNNER_LOCAL=false \
  -e USE_GVISOR=true \
  -e RUNNER_MAX_CONCURRENT=1 \
  -e RUNNER_BIND_ADDR=0.0.0.0 \
  ghcr.io/flatout-works/chetter-runner:local

sleep 8
docker logs chetter-runner-gvisor --tail 3
# Expected: "claiming tasks via ConnectRPC url=http://host.docker.internal:9080"
```

### 5. Submit a task and verify gVisor

Submit a task and check that the agent container is running under `runsc`:

```bash
# Submit via MCP JSON-RPC
TASK_RESP=$(curl -s -X POST http://localhost:9080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-token" \
  -d '{"jsonrpc":"2.0","id":"1","method":"tools/call","params":{"name":"chetter_submit_task","arguments":{"prompt":"echo GVISTEST && uname -a","timeout_sec":120}}}')
TASK_ID=$(echo "$TASK_RESP" | grep '^data:' | sed 's/^data: //' | jq -r '.result.structuredContent.task.id')
echo "Task: $TASK_ID"

# Check runtime while the container is running
sleep 7
CID=$(docker ps --filter name=chetter-task-$TASK_ID -q)
docker inspect $CID --format 'Name={{.Name}} Runtime={{index .HostConfig "Runtime"}}'
# Expected: Name=/chetter-task-... Runtime=runsc

# Wait for completion and verify
sleep 15
curl -s -X POST http://localhost:9080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-token" \
  -d "{\"jsonrpc\":\"2.0\",\"id\":\"2\",\"method\":\"tools/call\",\"params\":{\"name\":\"chetter_task_status\",\"arguments\":{\"task_id\":\"$TASK_ID\"}}}" \
  | grep '^data:' | sed 's/^data: //' | jq '.result.structuredContent.task | {status, summary}'
```

The `uname -a` output inside the container will show the gVisor kernel
(`4.19.0-gvisor`) confirming the agent ran under gVisor.

### Cleanup

```bash
docker rm -f chetter-runner-gvisor chetter-mcp chetter-tidb
sudo rm -rf /tmp/chetter-gvisor-test
```

### Running gVisor inside k3d

To run gVisor inside k3d, you need Docker socket access from within the k3d cluster.
Create the cluster with the host Docker socket mounted into an agent node:

```bash
k3d cluster create chetter --agents 1 \
  --volume /var/run/docker.sock:/var/run/docker.sock@agent:0

# Then deploy the gVisor runner from deploy/k3d/gvisor-runner.yaml
# Note: hostPath /var/run/docker.sock must be available on the node
```

This path has additional networking complexity (DNS resolution, Docker bridge vs
pod network). For quick gVisor testing, the Docker-based approach above is
recommended.

---

## Run time

| Step | Estimated time |
|------|---------------|
| Install prerequisites (one-time) | 5–10 min |
| Install gVisor (one-time) | 2 min |
| Create k3d cluster | 30 s |
| TiDB ready | 60 s |
| Run migrations | 15 s |
| Build images (first time) | 5–10 min (base), 1 min (app layer) |
| Deploy + pods ready | 30 s |
| Task execution | 10–30 s depending on prompt |

A fresh Phase A run (images already built) takes under 5 minutes from cluster create to task done.

## Troubleshooting

### TiDB won't start

Check the TiDB pod logs:

```bash
kubectl -n chetter logs -l app=tidb
```

Common issue: the PVC can't be provisioned. k3d's local-path provisioner should
handle this automatically, but if it doesn't, try:

```bash
kubectl -n chetter delete pvc -l app=tidb
kubectl -n chetter delete pod -l app=tidb
kubectl -n chetter apply -f deploy/k3d/tidb.yaml
```

### MCP server can't connect to TiDB

Check the MCP pod logs:

```bash
kubectl -n chetter logs -l app=chetter-mcp
```

Ensure the `DATABASE_DSN` secret references `tidb:4000` and that the TiDB service
is reachable from the MCP pod:

```bash
kubectl -n chetter run dns-test --rm -i --restart=Never --image=alpine:latest -- nslookup tidb
```

### gVisor runner: "runtime runsc not found"

This means gVisor isn't installed on the host or Docker doesn't know about it.

```bash
# Verify on the host
docker info | grep runsc
# Should list runsc as a runtime

# Re-install if missing
sudo runsc install
sudo systemctl restart docker
```

### gVisor runner: "Cannot connect to the Docker daemon"

Check the Docker socket is accessible from the pod:

```bash
kubectl -n chetter exec deploy/chetter-runner-gvisor -- ls -la /var/run/docker.sock
```

If the socket is missing, ensure the hostPath mount is correct and the k3d node
container has access to the host's `/var/run/docker.sock`.

### Runner pods crash-looping

Check logs:

```bash
kubectl -n chetter logs -l app=chetter-runner --tail=50
kubectl -n chetter logs -l app=chetter-runner-gvisor --tail=50
```

Common causes:
- Missing `CHETTER_SERVER_URL` or wrong value
- MCP server not reachable from the runner pod
- Missing `CHETTER_MCP_AUTH_TOKEN`
