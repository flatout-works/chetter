# Deployment

Chetter runs the server and runners on Docker Compose or Kubernetes. The runner
supports two production execution backends — `docker` (per-task containers via
the host Docker socket) and `kubernetes` (per-task Pods via the Kubernetes API)
— plus a development-only `local` backend. See [EXECUTION.md](EXECUTION.md) for
the backend contract.

Deployment guides:

| Guide | Scope |
|---|---|
| [MANUAL.md](MANUAL.md) | Quick start with Docker Compose, env vars, common operations. |
| [K3S.md](K3S.md) | Canonical local k3s guide for validating the Kubernetes backend. |
| [EKS.md](EKS.md) | Production EKS (or similar managed Kubernetes) installation. |
| [testing/k3d-gvisor.md](testing/k3d-gvisor.md) | Local Kubernetes testing with k3d and gVisor. |
| [TIDB-WOWBAGGER.md](TIDB-WOWBAGGER.md) | TiDB cluster bootstrap and migration runbook (wowbagger). |

## How The Runner Fleet Works

The runner uses a stateless pull model: it connects to the MCP server over
HTTP, long-polls `ClaimTask` to pick up work, sends heartbeats, and reports
task events. Each claim receives a fresh execution claim ID; task events, lease
renewal, and runner GitHub operations are rejected unless task, execution,
runner, claim, running status, and lease ownership all match. No special
protocols, no broker, no runner pre-registration. The MCP server's `ClaimTask`
uses `SELECT ... FOR UPDATE SKIP LOCKED` for atomic task assignment. Scaling is
`kubectl scale deployment chetter-runner --replicas=N`.

On GKE, use [GKE Sandbox](https://cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) instead of a custom gVisor RuntimeClass.

## Graceful Shutdown

Both the server and runner handle SIGTERM/SIGINT gracefully for zero-downtime rolling deployments.

**Server**: On SIGTERM, the server stops accepting new connections (MCP + web API), drains in-flight webhook processing goroutines, stops the reaper/cron/sync loops (aborting the current cycle early), closes the database, and exits 0. A second SIGTERM during the drain force-exits with code 1. The drain timeout is configurable via `CHETTER_SHUTDOWN_TIMEOUT` (default `15s`).

**Runner**: On SIGTERM, the runner marks itself `draining`, sends a final heartbeat, stops claiming new tasks, waits for in-flight tasks to finish (up to `CHETTER_DRAIN_TIMEOUT_SEC`, default 30s), and force-cancels any that overrun. The drain deadline is derived from each task's remaining timeout (clamped by `CHETTER_DRAIN_TIMEOUT_SEC`), preventing premature force-cancel of long-running tasks. Resumable sessions are paused (workspace preserved) before force-cancel, enabling later resume by a fresh runner.

After the drain deadline expires the runner enters a **forced-cleanup completion barrier**: it blocks exit until every force-cancelled task's teardown — sandbox teardown, workspace destroy, checkpoint/session-export flush — and its terminal `ReportTaskEvents` delivery have completed, so a rolling deploy never exits mid-cleanup or loses an in-flight terminal result. The barrier is bounded by `CHETTER_DRAIN_HARD_KILL_TIMEOUT_SEC` (default `60s`); terminal-report retries are clamped to the same budget so reporting provably finishes before exit. When the hard-kill timeout fires, the runner logs each still-in-flight execution with its terminal-report delivery status (and exits 1). Keep drain timeout + hard-kill timeout below the pod's `terminationGracePeriodSeconds` — the deployment defaults are 60s + 30s = 90s. Exits 0 on a clean drain, 1 if tasks were force-cancelled. See issue #313.

## Task Queue Admission Control

Set `CHETTER_MAX_PENDING_TASKS` (default `0` = disabled) to cap how many tasks may sit in the `pending` state waiting for a runner. When the cap is reached, every ingress path — MCP submit, web API, webhooks, triggers, rerun, recovery, and session resume — rejects new work with a retryable capacity error and records a `task_admission_rejected` audit event instead of storing the task. Completed, cancelled, and claimed tasks release pending capacity automatically, so a rejected submission can simply be retried.

Concurrent submissions cannot overshoot the limit: the server serializes admission checks using a database row lock (`admission_locks` table, `READ COMMITTED` transaction), so the cap is enforced strictly even across multiple server replicas. The reaper's lease-expiry recovery can transiently push `pending` above the cap — a claimed task releases a slot, the slot is reused, and the original task's lease then expires and requeues — but no further admissions occur until the count drops back below the limit. Queue depth is observable at any time via the `chetter_tasks{status="pending"}` Prometheus gauge, the fleet-health `PendingTasks` field, and the reaper's task metrics. See issue #50.

## Event Callback Recursion Guard

`create_task` event callbacks are checked against a provenance-depth limit to stop misconfigured recursion loops (a `task.completed` → `create_task` callback whose spawned tasks also emit `task.completed` would otherwise create tasks forever). Each callback-spawned task records its parent task and its depth in the chain (`tasks.callback_parent_task_id`, `tasks.callback_depth`); when a callback would spawn a task deeper than `CHETTER_CALLBACK_MAX_DEPTH` (default `5`), the spawn is rejected — a `task.callback_rejected` event with error `event_callback_recursion_limit` is recorded on the parent task's event stream and an `event_callback_recursion_limit` audit event is emitted. Only the specific recursive chain is stopped; the callback itself remains enabled for unrelated tasks. Set `CHETTER_CALLBACK_MAX_DEPTH` to `0` to disable the guard. See issue #312.

## Multi-Replica Coordination

The server supports horizontal scaling across multiple replicas using database-only
coordination (no Redis or external pub/sub). Five tiers ensure correctness:

| Tier | Mechanism | Table | Latency |
|---|---|---|---|
| Claim wake-up | `claim_notify_counter` polled per replica every 1s | `claim_notify_counter` | ~1s |
| Cron dedup | `SELECT ... FOR UPDATE` + interval marker | `trigger_locks` | immediate |
| Pending admission | `READ COMMITTED` tx + row lock | `admission_locks` | immediate |
| Drain requests | Durable row delivered on every heartbeat until the runner reports draining | `runner_drain_requests` | next heartbeat |
| Event/fleet streams | Per-stream 1s task-event poll; per-replica 3s fleet-cursor poll into the local bus | `task_events` (indexed) | 1–3s |

All coordination tables are created by `ApplySchema()` on startup and have matching
Goose migrations (`054` for MySQL/TiDB, `030` for PostgreSQL). The `claimNotifier`
in-process broadcast remains the low-latency fast path; the DB poller is the
cross-replica fallback that triggers the same broadcast.

### Known Multi-Replica Limitations

- **Output redaction is per-replica.** Redaction sets are built from plaintext
  secret env values at submission time and are deliberately never persisted
  (`sanitizeTaskEnv` stores `[redacted]` placeholders). With a shared claim
  queue, a runner on another replica may claim a task whose redaction set
  lives only on the submitting replica; events reported there are stored
  unredacted. Runners only ever receive sanitized env values (credentials are
  injected by name and resolved runner-side), which bounds the practical
  exposure, but secret-bearing tasks are best kept on single-replica
  deployments until encrypted set sharing exists.
- **Event cursors are timestamp-based.** Cross-replica event catch-up uses
  `created_at` cursors, so server clocks must be roughly synchronized (NTP).
  An event committed with a timestamp earlier than an already-observed cursor
  (clock skew plus a slow-committing transaction) can be skipped on streams;
  clients recover by re-reading from the DB.
- **Trigger enable/disable is eventually consistent.** The persisted
  `enabled` flag is re-checked on every trigger tick, so a trigger disabled
  on one replica stops firing on other replicas within one tick — but the
  in-flight tick may still fire once.
- **Drain delivery is at-least-once.** The drain command is re-delivered on
  every heartbeat until the runner reports a draining status, and a request
  row is dropped after 30 minutes without an acknowledgement.

### Scaling Notes

- Each replica should have its own runner fleet; runners connect to a specific
  replica via the runner RPC endpoint.
- The 15s safety-net poll remains as a backstop even with the DB counter poller.
- Fleet-wide streaming (task events, fleet updates) is eventually consistent:
  events from other replicas arrive within 1–3s via DB polling.

## Docker + gVisor

### Install gVisor On The Host

```bash
curl -fsSL https://gvisor.dev/archive.key | \
  sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
  sudo tee /etc/apt/sources.list.d/gvisor.list
sudo apt-get update && sudo apt-get install -y runsc
sudo /usr/bin/runsc install
sudo systemctl restart docker
docker run --runtime=runsc --rm alpine dmesg  # verify: should show "Starting gVisor..."
```

### Enable In Compose

Add `USE_GVISOR=true` to `.env`:

```yaml
chetter-runner:
  environment:
    EXECUTION_BACKEND: docker
    USE_GVISOR: "true"
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
```

The runner passes `--runtime=runsc` to `docker run` when creating agent containers. The host Docker daemon needs `runsc` registered, and the runner container also needs the `runsc` binary on its PATH: the isolation gate (`enforcedIsolation()` in `runner/internal/controller/isolation.go`) probes for it before advertising isolation capability to the server. The Docker socket mount is required because the runner shells out to `docker run`, and `deploy/compose.yaml` bind-mounts the host binary read-only (`/usr/bin/runsc:/usr/bin/runsc:ro`) to satisfy the probe.

## Sandbox Isolation

Chetter uses [gVisor](https://gvisor.dev/) (`runsc`) as its sandboxed execution runtime. gVisor provides an application kernel (the Sentry) written in Go that intercepts every system call the container makes and implements the Linux ABI in userspace. The application never touches the host kernel directly.

Plain Docker (`runc`) and local execution are convenience modes for trusted workloads. They are not task security boundaries, even if Docker containers, proxy settings, or DNS filtering are present. Enable `USE_GVISOR=true` for untrusted workloads or multi-tenant deployments.

### Why gVisor Over Alternatives

| Requirement | gVisor | Kata Containers | Sysbox | Daytona |
|---|---|---|---|---|
| Isolation model | Application kernel | Micro-VM | User namespaces | VM + sandbox lifecycle |
| Streaming interaction | Yes | No (batch only) | Yes | Yes |
| Standard EKS/GKE (no custom AMI) | Yes (DaemonSet) | No (needs nested virt) | No (host daemon) | No (9+ service CP) |
| Kernel-level isolation | Yes | Yes | Partial | Yes |
| Integration complexity | Low | High | Medium | Very high |

**Kata Containers** were removed from Chetter — they cannot expose a port from the micro-VM for the interactive serve flow and require nested virtualization.

### Enabling gVisor On Kubernetes

Install with a DaemonSet that copies `runsc` onto each node and updates containerd:

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

Register the RuntimeClass:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
```

Set `runtimeClassName: gvisor` on runner pods. On GKE, use [GKE Sandbox](https://cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) instead — no DaemonSet needed.

### Trade-off

gVisor adds per-syscall latency because every call is intercepted by the Sentry. For coding agent workloads (file I/O, git, compilation, HTTP calls) this is negligible. For syscall-heavy workloads (databases, high-frequency networking) the overhead can be noticeable. Runners can fall back to standard `runc` by omitting `runtimeClassName: gvisor` from the pod spec.

### Network Isolation

Regardless of the container runtime, Chetter runners provide outbound network filtering via a transparent HTTP proxy and DNS proxy. The proxy enforces an allowlist of domains and blocks everything else.

> **Note:** gVisor sandboxes the agent process only. It does not sandbox the network or the MCP bridge. Proxy/DNS filtering is an operational control, not a task security boundary.

### Monitoring The Sandbox Runtime

Once a task is running inside a sandbox, operators need visibility into the sandbox runtime itself: runsc start/teardown failures, sandbox crashes, and resource pressure are distinct from ordinary agent failures. Chetter collects per-sandbox metrics on the runner and surfaces them through heartbeats, the fleet metrics endpoint, and the web UI. See issue #302.

**Runner heartbeat fields** (visible in the runner fleet page and in `chetter_runner_health`):

| Field | Meaning |
|---|---|
| `sandbox_available` | The runsc binary is present on the runner and `runsc --version` succeeds (docker mode). In Kubernetes mode this reports the configured runtime class (`gvisor` = available). A runner that was advertising isolation but now reports `sandbox_available=false` has sandbox drift — investigate before admitting new tasks. |
| `sandbox_total` | Cumulative sandboxes started successfully since the runner started. |
| `sandbox_start_failures` | Cumulative `docker run` failures classified as sandbox runtime failures (runsc/OCI errors). |
| `sandbox_crashes` | Cumulative sandboxes that died from an infrastructure failure mid-task (daemon-recorded runsc/sandbox error, or an unexpected non-zero exit while the harness was still running). |
| `sandbox_start_latency_ms` | Cumulative sandbox start latency (docker run wall time), for average start cost. |
| `sandbox_lifetime_ms` | Cumulative sandbox lifetime (start to teardown), for average sandbox age. |
| `sandbox_max_rss_mb`, `sandbox_max_cpu_percent` | Peak sandbox RSS/CPU observed at teardown — a proxy for in-sandbox resource pressure. |

**Prometheus metrics** (`/metrics`, aggregate across active runners):

- `chetter_runner_sandbox_available{status="available"|"unavailable"}` — count of runners by sandbox runtime availability.
- `chetter_runner_sandbox{metric="total"|"start_failures"|"crashes"|"start_latency_ms"}` — cumulative per-sandbox counters summed across active runners.
- `chetter_task_failures{failure_category="..."}` — failed tasks by task-level failure category; sandbox infrastructure failures are counted under `harness_error` (the task-level failure category shared with other runner-side infrastructure failures).

**Failure categories** — sandbox lifecycle failures are reported as distinct per-attempt error categories and stored in `task_events` (e.g. `task.failed.sandbox_start_failed`, `task.failed.sandbox_crashed`):

- `sandbox_start_failed` — the sandbox could not be started (`docker run` failed with a runsc/OCI runtime error). This is a terminal per-attempt failure, retried by the normal attempt/backoff machinery.
- `sandbox_crashed` — the sandbox died while the task was running (runsc/sandbox runtime error, or an unexpected non-zero exit with a daemon-recorded error in serve mode). Also terminal per-attempt; the task is retried up to `max_attempts` like any other infrastructure failure.

Both map to the task-level `harness_error` failure category, so the reaper and session-recovery paths treat them exactly like `isolation_unavailable` and other runner-side infrastructure failures: the failed attempt feeds `task_events`, the task is eligible for a fresh execution attempt, and the failure counts appear in `chetter_task_failures`. Unlike an OOM kill (which is reported as `resource_limit` and `OOMKilled`), these categories mean the sandbox runtime itself — not the agent or the memory limit — was the cause.
