# Snapshots

Status: **Design reference - partial implementation through resumable sessions**

## What Is a Snapshot?

A snapshot captures the **full execution state** of a running agent container — not just its filesystem, but also process memory, open file descriptors, network connections, and CPU register state. When restored, the agent resumes exactly where it left off as if no time had passed.

Chetter supports two snapshot mechanisms with different capabilities:

| Mechanism | What it captures | Restoration | Requires gVisor |
|---|---|---|---|
| **gVisor checkpoint/restore** | Filesystem + memory + processes + network state + open FDs | Process resumes mid-execution | Yes |
| **Docker commit** | Filesystem overlay only (modified/added files) | New container from image, process starts fresh | No |

gVisor checkpoint/restore is the preferred mechanism. It enables workflows that filesystem-only snapshots cannot.

## Why Snapshots?

Chetter's default state mechanism is Git — each task clones the repo fresh, the agent makes changes and commits/PRs them, and the workspace is ephemeral. This is simple and reliable, but it has gaps:

| Limitation | What snapshots solve |
|---|---|
| No way to pause mid-task and resume later | Checkpoint the container, restore in a future session — agent continues from the exact instruction |
| No pre-warmed dependency caches | Checkpoint after `npm install` / `go mod download`, restore to skip all initialization |
| No pre-warmed agent sessions | Checkpoint after agent startup (config loaded, providers initialized, HTTP server ready), restore to eliminate 10-15s startup overhead per task |
| No branching within a single task | Checkpoint before a risky change, restore if the agent goes down a bad path — memory state is rewound too |
| No recovery from agent crashes | Periodic checkpoints with `--leave-running`; if the agent crashes, restore the last good state instead of restarting from scratch |
| No task migration between runners | Checkpoint on one node, copy state files, restore on another (with CPU feature compatibility) |

## gVisor Checkpoint/Restore

### How It Works

gVisor's `runsc` runtime can checkpoint a sandboxed container's complete state to a directory on disk:

```
runsc checkpoint --image-path=/snapshots/{taskID} chetter-task-{taskID}
```

This writes multiple files to `--image-path`:
- **Kernel state** — gVisor sandbox kernel data structures
- **Memory pages** — the application's full virtual memory
- **Filesystem state** — open file descriptors, mount state, file offsets

To restore:

```
runsc create chetter-task-{taskID}
runsc restore --image-path=/snapshots/{taskID} chetter-task-{taskID}
```

The restored process resumes from the exact point it was checkpointed — same call stack, same heap, same open connections.

### Keep-Running Checkpoints

The `--leave-running` flag checkpoints without stopping the container:

```
runsc checkpoint --image-path=/snapshots/{taskID} --leave-running chetter-task-{taskID}
```

This is the key enabler for **periodic crash-recovery checkpoints** — the agent keeps working while snapshots are taken in the background. If the agent later crashes, restore from the last successful checkpoint.

### Docker Integration

When using Docker with `--runtime=runsc`, checkpoints work through Docker's native checkpoint API:

```bash
# Create checkpoint
docker checkpoint create chetter-task-{taskID} checkpoint-1

# Restore into the same container
docker start --checkpoint checkpoint-1 chetter-task-{taskID}
```

Note: Docker currently requires restoring into the same container (not a new one). For restoring into new containers, use `runsc` directly or copy the state files and create a new container spec.

### Optimizations

gVisor supports several checkpoint/restore optimizations:

| Flag | Effect | Trade-off |
|---|---|---|
| `--compression=none` | No compression on state files | Faster checkpoint/restore, larger files. Required for `--direct` and `--background`. |
| `--compression=flate-best-speed` | Fast compression | Smaller files, slightly more CPU during checkpoint. Breaks `--direct` and `--background`. |
| `--exclude-committed-zero-pages` | Skip zero-filled memory pages | Dramatically smaller checkpoints for large-memory apps (e.g., LLM runtimes). Increases checkpoint duration since all pages must be scanned. |
| `--direct` (checkpoint/restore) | Bypass host page cache with `O_DIRECT` | Better for one-shot restores on different machines. Requires `--compression=none`. |
| `--background` (restore) | App starts as soon as kernel state loads; memory pages load async | Dramatically reduces "time to first instruction" for large images. Requires `--compression=none`. Accessed pages are prioritized on fault. |

**Recommended defaults for Chetter:**

```bash
runsc checkpoint \
  --image-path=/snapshots/{taskID} \
  --compression=none \
  --exclude-committed-zero-pages \
  --leave-running \
  chetter-task-{taskID}

runsc restore \
  --image-path=/snapshots/{taskID} \
  --background \
  chetter-task-{taskID}
```

This combination gives the fastest restore with background page loading, and skips zero pages to keep checkpoint size manageable.

### CPU Feature Compatibility

When restoring on a different host, gVisor verifies that the target CPU has all features enabled on the source. To enable cross-node restore, pin the CPU feature set using the `dev.gvisor.internal.cpufeatures` annotation:

```bash
runsc cpu-features  # list features on the current machine
```

Then configure the runner to use a stable feature set so checkpoints are portable across the fleet:

```json
{"dev.gvisor.internal.cpufeatures": "sse4_1,sse4_2,avx,avx2,fma,bmi1,bmi2"}
```

### GPU Checkpoint/Restore

gVisor supports checkpointing containers with GPUs via [cuda-checkpoint](https://github.com/NVIDIA/cuda-checkpoint):

```bash
runsc checkpoint \
  --image-path=/snapshots/{taskID} \
  --cuda-checkpoint-path=/usr/local/bin/cuda-checkpoint \
  chetter-task-{taskID}
```

On restore, CUDA processes are automatically resumed — no special flags needed on `runsc restore`. This enables snapshot workflows for GPU workloads. Limitation: not supported on ARM64.

## Docker Commit (Filesystem-Only Snapshots)

### How It Works

A running container stacks a **writable overlay** on top of read-only image layers:

```
Image layers (read-only)          ← alpine:3.19, opencode:latest, runner:main
    ↓
Writable overlay (copy-on-write)  ← agent's changes: /workspace/, node_modules/, .cache/, etc.
    ↓
docker commit → merges overlay into a new image layer
```

When a process writes a file, the copy-on-write mechanism places it in the overlay. A snapshot freezes that overlay:

1. Pause the container (optional, `--pause` flag)
2. Read the overlay diff
3. Create a new image with the diff as an additional layer
4. Return the new image ID

The resulting image is a regular Docker image — you can `docker run` it, `docker push` it to a registry, or use it as the base for future tasks.

### Limitations

`docker commit` captures **filesystem state only**. It does not capture:
- Process memory (in-flight LLM requests, agent session state, loaded models)
- Open network connections (HTTP server, API connections)
- Open file descriptors and their offsets
- Process tree state

A restored container starts with a fresh process — the agent must reinitialize from scratch. For many use cases this is sufficient (pre-warmed dependency caches, workspace preservation), but it cannot resume mid-execution.

### When to Use Docker Commit

Use `docker commit` when:
- gVisor is not available (no `runsc` on the host)
- You only need filesystem state (pre-baked images, dependency caches)
- You want to push a snapshot to a registry as a reusable base image
- You need cross-host portability without CPU feature matching

## Current State in Chetter

Chetter does **not** yet have snapshot support. The runner creates fresh containers per task and discards them on completion. The runner already passes `--runtime runsc` when `USE_GVISOR=true`, so the gVisor checkpoint/restore path is available without any infrastructure changes.

### Proposed Implementation

#### gVisor Checkpoint/Restore (Primary)

1. `chetter_snapshot_create` MCP tool — triggers `docker checkpoint create` (or `runsc checkpoint`) on the agent container
2. `chetter_snapshot_restore` MCP tool — creates a new container from a checkpoint directory via `runsc restore`
3. Snapshot metadata (checkpoint path, task ID, timestamp, label, size) stored in the database
4. Automatic periodic checkpoints with `--leave-running` for crash recovery (configurable interval, default 5 min)
5. Snapshot garbage collection (expiry, max count per task, max total size)
6. Agent session pre-warming pool — maintain N checkpointed agent containers ready to accept prompts, eliminating startup overhead

#### Docker Commit (Fallback)

1. `chetter_snapshot_create` MCP tool with `type: filesystem` — triggers `docker commit` on the agent container
2. `chetter_snapshot_restore` with `type: filesystem` — starts a new container from the committed image
3. Same metadata and GC as above

The tool should detect which mechanism to use based on the runtime: if the container was started with `--runtime=runsc`, use gVisor checkpoint/restore; otherwise fall back to `docker commit`.

## Snapshots in Kubernetes

### Docker Socket Passthrough (Current Path)

The runner uses Docker CLI (`docker run`) to manage agent containers. In Kubernetes, the runner pod mounts the host's Docker socket:

```yaml
volumes:
- name: docker-sock
  hostPath:
    path: /var/run/docker.sock
```

Both `docker commit` and `docker checkpoint create` work because they operate on containers managed by the node's Docker daemon. **This is the simplest path and works today.**

Trade-offs:
- Kubernetes doesn't know about agent containers (no scheduling, no resource tracking, no eviction)
- Agent containers don't appear in `kubectl get pods`
- The runner pod must land on a node running Docker (not containerd-only)
- `docker checkpoint create` requires Docker ≥ 18.03 (fixed `--leave-running` handling)

### gVisor Checkpoint State Storage

gVisor checkpoint files are written to a directory on the host filesystem. In Kubernetes, this requires a hostPath mount or a shared PVC:

```yaml
volumes:
- name: checkpoints
  hostPath:
    path: /var/lib/chetter/checkpoints
    type: DirectoryOrCreate
```

Each checkpoint directory contains multiple files (kernel state, memory pages, metadata). Two checkpoints cannot share the same directory. Size varies by application memory footprint — a typical agent container with ~500MB resident memory produces a ~500MB checkpoint (less with `--exclude-committed-zero-pages`).

For cross-node restore, checkpoint directories must be on shared storage (NFS, EFS, or object storage synced before restore).

### K8s-Native Alternatives

If the runner were rewritten to spawn agent containers as Kubernetes Pods (instead of via Docker CLI), snapshots become harder because you can't `docker commit` or `docker checkpoint` a Pod. The options:

| Approach | How it works | Trade-off |
|---|---|---|
| **gVisor checkpoint via containerd** | Use `runsc checkpoint` on the node's containerd | Captures full state, but requires `runsc` installed on the node and access to the containerd socket |
| **containerd snapshot export** | Use `ctr snapshot` on the node's containerd | Filesystem only, same node-bypass problem as Docker |
| **Volume snapshots** | Snapshot the PVC via CSI (e.g., Velero) | K8s-native, but only captures mounted volumes, not the container's root filesystem |
| **CRI image build** | Use `crane` or `buildkit` to build a new image from the container's rootfs | Works without Docker, but slow (full image build) |
| **Rootfs tar** | `kubectl exec tar` the workspace, store as artifact | Simple and portable, but no incremental support, slow for large workspaces |

### Recommendation

For gVisor deployments (`USE_GVISOR=true`), use `runsc checkpoint` / `docker checkpoint create` as the primary snapshot mechanism. It provides full execution-state capture with no application changes required. The runner already starts containers with `--runtime=runsc`, so no infrastructure changes are needed.

For non-gVisor deployments, fall back to `docker commit` for filesystem-only snapshots.

The K8s-native Pod spawning path would require a significant runner rewrite and loses checkpoint/restore capability unless you add a gVisor + containerd integration layer on top.

## Comparison with Other Agent Infrastructure

### Daytona

Daytona supports workspace snapshots as a first-class feature. A snapshot captures the entire project directory state. Use cases include resuming work, branching before risky changes, and pre-baking environments with dependencies already installed. Under the hood, Daytona uses containerd snapshots or Docker commit — filesystem-level only. gVisor checkpoint/restore gives Chetter an advantage: full process-state capture, not just filesystem.

### Dev Containers / VS Code

VS Code Dev Containers support "pre-builds" — essentially snapshot images that include dependencies and tooling. These are built on push via CI and cached in a registry. The model is similar but focused on interactive development rather than autonomous agent sessions. No process-state checkpointing.

### Gitpod

Gitpod uses "prebuilds" that snapshot workspace state after initialization tasks. Workspaces start from these snapshots instead of building from scratch. The implementation relies on container image layers stored in a registry — filesystem-level only. No process-state checkpointing.

### CRIU (Checkpoint/Restore In Userspace)

CRIU is the Linux-level technology that inspired gVisor's checkpoint/restore. It works with standard runc containers and captures full process state. However, CRIU is notoriously fragile — it requires specific kernel versions, has limited support for network namespaces and complex IPC, and many applications fail to checkpoint cleanly. gVisor's implementation is more reliable because it controls the entire sandbox kernel and can guarantee consistent state.

| Feature | gVisor checkpoint | CRIU | docker commit |
|---|---|---|---|
| Filesystem state | Yes | Yes | Yes |
| Process memory | Yes | Yes (fragile) | No |
| Network connections | Yes | Partial | No |
| Open FDs | Yes | Yes (fragile) | No |
| GPU state | Yes (via cuda-checkpoint) | No | No |
| Reliability | High (controlled sandbox) | Low (kernel-dependent) | High |
| Requires gVisor | Yes | No | No |
