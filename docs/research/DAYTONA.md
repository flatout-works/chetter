# Daytona Integration Proposal

Status: **Proposal - not implemented**

Daytona is a possible future remote execution backend for Chetter. This document is research and should not be read as current implementation docs.

Chetter today uses Docker task containers with optional gVisor (`runsc`) isolation. Legacy Kata/containerd execution has been removed. Any Daytona work should start by extracting a general execution backend interface around the current Docker/gVisor path.

## Why Consider Daytona

[Daytona](https://daytona.io) provides API-driven sandboxes with snapshots, workspace APIs, network controls, and optional managed infrastructure.

Potential benefits:

- Elastic remote sandbox capacity instead of only self-hosted runner machines.
- Fast filesystem snapshot startup for pre-baked workspaces.
- Built-in workspace APIs that could back some runner-local operations.
- Managed network controls per sandbox.
- Optional GPU sandbox availability for future workloads.
- Self-hosted option if Chetter ever wants remote sandbox semantics without managed service dependency.

## Risks And Trade-Offs

| Risk | Detail |
|---|---|
| Vendor/API dependency | Even self-hosted Daytona introduces another control plane and API surface. |
| Cost | Managed Daytona bills per sandbox-hour; Chetter's current runners use owned infrastructure. |
| Lower local control | Chetter's Docker/gVisor path can tune mounts, checkpoints, proxying, and runner behavior directly. |
| Tool overlap | Daytona workspace/process APIs overlap with Chetter runner and harness responsibilities. |
| Latency | File and process operations through a remote API can be slower than local container access. |
| Snapshot semantics | Daytona snapshots are primarily workspace/filesystem snapshots, while gVisor checkpoints can preserve process state. |

## Proposed Architecture

Introduce an `ExecutionBackend` interface in the runner and keep Docker/gVisor as the first implementation.

```go
type ExecutionBackend interface {
    CreateSandbox(ctx context.Context, task *TaskRequest) (SandboxHandle, error)
    StartHarness(ctx context.Context, handle SandboxHandle, task *TaskRequest) error
    StopSandbox(ctx context.Context, handle SandboxHandle) error
    CreateCheckpoint(ctx context.Context, handle SandboxHandle) (CheckpointMetadata, error)
    RestoreCheckpoint(ctx context.Context, checkpoint CheckpointMetadata) (SandboxHandle, error)
}

type SandboxHandle struct {
    ID   string
    Host string
    Port int
}
```

Possible implementations:

| Backend | Runtime name | Status |
|---|---|---|
| Docker/gVisor | `docker` | Current production path. |
| Local | `local` | Development path. |
| Kubernetes | `kubernetes` | Possible future backend. |
| Daytona | `daytona` | Proposal only. |

## Daytona Backend Sketch

```go
type DaytonaBackend struct {
    client *daytona.Client
    config DaytonaConfig
}

type DaytonaConfig struct {
    APIKey   string
    APIURL   string
    Target   string
    Snapshot string
}
```

Expected mapping:

- `CreateSandbox` creates a Daytona sandbox from a configured snapshot.
- Agent image variants map to Daytona snapshot names.
- Secrets are passed as environment variables at sandbox creation.
- Workspace file operations use Daytona APIs or the harness inside the sandbox.
- Cleanup deletes the sandbox after terminal task state.

Example future config:

```yaml
execution:
  runtime: daytona
  daytona:
    api_key: "${DAYTONA_API_KEY}"
    api_url: "${DAYTONA_API_URL}"
    target: "eu"
    snapshot: "chetter-runner-golang"
```

## Migration Phases

### Phase 1: Execution Backend Interface

Extract the backend interface around current Docker/gVisor and local behavior. No behavior change.

### Phase 2: Backend Capability Matrix

Model backend capabilities explicitly:

- Process checkpoint/restore.
- Filesystem snapshot/restore.
- Per-task network policy.
- Per-task Docker/gVisor isolation.
- Remote workspace API.
- GPU support.

### Phase 3: Daytona Prototype

Implement a narrow Daytona backend for non-critical workloads such as docs updates or scans. Measure cost, latency, failure modes, and artifact compatibility.

### Phase 4: Rollout Decision

Keep Docker/gVisor as default unless Daytona proves better for a concrete workload.

## What Does Not Change

- Server task queue, claiming, heartbeats, auth, teams, triggers, and audit log.
- MCP tool contracts.
- GitHub artifact tools.
- Session and task records, except for additional backend metadata if needed.

## Open Questions

1. How should `agent_image` map to Daytona snapshot names?
2. Which secrets should be forwarded, and how should blocked secret validation apply?
3. How should MCP bridge connectivity work when the sandbox does not share the runner filesystem?
4. What is the per-task cost and latency compared with self-hosted Docker/gVisor runners?
5. Which workloads need remote/GPU sandboxes enough to justify the integration?
