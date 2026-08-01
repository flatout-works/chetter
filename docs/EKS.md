# Chetter on EKS — Production Installation Guide

This guide covers installing Chetter into AWS EKS (or a similar managed Kubernetes
environment). A stock EKS cluster is not sufficient for the supplied Kubernetes runner:
you must provide an RWX workspace StorageClass (normally EFS CSI), install a compatible
RuntimeClass if gVisor is enabled, and ensure RuntimeClass scheduling or cluster policy
places agent Pods on compatible nodes.

For local validation on k3s, see `docs/K3S.md`.

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

**Option B: Operator-managed node bootstrap**

Use your node-image/bootstrap pipeline to install `runsc`, configure the containerd
handler, restart containerd, and register the RuntimeClass. The reference
`deploy/k8s/gvisor-runtimeclass.yaml` is not a complete stock-EKS installer: copying a
binary does not configure EKS containerd, and its DaemonSet is intentionally excluded
from the generic kustomization. Do not apply it as a production installation procedure.

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

Create the `chetter` database before starting the Chetter server unless it already
exists. For TiDB Cloud, create it through the SQL endpoint with an administrative user,
then use an application user scoped to that database in `DATABASE_DSN`.

### Option B: In-Cluster TiDB

For development or self-hosted production, deploy TiDB using the TiDB Operator or
a simple StatefulSet. The k3d TiDB manifest works but uses `unistore` (test engine):

```bash
kubectl apply -f deploy/k3d/tidb.yaml
```

For production in-cluster TiDB, use the [TiDB Operator](https://docs.pingcap.com/tidb-in-kubernetes/).
Create the `chetter` database before deploying the server. Chetter applies table
migrations, but a TiDB/MySQL DSN cannot select a database that does not yet exist.

## Step 5: Create RuntimeClass

Create a RuntimeClass whose scheduling section selects and tolerates the gVisor node
group. Without this section (or an equivalent admission policy), child agent Pods do
not inherit the runner Pod's node selector or tolerations:

```bash
kubectl apply -f - <<'EOF'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
scheduling:
  nodeSelector:
    node-role: gvisor
  tolerations:
    - key: sandbox
      operator: Equal
      value: gvisor
      effect: NoSchedule
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
          value: "<registry>/chetter/agent-golang:latest"
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
            path: /readyz
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

Use `deploy/k8s/runner-rbac.yaml`, `runner-workspace-pvc.yaml`, and
`runner-deployment.yaml` as the authoritative base. Before applying them:

- Set `storageClassName` on the PVC to an RWX-capable EFS CSI class. The default EBS
  classes are RWO and are not suitable for runner and agent Pods on different nodes.
- Install the `gvisor` RuntimeClass separately, or clear `KUBERNETES_RUNTIME_CLASS`.
  `deploy/k8s/gvisor-runtimeclass.yaml` is not part of the generic kustomization.
- Ensure the server resolves `DEFAULT_AGENT_IMAGE` to an actual agent image containing
  harness CLIs; never use the tight runner daemon image as the agent image.
- Keep the downward API values for `RUNNER_ID`, `POD_NAME`, `POD_UID`, `POD_IP`, and
  `NODE_NAME`. The Pod UID prevents runner replicas sharing a PVC from sharing identity.

The following is an abridged structural example, not a directly applicable manifest;
the repository manifests include workspace volumes, owner identity, drain timing, and
the least-privilege RBAC rules.

```yaml
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
  resources: [pods]
  verbs: [create, get, list, delete]
- apiGroups: [""]
  resources: [pods/attach]
  verbs: [create, get]
- apiGroups: [""]
  resources: [pods/log]
  verbs: [get]
- apiGroups: [""]
  resources: [secrets]
  verbs: [create, get, list, delete]
- apiGroups: [""]
  resources: [events]
  verbs: [list]
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
        - name: RUNNER_ID
          valueFrom:
            fieldRef:
              fieldPath: metadata.uid
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_UID
          valueFrom:
            fieldRef:
              fieldPath: metadata.uid
        - name: POD_IP
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: KUBERNETES_RUNTIME_CLASS
          value: "gvisor"
        - name: KUBERNETES_CLEANUP_AFTER_TASK
          value: "true"
        - name: KUBERNETES_AGENT_IMAGE_PULL_POLICY
          value: "Always"
        - name: KUBERNETES_WORKSPACE_PVC
          value: "chetter-runner-workspaces"
        - name: RUNNER_MAX_CONCURRENT
          value: "2"
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: "2"
            memory: 2Gi
```

Key points:
- **No Docker socket mount.** The runner uses the Kubernetes API.
- **RuntimeClass scheduling:** the RuntimeClass or cluster policy must schedule child
  agent Pods onto nodes configured with the selected runtime handler. Scheduling only
  the runner does not automatically schedule its child Pods to the same node.
- **ServiceAccount + RBAC:** grants only the Pod, attach/log, Secret, and Event operations
  used by the controller. Agent Pods use a separate ServiceAccount with token automount disabled.
- **`EXECUTION_BACKEND=kubernetes`** selects the Kubernetes executor.
- **Runner authentication:** `CHETTER_RUNNER_RPC_TOKEN` must have the same value in the
  server and runner Pods. `EXECUTION_BACKEND` is the only backend environment selector.

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
   - Permissions: issues (read/write), pull requests (read/write), contents (read/write)
   Install this same App on each organization or user account Chetter should
   automate, selecting the intended repositories in each installation.
2. Set in `chetter-secrets`:

```
GITHUB_APP_ID=<app-id>
GITHUB_APP_PRIVATE_KEY_B64=<base64-encoded-private-key>
GITHUB_WEBHOOK_SECRET=<webhook-secret>
```

Do not set `GITHUB_INSTALLATION_ID` on new deployments. Chetter selects signed
webhook installations and discovers installations for manual or scheduled
tasks from their repository identity.

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

# Verify runner is claiming over ConnectRPC
kubectl -n chetter logs -l app=chetter-runner | grep "claiming tasks via ConnectRPC"
```

Before the first task, create a default managed Git identity through the web UI or the
`chetter_create_git_identity` and `chetter_set_git_identity_default` MCP tools. Task
submission requires an author identity even when the task does not clone a repository.

Submit a small task, then verify its child Pod while it is running:

```bash
kubectl -n chetter get pods -l chetter.io/owned=true -w
kubectl -n chetter get pod <agent-pod> \
  -o jsonpath='runtimeClass={.spec.runtimeClassName}{"\n"}image={.spec.containers[0].image}{"\n"}workspaceSubPath={.spec.containers[0].volumeMounts[0].subPath}{"\n"}automountToken={.spec.automountServiceAccountToken}{"\n"}'
```

Expect the configured runtime class and requested agent image, a task/execution-specific
workspace subPath, and `automountToken=false`. After task finalization, both
`kubectl -n chetter get pods -l chetter.io/owned=true` and the equivalent Secret query
should return no resources. The complete MCP smoke-test commands are in `docs/K3S.md`.

## Agent Pod Configuration

When the runner creates an agent pod, it uses these conventions:

| Setting | Value |
|---|---|
| Pod name | DNS-safe `chetter-<task-fragment>-<runner/task/execution-hash>` |
| Namespace | From `KUBERNETES_NAMESPACE` |
| RuntimeClass | From `KUBERNETES_RUNTIME_CLASS` (e.g. `gvisor`) |
| Labels | Runner, task, execution, session, and prompt identity labels |
| Workspace volume | One shared PVC; only the validated execution workspace is mounted through `subPath` |
| Agent port | 9999 |
| Runner connects via | Pod IP directly |

## Storage Classes

The runner does not create per-task PVCs. Runner and agent Pods mount one operator-provided
RWX PVC, while each agent sees only its execution workspace subPath. Ensure an RWX
StorageClass is available:

```bash
kubectl get storageclass
```

EKS EBS `gp2`/`gp3` defaults are ReadWriteOnce and do not satisfy this requirement.
Use EFS CSI or another RWX implementation accessible from every eligible agent node.

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

The server exposes `/healthz` (liveness) and `/readyz` (readiness, performs a database ping) on port 8080. Use `/readyz` for readiness probes and `/healthz` for liveness probes. A Prometheus `/metrics` endpoint is also available on port 8080 without authentication.

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
- **gVisor isolation:** `runtimeClassName: gvisor` provides a syscall/kernel isolation
  boundary when the cluster handler is correctly installed. It does not enforce egress,
  prevent access to other reachable Pods, or replace NetworkPolicy.
- **Secrets:** Use Kubernetes Secrets (or External Secrets Operator) for API keys and tokens.
  Do not embed secrets in PodSpec env directly.
- **Scheduling:** configure RuntimeClass scheduling, admission policy, or equivalent node
  selection so agent Pods, not only runner Pods, land on gVisor-capable nodes.
- **Current limitation:** proxy environment variables route cooperative clients but are not
  an enforced egress boundary. Cross-task runner MCP authentication/network isolation is
  not provided by this recovery; apply cluster network controls appropriate to your threat model.

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
