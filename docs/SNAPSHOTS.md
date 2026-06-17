# Snapshots

## What Is a Snapshot?

A snapshot captures the **writable overlay filesystem** of a running agent container. This includes everything the agent has changed since the container started: modified source files, installed dependencies, build artifacts, config changes, and any other filesystem state.

Snapshots let you preserve and restore the exact state of a workspace, enabling workflows that plain Git commits can't cover.

## Why Snapshots?

Chetter's default state mechanism is Git — each task clones the repo fresh, the agent makes changes and commits/PRs them, and the workspace is ephemeral. This is simple and reliable, but it has gaps:

| Limitation | What snapshots solve |
|---|---|
| No way to pause mid-task and resume later | Snapshot the container, restore it in a future session |
| No pre-warmed dependency caches | Snapshot after `npm install` / `go mod download`, start next session warm |
| No branching within a single task | Snapshot before a risky change, restore if the agent goes down a bad path |
| No recovery from agent crashes | Snapshot periodically; if the agent crashes, restore the last good state |

## How Snapshots Work (Filesystem Level)

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

## Current State in Chetter

Chetter does **not** yet have snapshot support. The runner creates fresh containers per task and discards them on completion. Adding snapshots would require:

1. A `chetter_snapshot_create` MCP tool that triggers `docker commit` on the agent container
2. A `chetter_snapshot_restore` MCP tool that starts a new container from a snapshot image
3. Snapshot metadata storage (image ID, task ID, timestamp, label) in the database
4. Optional automatic periodic snapshots (e.g., every 5 minutes) for crash recovery
5. Snapshot garbage collection (expiry, max count per task)

## Snapshots in Kubernetes

### Docker Socket Passthrough (Current Path)

The runner uses Docker CLI (`docker run`, `docker commit`) to manage agent containers. In Kubernetes, the runner pod mounts the host's Docker socket:

```yaml
volumes:
- name: docker-sock
  hostPath:
    path: /var/run/docker.sock
```

Snapshots work because `docker commit` operates on any container the node's Docker daemon manages. **This is the simplest path and works today.**

Trade-offs:
- Kubernetes doesn't know about agent containers (no scheduling, no resource tracking, no eviction)
- Agent containers don't appear in `kubectl get pods`
- The runner pod must land on a node running Docker (not containerd-only)

### K8s-Native Alternatives

If the runner were rewritten to spawn agent containers as Kubernetes Pods (instead of via Docker CLI), snapshots become harder because you can't `docker commit` a Pod. The options:

| Approach | How it works | Trade-off |
|---|---|---|
| **containerd snapshot export** | Use `ctr snapshot` on the node's containerd | Same node-bypass problem as Docker, lower-level API |
| **Volume snapshots** | Snapshot the PVC via CSI (e.g., Velero) | K8s-native, but only captures mounted volumes, not the container's root filesystem |
| **CRI image build** | Use `crane` or `buildkit` to build a new image from the container's rootfs | Works without Docker, but slow (full image build) |
| **Rootfs tar** | `kubectl exec tar` the workspace, store as artifact | Simple and portable, but no incremental support, slow for large workspaces |
| **Docker socket passthrough** | Runner uses `docker commit` on the node's Docker daemon | Works today, but bypasses K8s scheduling |

### Recommendation

For now, stick with Docker socket passthrough. It works in K8s (as long as the node runs Docker), supports snapshots natively via `docker commit`, and is what the runner already uses. The K8s-native Pod spawning path would require a significant runner rewrite and loses snapshot capability unless you add a CSI/Volume snapshot layer on top.

The runner deployment manifests require a node with Docker available. On managed K8s (EKS, GKE), use a node pool with Docker configured as the container runtime, or use a DaemonSet to ensure Docker is available on runner nodes.

## Comparison with Other Agent Infrastructure

### Daytona

Daytona supports workspace snapshots as a first-class feature. A snapshot captures the entire project directory state. Use cases include resuming work, branching before risky changes, and pre-baking environments with dependencies already installed. Under the hood, Daytona uses containerd snapshots or Docker commit.

### Dev Containers / VS Code

VS Code Dev Containers support "pre-builds" — essentially snapshot images that include dependencies and tooling. These are built on push via CI and cached in a registry. The model is similar but focused on interactive development rather than autonomous agent sessions.

### Gitpod

Gitpod uses "prebuilds" that snapshot workspace state after initialization tasks. Workspaces start from these snapshots instead of building from scratch. The implementation relies on container image layers stored in a registry.
