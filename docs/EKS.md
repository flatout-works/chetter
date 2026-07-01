# Chetter on EKS — Production Installation Guide

This guide covers installing Chetter into a stock AWS EKS cluster (or similar managed
Kubernetes environment) with gVisor sandboxing. It assumes you have an EKS cluster
and `kubectl` configured.

For local validation on k3s, see `docs/testing/k3s-chetter.md`.

## Architecture

```text
EKS Cluster
  ├── Node Group: system (standard, no gVisor)
  │    └── Chetter MCP server, TiDB (or external TiDB Cloud)
  │
  ├── Node Group: gvisor (custom AMI with runsc)
  │    └── Chetter runner (EXECUTION_BACKEND=kubernetes)
  │         └── Creates agent Pods (runtimeClassName: gvisor)
  │
  ├── Ingress / Load Balancer
  │    └── TLS termination → chetter-mcp service
  │
  └── TiDB Cloud (external) or in-cluster TiDB
```

## Prerequisites

- An EKS cluster (1.28+ recommended) with `kubectl` access.
- At least two node groups (see below).
- A container registry (ECR or GHCR) with Chetter images pushed.
- TiDB Cloud or an in-cluster TiDB instance.
- An LLM provider API key.
- A GitHub App for webhook/PR review automation (optional but recommended).

## Step 1: Prepare Node Groups

### System Node Group

Standard EKS nodes (Amazon Linux 2 or Bottlerocket). No special configuration.
This runs the Chetter server, web UI, and optionally TiDB.

```bash
# Example: create a managed node group for system workloads
aws eks create-nodegroup \
  --cluster-name chetter-prod \
  --nodegroup-name system \
  --node-role <node-role-arn> \
  --subnets <subnet-ids> \
  --instance-types t3.large \
  --desired-size 2 \
  --min-size 1 \
  --max-size 4 \
  --labels node-role=system \
  --taints no-sandbox=true:NoSchedule
```

### gVisor Node Group

gVisor requires `runsc` installed on the node and containerd configured with a
`runsc` runtime handler. EKS does not provide this by default. You need either:

**Option A: Custom AMI with gVisor preinstalled**

Build an EKS-optimized AMI with `runsc` installed and containerd configured:

1. Start from the standard EKS-optimized AMI.
2. Install `runsc`:

```bash
apt-get update && apt-get install -y gvisor
```

3. Configure containerd (`/etc/containerd/config.toml`):

```toml
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
```

4. Restart containerd and bake the AMI.
5. Use this AMI for the gVisor node group.

**Option B: DaemonSet installer (easier, slower boot)**

Use a DaemonSet that installs `runsc` on each node at startup. The existing
`deploy/k8s/gvisor-runtimeclass.yaml` does this for Debian-based nodes:

```bash
kubectl apply -f deploy/k8s/gvisor-runtimeclass.yaml
```

This DaemonSet:
- Runs an init container that installs `runsc` and copies the binary to `/usr/local/bin`
  on the host.
- Creates a `RuntimeClass` named `gvisor` with handler `runsc`.

> **Warning:** The DaemonSet installer requires privileged access and host path mounts.
> For production, prefer Option A (custom AMI). Use the DaemonSet only for testing.

Create the gVisor node group:

```bash
aws eks create-nodegroup \
  --cluster-name chetter-prod \
  --nodegroup-name gvisor \
  --node-role <node-role-arn> \
  --subnets <subnet-ids> \
  --instance-types t3.xlarge \
  --desired-size 2 \
  --min-size 1 \
  --max-size 6 \
  --labels node-role=gvisor \
  --taints sandbox=gvisor:NoSchedule
```

The taint ensures only agent pods with the gVisor toleration are scheduled on these nodes.

## Step 2: Configure Container Registry

Push Chetter images to ECR or GHCR:

```bash
# ECR example
aws ecr create-repository --repository-name chetter/mcp
aws ecr create-repository --repository-name chetter/runner

# Tag and push
docker tag chetter-mcp:latest <account>.dkr.ecr.<region>.amazonaws.com/chetter/mcp:latest
docker push <account>.dkr.ecr.<region>.amazonaws.com/chetter/mcp:latest

docker tag chetter-runner:latest <account>.dkr.ecr.<region>.amazonaws.com/chetter/runner:latest
docker push <account>.dkr.ecr.<region>.amazonaws.com/chetter/runner:latest
```

If using ECR, ensure nodes have `ecr:GetAuthorizationToken` and `ecr:BatchGetImage`
permissions via the node IAM role.

## Step 3: Create Namespace And Secrets

```bash
kubectl create namespace chetter
```

Create secrets with your tokens and provider keys:

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: chetter-secrets
  namespace: chetter
type: Opaque
stringData:
  CHETTER_MCP_AUTH_TOKEN: "<long-random-token>"
  CHETTER_RUNNER_RPC_TOKEN: "<long-random-token>"
  DATABASE_DSN: "root@tcp(<tidb-host>:4000)/chetter?parseTime=true&tls=true"
  DEEPSEEK_API_KEY: "<key>"
  OPENCODE_API_KEY: "<key>"
  SYNTHETIC_API_KEY: "<key>"
  GITHUB_TOKEN: "<token>"
EOF
```

> Use Kubernetes Secrets or External Secrets Operator for production. Never commit
> real secrets to Git.

## Step 4: Deploy TiDB

### Option A: TiDB Cloud (Recommended)

Use [TiDB Cloud Serverless or Dedicated](https://tidbcloud.com). Set `DATABASE_DSN`
in the Chetter secrets to the TiDB Cloud connection string with TLS:

```
DATABASE_DSN=root@tcp(gateway.<region>.aws.tidbcloud.com:4000)/chetter?parseTime=true&tls=true
```

This is the production-recommended option. No in-cluster TiDB to manage.

### Option B: In-Cluster TiDB

For development or self-hosted production, deploy TiDB using the TiDB Operator or
a simple StatefulSet. The k3d TiDB manifest works but uses `unistore` (test engine):

```bash
kubectl apply -f deploy/k3d/tidb.yaml
```

For production in-cluster TiDB, use the [TiDB Operator](https://docs.pingcap.com/tidb-in-kubernetes/).

## Step 5: Create RuntimeClass

If not already created by the DaemonSet installer:

```bash
kubectl apply -f - <<'EOF'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
EOF
```

## Step 6: Deploy Chetter Server

```bash
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chetter-mcp
  namespace: chetter
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
      nodeSelector:
        node-role: system
      tolerations:
      - key: no-sandbox
        operator: Equal
        value: "true"
        effect: NoSchedule
      containers:
      - name: mcp
        image: <registry>/chetter/mcp:latest
        imagePullPolicy: Always
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
          value: "<registry>/chetter/runner:latest"
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
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 15
          periodSeconds: 20
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

## Step 7: Deploy The Runner (Kubernetes Mode)

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
  replicas: 2
  selector:
    matchLabels:
      app: chetter-runner
  template:
    metadata:
      labels:
        app: chetter-runner
    spec:
      serviceAccountName: chetter-runner
      nodeSelector:
        node-role: gvisor
      tolerations:
      - key: sandbox
        operator: Equal
        value: gvisor
        effect: NoSchedule
      containers:
      - name: runner
        image: <registry>/chetter/runner:latest
        imagePullPolicy: Always
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
        - name: KUBERNETES_AGENT_IMAGE_PULL_POLICY
          value: "Always"
        - name: RUNNER_MAX_CONCURRENT
          value: "2"
        - name: DEFAULT_AGENT_IMAGE
          value: "<registry>/chetter/runner:latest"
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: "2"
            memory: 2Gi
EOF
```

Key points:
- **No Docker socket mount.** The runner uses the Kubernetes API.
- **Node selector + toleration** schedules the runner on gVisor nodes.
- **ServiceAccount + RBAC** grants Pod/ConfigMap/Secret/PVC management.
- **`EXECUTION_BACKEND=kubernetes`** selects the Kubernetes executor.

## Step 8: Configure Ingress

Expose the MCP endpoint and web UI through an AWS Load Balancer:

### Option A: AWS Load Balancer Controller (NLB)

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Service
metadata:
  name: chetter-mcp-nlb
  namespace: chetter
  annotations:
    service.beta.kubernetes.io/aws-load-balancer-type: nlb
    service.beta.kubernetes.io/aws-load-balancer-scheme: internet-facing
spec:
  type: LoadBalancer
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

Get the external hostname:

```bash
kubectl -n chetter get svc chetter-mcp-nlb
```

### Option B: Ingress With TLS (Recommended)

Use the AWS Load Balancer Controller with an ALB and cert-manager:

```bash
kubectl apply -f - <<'EOF'
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: chetter
  namespace: chetter
  annotations:
    alb.ingress.kubernetes.io/scheme: internet-facing
    alb.ingress.kubernetes.io/target-type: ip
    alb.ingress.kubernetes.io/listen-ports: '[{"HTTPS": 443}, {"HTTP": 80}]'
    alb.ingress.kubernetes.io/ssl-redirect: '443'
spec:
  tls:
  - hosts:
    - chetter.example.com
    secretName: chetter-tls
  rules:
  - host: chetter.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: chetter-mcp
            port:
              number: 8090
      - path: /mcp
        pathType: Prefix
        backend:
          service:
            name: chetter-mcp
            port:
              number: 8080
EOF
```

Use cert-manager with Let's Encrypt or AWS Certificate Manager for the TLS certificate.

## Step 9: Configure GitHub Webhook (Optional)

If using PR review or issue triggers, configure the GitHub App:

1. Create a GitHub App with:
   - Webhook URL: `https://chetter.example.com/webhook/github`
   - Webhook secret: a random string
   - Permissions: issues (read/write), pull requests (read/write), contents (read)
2. Set in `chetter-secrets`:

```
GITHUB_APP_ID=<app-id>
GITHUB_INSTALLATION_ID=<installation-id>
GITHUB_APP_PRIVATE_KEY_B64=<base64-encoded-private-key>
GITHUB_WEBHOOK_SECRET=<webhook-secret>
```

3. Restart the MCP server to pick up the new secrets.

## Step 10: Verify

```bash
# Check all pods
kubectl -n chetter get pods -o wide

# Check services
kubectl -n chetter get svc

# Check runner health via MCP
curl -s https://chetter.example.com/healthz

# Check runner logs
kubectl -n chetter logs -l app=chetter-runner --tail=50

# Verify runner is connected to server
kubectl -n chetter logs -l app=chetter-runner | grep "connected"
```

## Agent Pod Configuration

When the runner creates an agent pod, it uses these conventions:

| Setting | Value |
|---|---|
| Pod name | `chetter-task-<task-id>` |
| Namespace | From `KUBERNETES_NAMESPACE` |
| RuntimeClass | From `KUBERNETES_RUNTIME_CLASS` (e.g. `gvisor`) |
| Labels | `app=chetter-agent`, `chetter.io/task-id=<id>`, `chetter.io/runner-id=<id>` |
| Workspace volume | `emptyDir` (non-resumable) or PVC (resumable) |
| Agent port | 9999 |
| Runner connects via | Pod IP directly |

## Storage Classes

For resumable sessions, the runner creates PVCs. Ensure a StorageClass is available:

```bash
kubectl get storageclass
```

EKS provides `gp2` by default. For gVisor nodes, ensure the StorageClass is available
on those nodes (EBS is accessible from any node in the same AZ).

## Monitoring

### Logs

```bash
# Server logs
kubectl -n chetter logs -l app=chetter-mcp --tail=200

# Runner logs
kubectl -n chetter logs -l app=chetter-runner --tail=200

# Agent pod logs (during task)
kubectl -n chetter logs chetter-task-<task-id> -c agent
```

### Health

The server exposes `/healthz` on port 8080. Use this for readiness and liveness probes.

The runner does not expose a health endpoint yet. Monitor runner health through the
Chetter server's runner health API:

```bash
# Via MCP tools
# chetter_runner_health
```

## Scaling

| Component | How to scale |
|---|---|
| Chetter server | Increase `replicas` in the MCP Deployment |
| Runner | Increase `replicas` in the runner Deployment |
| gVisor nodes | Increase the node group desired size |
| Agent pods | Managed automatically by the runner fleet |

Runner concurrency per pod is controlled by `RUNNER_MAX_CONCURRENT`. Each runner pod
can handle multiple concurrent tasks, each creating one agent pod.

## Security Considerations

- **No Docker socket:** The Kubernetes-mode runner does not mount `/var/run/docker.sock`.
  Agent pods are managed by Kubernetes, not Docker.
- **RBAC:** The runner ServiceAccount is namespace-scoped. It cannot create pods outside
  the `chetter` namespace.
- **gVisor isolation:** Agent pods run with `runtimeClassName: gvisor`, providing kernel-level
  sandboxing. The agent cannot access host filesystem, kernel syscalls, or other pods.
- **Secrets:** Use Kubernetes Secrets (or External Secrets Operator) for API keys and tokens.
  Do not embed secrets in PodSpec env directly.
- **Node taints:** gVisor nodes are tainted to prevent non-agent workloads from scheduling.
  Only the runner (with toleration) and agent pods (with runtimeClassName) land on these nodes.

## Backup

- **TiDB Cloud:** Managed backups are included.
- **In-cluster TiDB:** Use TiDB Backup/Restore or volume snapshots.
- **Definitions repo:** Triggers, agents, and model catalogs are in Git. Back up the repo.
- **Kubernetes secrets:** Back up with `kubectl get secret` or use External Secrets.

## Upgrading

1. Build and push new images to your registry.
2. Update the Deployment image tag:

```bash
kubectl -n chetter set image deployment/chetter-mcp mcp=<registry>/chetter/mcp:<new-tag>
kubectl -n chetter set image deployment/chetter-runner runner=<registry>/chetter/runner:<new-tag>
```

3. Roll out:

```bash
kubectl -n chetter rollout status deployment/chetter-mcp
kubectl -n chetter rollout status deployment/chetter-runner
```

4. If the runner needs draining (in-flight tasks):

Use the Chetter MCP tool `chetter_drain_runner` to gracefully stop the runner before
updating the image.
