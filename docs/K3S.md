# Running Chetter On Local k3s

Status: **Canonical local k3s guide**

This guide shows how to run the Chetter server, web UI, runner, and local TiDB in a single-node k3s cluster for local validation.

Current state: with `EXECUTION_BACKEND=kubernetes`, the runner creates one agent Pod per execution through the Kubernetes API. The k3s example uses a single-node hostPath shared by runner and agent Pods and does not mount a Docker socket. Set `KUBERNETES_RUNTIME_CLASS=gvisor` only after the k3s containerd runtime handler and RuntimeClass are installed.

Use [EKS.md](EKS.md) for production Kubernetes notes. Use this document for local k3s validation.

## What This Validates

- k3s can run the Chetter server, web UI, runner, and TiDB manifests.
- The server can connect to TiDB and serve MCP, health, ConnectRPC, and web UI endpoints.
- The runner can connect to the server, claim tasks, and report events.
- Agent Pods can run through the Kubernetes executor, optionally with `runtimeClassName: gvisor`.
- ServeHarness task execution and resumable-session follow-up have been validated live with OpenCode agent Pods under gVisor. Pi attach, cancellation, and rolling-update behavior still require separate live-cluster smoke validation before this guide is considered production proof.

The hostPath manifest is intentionally single-node. It does not validate multi-node RWX storage.

## Prerequisites

- Linux host or VM. Ubuntu 24.04 is the known-good target.
- Root or sudo access.
- At least 4 CPU and 4 GB RAM.
- Docker only if it is used to build/save local images; task execution itself uses k3s containerd and the Kubernetes API.
- `kubectl` installed or available through `sudo k3s kubectl`.
- A provider key for the harness/model you want to test, unless that provider offers a model that does not require one.

## Architecture

```text
k3s cluster (single node)
  namespace: chetter
    TiDB StatefulSet (local unistore test engine)
    Chetter MCP server Deployment
      port 8080: MCP and health
      port 8090: web UI and ConnectRPC API
    Chetter runner Deployment
      EXECUTION_BACKEND=kubernetes
      hostPath workspace mounted at the runner workspace root
      creates agent Pods pinned to the runner node
    Agent Pod per execution
      task AgentImage, execution-only workspace subPath
      optional runtimeClassName: gvisor
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

## 4. Decide Whether To Use gVisor

gVisor is optional. Without it, clear `KUBERNETES_RUNTIME_CLASS` in `deploy/k3s/kubernetes-runner.yaml`. With it, install `runsc` for k3s containerd and create the RuntimeClass in the next step. gVisor is syscall isolation; it does not enforce network egress.

## 5. Configure k3s containerd For gVisor

This step is required when the runner sets `KUBERNETES_RUNTIME_CLASS=gvisor`. RuntimeClass scheduling or cluster policy must also place agent Pods on nodes that support the `runsc` handler.

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
docker tag chetter-agent-base:latest ghcr.io/flatout-works/chetter-agent-base:main
```

The server must resolve tasks to an actual agent image containing harness CLIs. The tight runner daemon image is not an agent image.

## 7. Import Images Into k3s

k3s uses containerd for Kubernetes pods, not Docker. Import the tagged images:

```bash
docker save ghcr.io/flatout-works/chetter-mcp:main ghcr.io/flatout-works/chetter-runner:main ghcr.io/flatout-works/chetter-agent-base:main | sudo k3s ctr images import -
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

Create the database before starting the Chetter server. The TiDB image does not contain
the `mysql` client, so use a temporary client Pod:

```bash
kubectl -n chetter run mysql-client --rm -i --restart=Never --image=mysql:8.4 -- \
  mysql -h tidb -P 4000 -u root -e 'CREATE DATABASE IF NOT EXISTS chetter'
```

This manifest runs TiDB with `unistore`, a single-container test engine. It is fine for local validation. For production, use TiDB Cloud or a real TiDB cluster.

## 10. Deploy The Chetter Server And Runner

Apply and wait for the server first. This avoids runner registration failures while the
server is applying database migrations:

```bash
kubectl apply -f deploy/k8s/mcp-service.yaml
kubectl apply -f deploy/k8s/mcp-deployment.yaml
kubectl -n chetter set env deployment/chetter-mcp DEFAULT_AGENT_IMAGE=ghcr.io/flatout-works/chetter-agent-base:main
kubectl -n chetter scale deployment/chetter-mcp --replicas=1
kubectl -n chetter rollout status deployment/chetter-mcp
```

Then apply the single-node runner. If gVisor is not installed for k3s containerd,
remove `KUBERNETES_RUNTIME_CLASS` from `deploy/k3s/kubernetes-runner.yaml` before applying it.

```bash
kubectl apply -f deploy/k3s/kubernetes-runner.yaml
kubectl -n chetter scale deployment/chetter-runner --replicas=1
kubectl -n chetter rollout status deployment/chetter-runner
```

Verify all three long-running workloads are ready:

```bash
kubectl -n chetter get pods -o wide
kubectl -n chetter logs deployment/chetter-runner --tail=20
```

The runner log should contain `claiming tasks via ConnectRPC`.

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

Before submitting the first task to a new database, create and select a default Git
author identity in the web UI, or call the MCP tools directly. With the port-forward
from step 11 still running:

```bash
curl -sS http://localhost:18088/mcp \
  -H "Authorization: Bearer $CHETTER_MCP_AUTH_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chetter_create_git_identity","arguments":{"name":"k3s-local","git_author_name":"Chetter k3s","git_author_email":"chetter-k3s@example.invalid","credential_type":"github_app"}}}'

curl -sS http://localhost:18088/mcp \
  -H "Authorization: Bearer $CHETTER_MCP_AUTH_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"chetter_set_git_identity_default","arguments":{"name":"k3s-local"}}}'
```

Submit a task that stays alive long enough to inspect its Pod. Replace the provider and
model if needed:

```bash
curl -sS http://localhost:18088/mcp \
  -H "Authorization: Bearer $CHETTER_MCP_AUTH_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"chetter_submit_task","arguments":{"prompt":"Use the bash tool to run sleep 30. Then return exactly: K3S_GVISOR_OK","agent_image":"ghcr.io/flatout-works/chetter-agent-base:main","harness":"opencode","provider_id":"opencode","model_id":"deepseek-v4-flash-free","timeout_sec":180}}}'
```

Copy the returned task ID into `TASK_ID`, then wait for and inspect its child Pod:

```bash
export TASK_ID=task_<returned-id>
kubectl -n chetter wait --for=condition=Ready pod \
  -l "chetter.io/task-id=$TASK_ID" --timeout=60s
export AGENT_POD="$(kubectl -n chetter get pod \
  -l "chetter.io/task-id=$TASK_ID" -o jsonpath='{.items[0].metadata.name}')"

kubectl -n chetter get pod "$AGENT_POD" -o jsonpath='runtimeClass={.spec.runtimeClassName}{"\n"}node={.spec.nodeName}{"\n"}image={.spec.containers[0].image}{"\n"}workspaceSubPath={.spec.containers[0].volumeMounts[0].subPath}{"\n"}serviceAccount={.spec.serviceAccountName}{"\n"}automountToken={.spec.automountServiceAccountToken}{"\n"}'
```

For the supplied gVisor manifest, expect `runtimeClass=gvisor`, the current node name,
the requested agent image, an execution-specific workspace subPath,
`serviceAccount=chetter-agent`, and `automountToken=false`.

Confirm that the runner itself has no Docker socket mount:

```bash
kubectl -n chetter get deployment chetter-runner \
  -o jsonpath='{range .spec.template.spec.containers[0].volumeMounts[*]}{.mountPath}{"\n"}{end}'
```

The only expected mount is `/var/lib/chetter-runner/workspaces`.

Watch the Chetter workloads:

```bash
kubectl -n chetter get pods
kubectl -n chetter logs deployment/chetter-mcp -f
kubectl -n chetter logs deployment/chetter-runner -f
```

During execution, child agent Pods show up in Kubernetes and are removed at finalization:

```bash
kubectl -n chetter get pods -l chetter.io/owned=true -w
```

After completion, query the task status through MCP:

```bash
curl -sS http://localhost:18088/mcp \
  -H "Authorization: Bearer $CHETTER_MCP_AUTH_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d "{\"jsonrpc\":\"2.0\",\"id\":4,\"method\":\"tools/call\",\"params\":{\"name\":\"chetter_task_status\",\"arguments\":{\"task_id\":\"$TASK_ID\"}}}"
```

Expect `status` to be `done` and `summary` to contain `K3S_GVISOR_OK`. The following
commands should then report no resources, confirming execution Pod and Secret cleanup:

```bash
kubectl -n chetter get pods -l chetter.io/owned=true
kubectl -n chetter get secrets -l chetter.io/owned=true
```

## Common Operations

### Restart After Secret Changes

```bash
kubectl -n chetter rollout restart deployment/chetter-mcp deployment/chetter-runner
```

### Scale Runners

```bash
kubectl -n chetter scale deployment/chetter-runner --replicas=1
```

The supplied hostPath manifest is single-node-only. Each runner Pod gets a unique UID-based `RUNNER_ID`; child Pods are pinned to that runner's node. Use an RWX PVC instead for multi-node production.

### Graceful Shutdown And Disruption

The single-node `kubernetes-runner.yaml` sets `terminationGracePeriodSeconds: 120` — headroom above the drain budget (`CHETTER_DRAIN_TIMEOUT_SEC` 60s + `CHETTER_DRAIN_HARD_KILL_TIMEOUT_SEC` 30s). On SIGTERM the runner stops claiming, waits for in-flight tasks up to that budget, force-cancels any that overrun, and blocks exit until teardown and terminal reports complete. See [DEPLOYMENT.md](DEPLOYMENT.md#graceful-shutdown) for details.

The generic `deploy/k8s/` kustomization adds a `PodDisruptionBudget` (`minAvailable: 1`) and autoscaler eviction annotations to gate voluntary evictions on multi-node clusters; the single-node hostPath manifest intentionally omits them (with one replica on one node, a PDB would block `kubectl drain` entirely — evict with `--disable-eviction` and let the SIGTERM drain finish in-flight tasks).

### Check TiDB

```bash
kubectl -n chetter logs statefulset/tidb
kubectl -n chetter run mysql-client --rm -i --restart=Never --image=mysql:8.4 -- \
  mysql -h tidb -P 4000 -u root -e 'SELECT 1'
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
- The `chetter` database was not created before server startup. Repeat step 9.

### Runner cannot connect to server

Verify the service and logs:

```bash
kubectl -n chetter get svc chetter-mcp
kubectl -n chetter logs deployment/chetter-runner
kubectl -n chetter exec deployment/chetter-runner -- curl -s http://chetter-mcp:8080/healthz
```

If registration returns `401 Unauthorized`, verify both workloads read the same
`CHETTER_RUNNER_RPC_TOKEN` from `chetter-secrets`, then restart both deployments.

### Runner cannot launch agent Pods

Verify runner identity/node metadata, RBAC, workspace mount, and events:

```bash
kubectl -n chetter exec deployment/chetter-runner -- env | grep -E 'RUNNER_ID|POD_UID|NODE_NAME'
kubectl -n chetter auth can-i create pods --as system:serviceaccount:chetter:chetter-runner
kubectl -n chetter get events --sort-by=.lastTimestamp
```

Also verify the agent image exists in k3s containerd:

```bash
sudo k3s ctr images ls | grep 'flatout-works/chetter-agent'
```

### k3s RuntimeClass fails with `RuntimeHandler "runsc" not supported`

k3s containerd does not have the `runsc` runtime handler configured. Repeat [step 5](#5-configure-k3s-containerd-for-gvisor).

### TiDB not ready

Check the StatefulSet and logs:

```bash
kubectl -n chetter get statefulset,pod -l app=tidb
kubectl -n chetter logs statefulset/tidb
```

If the PVC remains `Pending` and the local-path provisioner reports `no route to host`
for `10.43.0.1:443`, k3s Pod-to-service networking is unhealthy. Restart k3s and the
affected system deployments before retrying:

```bash
sudo systemctl restart k3s
kubectl wait --for=condition=Ready node --all --timeout=180s
kubectl -n kube-system rollout restart deployment/local-path-provisioner deployment/metrics-server
```

### Namespace remains `Terminating`

A stale aggregated API, commonly `metrics.k8s.io`, can block namespace discovery even
after all namespaced content is gone. Inspect the namespace conditions and verify no
resources remain before forcing finalization:

```bash
kubectl get namespace chetter -o yaml
kubectl get all,pvc,secrets,configmaps -n chetter
kubectl get namespace chetter -o json | jq '.spec.finalizers=[]' | \
  kubectl replace --raw '/api/v1/namespaces/chetter/finalize' -f -
```
