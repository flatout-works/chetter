# Running Chetter On Local k3s

Status: **Canonical local k3s guide**

This guide shows how to run the Chetter server, web UI, runner, and local TiDB in a single-node k3s cluster for local validation.

Current state: Chetter's Kubernetes manifests run the control plane and runner as Kubernetes workloads, but the runner still executes agent task containers through the host Docker socket with `RUNNER_LOCAL=true`. There is no Kubernetes pod executor in the runner yet. For sandboxing, install gVisor (`runsc`) into Docker and set `USE_GVISOR=true`; task containers then run with Docker's `--runtime=runsc`.

Use [EKS.md](EKS.md) for production Kubernetes notes. Use this document for local k3s validation.

## What This Validates

- k3s can run the Chetter server, web UI, runner, and TiDB manifests.
- The server can connect to TiDB and serve MCP, health, ConnectRPC, and web UI endpoints.
- The runner can connect to the server, claim tasks, and report events.
- Agent task containers can run on the host Docker daemon, optionally under gVisor.

This does not validate a Kubernetes pod-per-task executor. Task containers are Docker containers on the host, not Kubernetes Pods.

## Prerequisites

- Linux host or VM. Ubuntu 24.04 is the known-good target.
- Root or sudo access.
- At least 4 CPU and 4 GB RAM.
- Docker installed and running. k3s uses containerd, but the current runner needs the host Docker socket for task execution.
- `kubectl` installed or available through `sudo k3s kubectl`.
- At least one provider key for the harness/model you want to test.

## Architecture

```text
k3s cluster (single node)
  namespace: chetter
    TiDB StatefulSet (local unistore test engine)
    Chetter MCP server Deployment
      port 8080: MCP and health
      port 8090: web UI and ConnectRPC API
    Chetter runner Deployment
      RUNNER_LOCAL=true
      /var/run/docker.sock mounted from host
      creates host Docker task containers

host Docker daemon
  agent task containers
  optional gVisor runtime: runsc
```

## 1. Install k3s

```bash
curl -sfL https://get.k3s.io | sh -
```

Verify k3s is running:

```bash
sudo systemctl status k3s --no-pager
sudo k3s kubectl get nodes
```

## 2. Configure kubectl

The default `kubectl` config is often missing after a fresh k3s install. Copy the k3s kubeconfig:

```bash
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown "$USER:$USER" ~/.kube/config
chmod 600 ~/.kube/config
kubectl get nodes
```

If `kubectl` reports `connection refused localhost:8080`, it is still not using the k3s kubeconfig.

## 3. Install CNI Plugins If Needed

k3s normally includes working pod networking, but some minimal hosts are missing CNI plugin binaries at `/opt/cni/bin`. If pods fail with `failed to find plugin "bridge" in path [/opt/cni/bin]`, install them:

```bash
sudo apt-get update
sudo apt-get install -y containernetworking-plugins
sudo mkdir -p /opt/cni/bin
sudo cp -a /usr/lib/cni/* /opt/cni/bin/
```

Verify the expected plugins exist:

```bash
ls -l /opt/cni/bin/bridge /opt/cni/bin/host-local /opt/cni/bin/loopback
```

Run a simple pod smoke test:

```bash
kubectl run cni-smoke --image=busybox --restart=Never -- sh -c 'ip addr && sleep 5'
kubectl get pod cni-smoke
kubectl logs cni-smoke
kubectl delete pod cni-smoke
```

## 4. Install gVisor For Docker Task Containers

This is the important gVisor setup for current Chetter task execution. The runner calls the host Docker daemon, so Docker must know about the `runsc` runtime.

```bash
sudo apt-get update
sudo apt-get install -y runsc
sudo /usr/bin/runsc install
sudo systemctl restart docker
```

Verify Docker can launch a gVisor container:

```bash
docker run --runtime=runsc --rm alpine dmesg
```

The output should mention gVisor, for example `Starting gVisor...`.

If you do not need sandbox validation, you can skip this step and keep `USE_GVISOR=false` on the runner.

## 5. Optional: Configure k3s containerd For RuntimeClass Smoke Tests

This optional step validates that k3s itself can start pods with `runtimeClassName: gvisor`. It is useful before future Kubernetes pod executor work, but current Chetter tasks do not use this path.

Install `runsc` if you did not already:

```bash
sudo apt-get update
sudo apt-get install -y runsc
```

Add a `runsc` runtime handler to k3s containerd:

```bash
sudo tee -a /var/lib/rancher/k3s/agent/etc/containerd/config.toml <<'EOF'

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
EOF

sudo systemctl restart k3s
```

k3s may regenerate `config.toml` from `config.toml.tmpl`. If your change disappears after restart, put the same runtime section in:

```text
/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl
```

Register the RuntimeClass:

```bash
kubectl apply -f - <<'EOF'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
EOF
```

Smoke test it:

```bash
kubectl run gvisor-smoke \
  --image=busybox \
  --restart=Never \
  --overrides='{"spec":{"runtimeClassName":"gvisor"}}' \
  -- sh -c 'uname -a && sleep 5'

kubectl get pod gvisor-smoke
kubectl describe pod gvisor-smoke | grep "Runtime Class"
kubectl logs gvisor-smoke
kubectl delete pod gvisor-smoke
```

## 6. Build And Tag Local Images

From the repository root:

```bash
./deploy/build.sh
```

The Kubernetes manifests use GHCR image names. For local validation from your checkout, tag the locally built images with those names:

```bash
docker tag chetter-mcp:latest ghcr.io/flatout-works/chetter-mcp:main
docker tag chetter-runner:latest ghcr.io/flatout-works/chetter-runner:main
```

The host Docker daemon also needs the runner image under the same name, because task containers use `DEFAULT_AGENT_IMAGE=ghcr.io/flatout-works/chetter-runner:main` by default.

## 7. Import Images Into k3s

k3s uses containerd for Kubernetes pods, not Docker. Import the tagged images:

```bash
docker save ghcr.io/flatout-works/chetter-mcp:main ghcr.io/flatout-works/chetter-runner:main | sudo k3s ctr images import -
```

Verify:

```bash
sudo k3s ctr images ls | grep 'flatout-works/chetter'
```

## 8. Create Namespace And Secrets

Create the namespace:

```bash
kubectl apply -f deploy/k8s/namespace.yaml
```

Create local tokens and set at least one provider key for the harness/model you want to test:

```bash
export CHETTER_MCP_AUTH_TOKEN="$(openssl rand -hex 32)"
export CHETTER_RUNNER_RPC_TOKEN="$(openssl rand -hex 32)"
export GITHUB_TOKEN=""
export DEEPSEEK_API_KEY=""
export OPENCODE_API_KEY=""
export ANTHROPIC_API_KEY=""

kubectl -n chetter create secret generic chetter-secrets \
  --from-literal=CHETTER_MCP_AUTH_TOKEN="$CHETTER_MCP_AUTH_TOKEN" \
  --from-literal=CHETTER_RUNNER_RPC_TOKEN="$CHETTER_RUNNER_RPC_TOKEN" \
  --from-literal=DATABASE_DSN='root@tcp(tidb:4000)/chetter?parseTime=true' \
  --from-literal=GITHUB_TOKEN="$GITHUB_TOKEN" \
  --from-literal=DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" \
  --from-literal=OPENCODE_API_KEY="$OPENCODE_API_KEY" \
  --from-literal=ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Do not apply `deploy/k8s/secrets.yaml` unchanged; it intentionally contains empty placeholders and the server rejects empty or `change-me*` auth tokens.

## 9. Deploy TiDB

Use the local TiDB test manifest:

```bash
kubectl apply -f deploy/k3d/tidb.yaml
```

Wait for it:

```bash
kubectl -n chetter rollout status statefulset/tidb
kubectl -n chetter get pod -l app=tidb
```

This manifest runs TiDB with `unistore`, a single-container test engine. It is fine for local validation. For production, use TiDB Cloud or a real TiDB cluster.

## 10. Deploy The Chetter Server And Runner

Apply the service and deployments:

```bash
kubectl apply -f deploy/k8s/mcp-service.yaml
kubectl apply -f deploy/k8s/mcp-deployment.yaml
kubectl apply -f deploy/k8s/runner-deployment.yaml
```

For local validation, scale the server and runner down to one replica each:

```bash
kubectl -n chetter scale deployment/chetter-mcp --replicas=1
kubectl -n chetter scale deployment/chetter-runner --replicas=1
```

If you installed Docker gVisor in step 4, enable it for task containers:

```bash
kubectl -n chetter set env deployment/chetter-runner USE_GVISOR=true
```

Wait for rollout:

```bash
kubectl -n chetter rollout status deployment/chetter-mcp
kubectl -n chetter rollout status deployment/chetter-runner
kubectl -n chetter get pods
```

## 11. Open The Web UI

Forward the web UI and API port:

```bash
kubectl -n chetter port-forward svc/chetter-mcp 18090:8090
```

Open `http://localhost:18090` and log in with `CHETTER_MCP_AUTH_TOKEN`.

In a separate terminal, you can also forward the MCP/health port:

```bash
kubectl -n chetter port-forward svc/chetter-mcp 18088:8080
curl http://localhost:18088/healthz
```

## 12. Submit And Validate A Task

Use the web UI or an MCP client to submit a small task.

Watch the Chetter workloads:

```bash
kubectl -n chetter get pods
kubectl -n chetter logs deployment/chetter-mcp -f
kubectl -n chetter logs deployment/chetter-runner -f
```

Because the current runner uses Docker mode, task containers show up in host Docker, not in `kubectl get pods`:

```bash
docker ps --filter name=chetter
docker ps -a --filter name=chetter
```

If `USE_GVISOR=true`, inspect the task container runtime from Docker:

```bash
docker inspect <container-id> --format '{{.HostConfig.Runtime}}'
```

The expected value is `runsc`.

## Common Operations

### Restart After Secret Changes

```bash
kubectl -n chetter rollout restart deployment/chetter-mcp deployment/chetter-runner
```

### Scale Runners

```bash
kubectl -n chetter scale deployment/chetter-runner --replicas=2
```

Each runner pod can also process multiple tasks with `RUNNER_MAX_CONCURRENT`.

### Check TiDB

```bash
kubectl -n chetter logs statefulset/tidb
kubectl -n chetter exec -ti tidb-0 -- mysql -h 127.0.0.1 -P 4000 -e 'SELECT 1'
```

### Cleanup Chetter Resources

```bash
kubectl delete namespace chetter
```

### Remove k3s Completely

```bash
sudo /usr/local/bin/k3s-uninstall.sh
```

## Troubleshooting

### `connection refused localhost:8080`

`kubectl` is not using the k3s kubeconfig. Repeat [step 2](#2-configure-kubectl).

### Pod stuck in `ContainerCreating`

Check events:

```bash
kubectl -n chetter describe pod <pod-name>
```

Common causes:

- Missing CNI plugins. See [step 3](#3-install-cni-plugins-if-needed).
- Local image was not imported into k3s. See [step 7](#7-import-images-into-k3s).
- A placeholder secret is still empty. See [step 8](#8-create-namespace-and-secrets).

### Server exits immediately

Check logs:

```bash
kubectl -n chetter logs deployment/chetter-mcp
```

Common causes:

- `CHETTER_MCP_AUTH_TOKEN` is empty or starts with `change-me`.
- `CHETTER_RUNNER_RPC_TOKEN` is empty or starts with `change-me`.
- `DATABASE_DSN` cannot reach `tidb:4000`.

### Runner cannot connect to server

Verify the service and logs:

```bash
kubectl -n chetter get svc chetter-mcp
kubectl -n chetter logs deployment/chetter-runner
kubectl -n chetter exec deployment/chetter-runner -- curl -s http://chetter-mcp:8080/healthz
```

### Runner cannot launch task containers

The current runner needs the host Docker socket. Verify the socket exists on the host and is mounted in the runner pod:

```bash
ls -l /var/run/docker.sock
kubectl -n chetter exec deployment/chetter-runner -- ls -l /var/run/docker.sock
kubectl -n chetter exec deployment/chetter-runner -- docker version
```

Also verify the default agent image exists in the host Docker daemon:

```bash
docker images | grep 'flatout-works/chetter-runner'
```

### Docker gVisor fails with `runtime runsc not found`

Docker does not have the `runsc` runtime installed. Repeat [step 4](#4-install-gvisor-for-docker-task-containers).

### k3s RuntimeClass fails with `RuntimeHandler "runsc" not supported`

k3s containerd does not have the `runsc` runtime handler configured. Repeat [step 5](#5-optional-configure-k3s-containerd-for-runtimeclass-smoke-tests).

### TiDB not ready

Check the StatefulSet and logs:

```bash
kubectl -n chetter get statefulset,pod -l app=tidb
kubectl -n chetter logs statefulset/tidb
```
