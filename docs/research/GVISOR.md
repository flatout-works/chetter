# gVisor Features (Beyond Checkpoint/Restore)

Status: **Research reference**

Research based on [gvisor.dev/docs](https://gvisor.dev/docs/) as of 2026-06-19.

---

## 1. Runtime Monitoring (Threat Detection / Intrusion Detection)

- All syscalls and container lifecycle events emit trace points.
- Streams real-time event data to an external process (e.g. Falco).
- Isolated monitoring process, shared across sandboxes.
- Includes a `tracereplay` tool for offline testing.

**Chetter angle:** Monitor agent behavior inside sandboxes -- detect hangs,
unauthorized filesystem writes, unexpected network access, or anomalous syscall
patterns.

## 2. Prometheus Observability (Built-in Metrics)

- Per-sandbox metrics: file opens, reads, write waits, network I/O.
- Process-wide metrics: sandbox count, running count, broken metrics.
- Ad-hoc export via `runsc export-metrics` (no server required).
- `sandbox_metadata` exposes platform, network type, version per sandbox.

**Chetter angle:** Track runner resource consumption, detect performance
regressions, correlate FS/network activity with task progress.

## 3. Filesystem Overlay System (`--overlay2`)

Writable tmpfs on top of any mount (root or all). Three backing mediums:

| Medium | Behavior |
|--------|----------|
| `memory` | Fast, bloats RAM |
| `self` | File hidden in rootfs, Kubernetes ephemeral-storage-aware |
| `dir=/path` | Host directory, no memory pressure |

Enables read-only rootfs + writable workspace without copying.

**Chetter angle:** Use `self` or `dir` backing for workspace overlays -- fast
teardown, no persistent disk leakage, no memory bloat.

## 4. EROFS (Enhanced Read-Only File System)

- Memory-mapped read-only filesystem for the rootfs lower layer.
- Zero gofer communication -- the Sentry reads directly from mmap'd EROFS image.
- Enables **gofer-less mode** if no other gofer mounts exist.

**Chetter angle:** Fastest possible container cold-start; skip gofer entirely
for agent images; pair with overlay for writable workspace.

## 5. Exclusive Bind Mounts (`--file-access-mounts=exclusive`)

- Disables continuous host revalidation of bind-mounted directories.
- Assumes sandbox has exclusive ownership of the mount.
- Massive dentry caching improvement -- fewer FS round-trips.

**Chetter angle:** Workspaces are already exclusive to one task -- enable this
for significant I/O performance on agent workloads that do heavy file ops
(git, builds, tests).

## 6. Directfs (Sandbox-Direct Filesystem Access)

- Enabled by default -- sandbox gets donated file descriptors from the gofer.
- Uses `openat(2)` style calls directly instead of gofer RPCs.
- Gofer still owns the filesystem; sandbox can only access donated trees.
- Can be disabled (`--directfs=false`) for stricter seccomp.

**Chetter angle:** Keeps good I/O performance while maintaining isolation.

## 7. Network Isolation (`--network=none`)

- Complete network isolation -- only loopback inside netstack.
- No host network access whatsoever.

**Chetter angle:** Extra security for agents that don't need external network
(use runner's MCP bridge for controlled outbound).

## 8. Egress Traffic Shaping (Token Bucket Filter)

- Rate-limit outbound sandbox traffic with `--qdisc=tbf`.
- Configurable rate (bytes/sec) and burst (bytes).
- Per-sandbox overrides via OCI annotations or containerd config.

**Chetter angle:** Prevent runaway agents from saturating outbound bandwidth.

## 9. Custom Gofer Extensions (Pluggable Filesystem Backends)

- Implement the `extension.Extension` interface in a custom `runsc` build.
- Handle specific mount paths with custom backends (network store, encrypted
  FS, tiered cache).
- Unclaimed mounts fall through to stock fsgofer.

**Chetter angle:** Could implement a workspace snapshot backend (e.g.
overlayfs-diff to blob store, or a copy-on-write workspace layer).

## 10. Multiple Platforms (Performance Tuning)

| Platform | Best For | Notes |
|----------|----------|-------|
| systrap (default) | VMs, cloud, anywhere | `SIGSYS`-based, good all-rounder |
| KVM | Bare metal | Best performance, uses hardware virt |
| ptrace | Legacy | Deprecated, will be removed |

**Chetter angle:** Use `systrap` for VM-hosted runners, `KVM` for bare-metal
for max performance.

## 11. GPU Passthrough (`nvproxy`)

- Supports CUDA, Vulkan, NVENC/NVDEC via ioctl forwarding.
- NVIDIA T4/A100/A10G/L4/H100 and consumer cards (RTX 3090/4090).
- Minimal overhead (ioctls are passed through directly).
- CDI and NVIDIA container runtime compatible.

**Chetter angle:** If Chetter ever runs ML agents or GPU-accelerated workloads.

## 12. Rootless Mode

- Run containers entirely without root privileges.
- Three approaches: `--rootless` flag, caller-configured user namespaces
  (Docker/Podman), native rootless OCI spec.

**Chetter angle:** Potential for running runners without Docker daemon root
privileges.

---

## Top Recommendations for Chetter

| Priority | Feature | Benefit |
|----------|---------|---------|
| 1 | Exclusive bind mounts | Free I/O perf win for workspace dirs |
| 2 | Overlay2 with `dir` backing | Clean workspace lifecycle, no memory bloat |
| 3 | Prometheus observability | Production visibility into runner fleet |
| 4 | Runtime monitoring | Debug agent hangs, detect anomalies |
| 5 | EROFS rootfs | Fastest cold-start for agent images |
| 6 | Custom gofer extensions | Workspace snapshot backend (future) |
