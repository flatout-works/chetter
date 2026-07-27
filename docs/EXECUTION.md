# Execution Backends

Chetter supports three explicit runner execution backends:

| Backend | Use | Isolation and lifecycle |
|---|---|---|
| `docker` | Production single-host/Compose deployments | Agent container per execution; optional Docker `runsc`; existing Docker workspace and checkpoint behavior. |
| `kubernetes` | Production Kubernetes deployments | Agent Pod per execution; optional `runtimeClassName`; shared PVC or single-node hostPath; no Docker socket. |
| `local` | Development only | Harness process runs directly on the runner host with no container boundary. |

Select one with `EXECUTION_BACKEND=docker|kubernetes|local`. It is the sole environment selector; when unset, the runner defaults to `docker`, and unknown values fail startup.

## Kubernetes Configuration

| Environment | YAML | Meaning |
|---|---|---|
| `KUBERNETES_NAMESPACE` | `kubernetes.namespace` | Namespace for agent Pods and Secrets. |
| `KUBERNETES_RUNTIME_CLASS` | `kubernetes.runtime_class` | Optional runtime class, commonly `gvisor`. |
| `KUBERNETES_AGENT_IMAGE_PULL_POLICY` | `kubernetes.image_pull_policy` | `Always`, `IfNotPresent`, or `Never`. |
| `KUBERNETES_AGENT_SERVICE_ACCOUNT` | `kubernetes.agent_service_account` | Optional low-privilege agent ServiceAccount, distinct from the runner. |
| `KUBERNETES_WORKSPACE_PVC` | `kubernetes.workspace_pvc` | Shared PVC mounted at the runner workspace root. |
| `KUBERNETES_WORKSPACE_HOST_PATH` | `kubernetes.workspace_host_path` | Single-node testing alternative to PVC. |
| `NODE_NAME` | `kubernetes.node_name` | Required in hostPath mode; child Pods are pinned to the runner node. |
| `KUBERNETES_POD_READY_TIMEOUT_SEC` | `kubernetes.pod_ready_timeout_sec` | Scheduling/container readiness timeout. |
| `KUBERNETES_CLEANUP_AFTER_TASK` | `kubernetes.cleanup_after_task` | Must remain `true`; Pods and environment Secrets are never preserved. |
| `KUBECONFIG` | `kubernetes.kubeconfig` | Out-of-cluster development configuration. In-cluster config is preferred. |

Exactly one workspace PVC or hostPath is required in Kubernetes mode. Multi-node production requires an RWX PVC accessible from every eligible agent node; the hostPath option is single-node only. The runner mounts the shared storage at `runner.workspace_root`, while each child Pod mounts only its validated execution workspace through a relative `subPath` at the exact workspace path. Sibling execution workspaces are not present in the agent container mount table.

Each child Pod uses the task's resolved `agent_image`, `restartPolicy: Never`, disabled ServiceAccount token automount, optional agent ServiceAccount and runtime class, and task CPU/memory limits. Managed runner, provider, Git, harness, and execution hierarchy environment values are placed in a per-execution Secret. Task environment cannot override managed keys. Kubernetes runner deployments set `RUNNER_ID` from the runner Pod UID and owner-reference child Pods and Secrets to that runner Pod for garbage collection.

ServeHarness tasks retain the current watch, token delta, watchdog, finalization, export, cancellation, and workspace-preservation lifecycle. Pi uses Kubernetes attach for its JSONL RPC stream. Resumes create a fresh Pod over the preserved workspace and send the follow-up to `ResumeHarnessSessionID`.

gVisor provides syscall isolation, not egress policy. A `gvisor` RuntimeClass must be installed separately, and its scheduling configuration or cluster policy must select compatible nodes for child agent Pods. Proxy environment variables affect cooperative HTTP clients but are not enforced egress isolation. Cross-task runner MCP authentication and network isolation remain limitations of this recovery; use cluster network controls where the threat model requires them. Kubernetes mode reports checkpoint/restore as unsupported even when gVisor is configured.
