# Daytona Integration Proposal

Status: **Proposal — not implemented**

## Overview

[Daytona](https://daytona.io) is a managed sandbox platform that provides API-driven ephemeral VMs ("sandboxes") with full OS isolation, dedicated kernels, filesystem, network stacks, and resource allocation. Sandboxes spin up in <90ms from snapshots.

Integrating Daytona as an **optional execution backend** would replace the runner's Kata Containers + containerd infrastructure with Daytona API calls, eliminating the need to manage containerd, iptables, veth pairs, DNS proxies, and network bridges.

## Why Consider Daytona

| Benefit | Detail |
|---------|--------|
| **Eliminates infra management** | Removes ~1500 lines of runner code (`containerd/`, `network/`, bridge management). No more `ctr`, Kata, iptables, veth pairs. |
| **Elastic scaling** | Sandboxes spin up in <90ms. No need to pre-provision runner capacity for peak load. |
| **Snapshots** | Pre-bake a snapshot with Go, opencode, claude — sandboxes start instantly instead of pulling images. |
| **Multi-region** | Daytona supports `us`, `eu`, `android` regions. Current runners are single-region. |
| **Built-in MCP** | Each sandbox exposes file system, git, and process execution via MCP. Overlaps with runner-local tools. |
| **Network isolation** | Per-sandbox firewall with domain allowlist/blocklist. Replaces custom iptables + DNS proxy. |
| **GPU sandboxes** | NVIDIA H100 and RTX Pro 6000 available on demand. Not possible with current Kata setup. |
| **Go SDK** | Daytona has a Go SDK — natural fit for the Chetter runner. |
| **Self-hosted option** | Daytona is open source and can be self-hosted. Not locked into managed service. |

## Risks & Trade-offs

| Risk | Detail |
|------|--------|
| **Vendor dependency** | Even self-hosted, you depend on Daytona's API stability and roadmap. Kata stack is fully under your control. |
| **Cost** | Managed Daytona bills per sandbox-hour. Current Kata runners run on your own infra. Cost comparison depends on scale. |
| **Loss of fine-grained control** | Current network stack does specific things (IPv6 suppression for Kata VM stalls, custom iptables chains, metadata endpoint blocking). Daytona's network limits are coarser. |
| **Tool overlap** | Daytona's built-in MCP (file system, git, process) overlaps with `runner/internal/tools/`. Your deploy tools (build/push/run/rollback) go beyond what Daytona offers. |
| **Latency** | File operations go through Daytona's HTTP API instead of local Unix sockets. Local workspace tools are faster. |
| **Self-hosted weight** | Running Daytona open-source means managing its own infrastructure (Kubernetes, etc.). You trade "manage Kata" for "manage Daytona". |

## Proposed Architecture

### ExecutionBackend Interface

Introduce a `ExecutionBackend` interface in the runner, selected by `execution.runtime` in `runner.yaml`:

```go
type ExecutionBackend interface {
    CreateSandbox(ctx context.Context, task *TaskRequest) (SandboxHandle, error)
    RunCommand(ctx context.Context, handle SandboxHandle, cmd []string) (ExecResult, error)
    StopSandbox(ctx context.Context, handle SandboxHandle) error
    WriteFile(ctx context.Context, handle SandboxHandle, path string, content []byte) error
    ReadFile(ctx context.Context, handle SandboxHandle, path string) ([]byte, error)
}

type SandboxHandle struct {
    ID     string
    Host   string // API endpoint for the sandbox
    Port   int
}
```

Implementations:

| Backend | `execution.runtime` | Description |
|---------|---------------------|-------------|
| Kata | `kata` (default) | Current containerd + Kata Containers. Full control, requires privileged runner. |
| Docker | `docker` | Current Docker mode. Simpler, less isolation. |
| Local | `local` | Agent runs directly on host. No isolation. |
| Daytona | `daytona` | New. Creates Daytona sandboxes via API. No local containerd needed. |

### Daytona Backend Implementation

```go
type DaytonaBackend struct {
    client *daytona.Client
    config DaytonaConfig
}

type DaytonaConfig struct {
    APIKey   string
    APIURL   string
    Target   string // "us", "eu", etc.
    Snapshot string // "chetter-runner-golang", "chetter-runner-python", etc.
}
```

- `CreateSandbox` → `client.Create(ctx, types.SnapshotParams{Snapshot: snapshotForTask(req)})`
- `RunCommand` → `sandbox.Process.ExecuteCommand(ctx, cmd)`
- File operations via Daytona's file system API
- Network limits configured per-sandbox (domain allowlist/blocklist)
- Cleanup on task completion: `sandbox.Delete(ctx)`

### Runner Config Addition

```yaml
execution:
  runtime: daytona  # "kata" (default), "docker", "local", "daytona"
  daytona:
    api_key: "${DAYTONA_API_KEY}"
    api_url: "${DAYTONA_API_URL}"
    target: "eu"
    snapshot: "chetter-runner-golang"
```

### Snapshot Management

- Create Daytona snapshots from the same Dockerfile variants in `runner/images/`.
- CI pipeline: build image → push to Daytona as snapshot.
- Map `agent_image` field in task requests to Daytona snapshot names.

## Migration Phases

### Phase 1: Interface Abstraction (1 week)

Extract `ExecutionBackend` from the existing `controller.go`. Implement for Kata, Docker, and Local. No behavior change — just refactoring.

### Phase 2: Daytona Backend (1-2 weeks)

Implement `DaytonaBackend` using the Go SDK. Add `execution.runtime: daytona` config support. Test with simple tasks (doc reviews, scheduled scans).

### Phase 3: Snapshot Management (1 week)

Create Daytona snapshots for each image variant. Add CI step to rebuild snapshots when base images change. Map `agent_image` to snapshot names.

### Phase 4: Gradual Rollout (ongoing)

- Start with non-critical workloads on Daytona.
- Keep Kata as default for production tasks.
- Monitor cost, latency, and reliability.
- Consider Daytona for GPU workloads (not possible with Kata today).

## What Does NOT Change

- Server/control plane — no changes.
- MCP tools exposed to the control plane — no changes.
- Task claiming, heartbeats, reaper — no changes.
- Auth & teams — no changes.
- GitHub webhook / PR review — no changes.
- Runner-local MCP tools (workspace, git, deploy) — may use Daytona's built-in APIs instead of local filesystem, but the tool interface stays the same.

## Open Questions

1. **How to handle `agent_image` selection?** Currently the runner pulls an image by reference. Daytona uses snapshot names. Need a mapping strategy (e.g., `agent_image: ghcr.io/flatout-works/chetter-runner:golang` → snapshot `chetter-runner-golang`).

2. **Secrets forwarding?** The runner currently injects API keys (ANTHROPIC_API_KEY, GITHUB_TOKEN, etc.) as environment variables into the container. Daytona sandboxes support env vars at creation time. Need to ensure all secrets are passed through.

3. **MCP socket?** The runner creates a Unix socket for the `runner-bridge` MCP server. Daytona sandboxes don't share the host filesystem. Would need to expose MCP tools via Daytona's built-in MCP server or route through the sandbox's network.

4. **Cost model?** Need to benchmark per-task cost on Daytona vs. self-hosted Kata on Hetzner before committing to production use.
