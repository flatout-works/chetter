# Full Chetter on k3s with gVisor

This guide deploys the entire Chetter stack — server, runner, TiDB, and web UI — on a
local k3s cluster with gVisor (`runsc`) sandboxing. Use this to validate that Chetter
works end-to-end in Kubernetes before moving to EKS or another managed cluster.

## Prerequisites

- A Linux host or VM (Ubuntu 24.04 tested)
- Root/sudo access
- Minimum 4 CPU, 4 GB RAM
- Docker installed (for building images, not for the runner)

## Architecture

```text
k3s cluster (single node)
  ├── Namespace: chetter
  │    ├── TiDB (StatefulSet, unistore)
  │    ├── Chetter MCP server (Deployment)
  │    │     ├── Port 8080 — MCP endpoint
  │    │     └── Port 8090 — Web UI + API
  │    ├── Chetter runner (Deployment)
  │    │     └── EXECUTION_BACKEND=kubernetes
  │    │         └── Creates agent Pods (runtimeClassName: gvisor)
  │    ├── Service: chetter-mcp
  │    ├── Service: tidb
  │    ├── Secret: chetter-secrets
  │    └── RuntimeClass: gvisor
  └── Namespace: kube-system
       └── (k3s built-in components)
```

## Step 1: Install k3s

```bash
curl -sfL https://get.k3s.io | sh -
```

Configure kubectl:

```bash
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown "$USER:$USER" ~/.kube/config
chmod 600 ~/.kube/config
kubectl get nodes
```

## Step 2: Install CNI Plugins

k3s needs CNI binaries at `/opt/cni/bin`:

```bash
sudo apt-get update
sudo apt-get install -y containernetworking-plugins
sudo mkdir -p /opt/cni/bin
sudo cp -a /usr/lib/cni/* /opt/cni/bin/
```

Verify:

```bash
ls /opt/cni/bin/bridge /opt/cni/bin/host-local /opt/cni/bin/loopback
```

## Step 3: Install gVisor

```bash
sudo apt-get install -y runsc
```

Configure containerd for `runsc`. The k3s containerd config is at:

```
/var/lib/rancher/k3s/agent/etc/containerd/config.toml
```

Add the runsc runtime handler:

```bash
sudo tee -a /var/lib/rancher/k3s/agent/etc/containerd/config.toml <<'EOF'

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
EOF
```

Restart k3s:

```bash
sudo systemctl restart k3s
```

Create the RuntimeClass:

```bash
kubectl apply -f - <<'EOF'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
EOF
```

Smoke test gVisor:

```bash
kubectl run gvisor-smoke \
  --image=busybox \
  --restart=Never \
  --overrides='{"spec":{"runtimeClassName":"gvisor"}}' \
  -- sh -c 'uname -a && sleep 5'

kubectl get pod gvisor-smoke
kubectl logs gvisor-smoke
kubectl delete pod gvisor-smoke
```

> See `docs/K3S.md` for detailed troubleshooting of k3s + gVisor setup.

## Step 4: Build Chetter Images

Build the server and runner images locally:

```bash
cd ~/git/chetter
./deploy/build.sh
```

This produces `chetter-mcp:latest` and `chetter-runner:latest` as local Docker images.

## Step 5: Import Images Into k3s

k3s uses containerd, not Docker. Import the built images:

```bash
sudo k3s ctr images import --platform linux/amd64 <(docker save chetter-mcp:latest)
sudo k3s ctr images import --platform linux/amd64 <(docker save chetter-runner:latest)
```

Verify:

```bash
sudo k3s ctr images ls | grep chetter
```

## Step 6: Create Namespace And Secrets

```bash
kubectl create namespace chetter
```

Create a secret with your tokens and provider keys:

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: chetter-secrets
  namespace: chetter
type: Opaque
stringData:
  CHETTER_MCP_AUTH_TOKEN: "CHANGE_ME_TO_A_LONG_RANDOM_TOKEN"
  CHETTER_RUNNER_RPC_TOKEN: "CHANGE_ME_RUNNER_TOKEN"
  DATABASE_DSN: "root@tcp(tidb:4000)/chetter?parseTime=true"
  DEEPSEEK_API_KEY: ""
  OPENCODE_API_KEY: ""
  SYNTHETIC_API_KEY: ""
  ANTHROPIC_API_KEY: ""
  GITHUB_TOKEN: ""
EOF
```

Replace the token values and at least one provider key.

## Step 7: Deploy TiDB

Use the existing k3d TiDB manifest (works on k3s too):

```bash
kubectl apply -f deploy/k3d/tidb.yaml
```

Wait for TiDB to be ready:

```bash
kubectl -n chetter rollout status statefulset/tidb
kubectl -n chetter get pod -l app=tidb
```

> This deploys TiDB with `unistore` (single-container test engine). It is fine for
> local validation. For production, use TiDB Cloud or a real TiDB cluster.

## Step 8: Deploy Chetter Server

```bash
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chetter-mcp
  namespace: chetter
spec:
  replicas: 1
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
        image: chetter-mcp:latest
        imagePullPolicy: IfNotPresent
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
        - name: MCP_AUTH_TOKEN
          valueFrom:
            secretKeyRef:
              name: chetter-secrets
              key: CHETTER_MCP_AUTH_TOKEN
        - name: CHETTER_RUNNER_RPC_TOKEN
          valueFrom:
            secretKeyRef:
              name: chetter-secrets
              key: CHETTER_RUNNER_RPC_TOKEN
        - name: DEFAULT_AGENT_IMAGE
          value: chetter-runner:latest
        - name: DATABASE_DSN
          valueFrom:
            secretKeyRef:
              name: chetter-secrets
              key: DATABASE_DSN
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: "1"
            memory: 512Mi
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: chetter-mcp
  namespace: chetter
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
EOF
```

Wait for the server:

```bash
kubectl -n chetter rollout status deployment/chetter-mcp
```

Expose the web UI for local access:

```bash
kubectl -n chetter port-forward svc/chetter-mcp 18090:8090 &
```

Open `http://localhost:18090` and log in with your `CHETTER_MCP_AUTH_TOKEN`.

## Step 9: Deploy The Runner (Kubernetes Mode)

> **Note:** This manifest describes the target state after the Kubernetes executor
> is implemented. Until then, use the Docker-mode runner (Step 9B) to validate the
> rest of the stack.

### Target: Kubernetes-Mode Runner

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: chetter-runner
  namespace: chetter
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: chetter-runner
  namespace: chetter
rules:
- apiGroups: [""]
  resources: [pods, pods/log, configmaps, secrets]
  verbs: [create, get, list, watch, delete, patch]
- apiGroups: [""]
  resources: [persistentvolumeclaims]
  verbs: [create, get, list, watch, delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: chetter-runner
  namespace: chetter
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: chetter-runner
subjects:
- kind: ServiceAccount
  name: chetter-runner
  namespace: chetter
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chetter-runner
  namespace: chetter
spec:
  replicas: 1
  selector:
    matchLabels:
      app: chetter-runner
  template:
    metadata:
      labels:
        app: chetter-runner
    spec:
      serviceAccountName: chetter-runner
      containers:
      - name: runner
        image: chetter-runner:latest
        imagePullPolicy: IfNotPresent
        envFrom:
        - secretRef:
            name: chetter-secrets
        env:
        - name: CHETTER_SERVER_URL
          value: "http://chetter-mcp:8080"
        - name: CHETTER_RUNNER_AUTH_TOKEN
          valueFrom:
            secretKeyRef:
              name: chetter-secrets
              key: CHETTER_RUNNER_RPC_TOKEN
        - name: EXECUTION_BACKEND
          value: "kubernetes"
        - name: KUBERNETES_NAMESPACE
          value: "chetter"
        - name: KUBERNETES_RUNTIME_CLASS
          value: "gvisor"
        - name: KUBERNETES_CLEANUP_AFTER_TASK
          value: "true"
        - name: RUNNER_MAX_CONCURRENT
          value: "2"
        - name: DEFAULT_AGENT_IMAGE
          value: chetter-runner:latest
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: "1"
            memory: 1Gi
EOF
```

Key differences from the Docker-mode runner:
- No `docker-sock` volume mount.
- No `runner-data` hostPath mount.
- `EXECUTION_BACKEND=kubernetes` instead of `RUNNER_LOCAL=true`.
- ServiceAccount with RBAC for Pod/ConfigMap/Secret/PVC management.

### Interim: Docker-Mode Runner (Before Kubernetes Executor)

To validate the rest of the stack while the Kubernetes executor is still being built,
use the existing Docker-mode runner with the Docker socket:

```bash
kubectl apply -f deploy/k8s/runner-deployment.yaml
```

This mounts `/var/run/docker.sock` and uses `RUNNER_LOCAL=true`. It works on k3s
because k3s nodes run Docker (or containerd with Docker CLI compatibility). The
agent containers will be created on the host, not as Kubernetes Pods.

## Step 10: Submit A Test Task

Once the runner is connected, submit a task via the MCP endpoint:

```bash
# Port-forward the MCP endpoint
kubectl -n chetter port-forward svc/chetter-mcp 18088:8080 &

# Submit a trivial task (using curl against the MCP server)
# This requires an MCP client. Alternatively use the web UI or chetterctl.
```

Or use the web UI at `http://localhost:18090` to submit a task and watch its progress.

## Step 11: Verify

Check that all components are running:

```bash
kubectl -n chetter get pods
kubectl -n chetter get svc
```

Check runner health:

```bash
# Via MCP
curl -s http://localhost:18088/healthz

# Via web UI
# Open http://localhost:18090
```

If using Kubernetes mode, verify agent pods are created:

```bash
kubectl -n chetter get pods -l app=chetter-agent
kubectl -n chetter describe pod chetter-task-<task-id>
```

Verify gVisor is in use:

```bash
kubectl -n chetter get pod chetter-task-<task-id> \
  -o jsonpath='{.spec.runtimeClassName}'
# Should print: gvisor
```

## Cleanup

```bash
# Delete all Chetter resources
kubectl delete namespace chetter

# Remove gVisor RuntimeClass
kubectl delete runtimeclass gvisor

# Stop k3s
sudo /usr/local/bin/k3s-uninstall.sh
```

## Troubleshooting

### Pod stuck in `ContainerCreating`

Check events:

```bash
kubectl -n chetter describe pod <pod-name>
```

Common causes:
- Missing CNI plugins (see Step 2).
- Image not imported into k3s (see Step 5).
- RuntimeClass not found (see Step 3).

### Runner cannot connect to server

Verify the service:

```bash
kubectl -n chetter get svc chetter-mcp
kubectl -n chetter exec deployment/chetter-runner -- \
  curl -s http://chetter-mcp:8080/healthz
```

### TiDB not ready

```bash
kubectl -n chetter logs statefulset/tidb
kubectl -n chetter exec -ti tidb-0 -- mysql -h 127.0.0.1 -P 4000 -e "SELECT 1"
```

### gVisor pod fails with `RuntimeHandler "runsc" not supported`

containerd is not configured for `runsc`. See Step 3 in `docs/K3S.md`.

## What This Validates

- k3s cluster works with CNI networking.
- gVisor (`runsc`) is installed and RuntimeClass is functional.
- TiDB runs as a StatefulSet.
- Chetter server starts and connects to TiDB.
- Runner connects to server and claims tasks.
- Agent pods are created with `runtimeClassName: gvisor` (once Kubernetes executor is implemented).
- No Docker socket is required for the runner (Kubernetes mode).
