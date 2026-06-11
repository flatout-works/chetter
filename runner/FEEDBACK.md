# Runner Feedback

This file collects improvement ideas for turning the current runner prototype into a secure Kata-based agent execution service for OpenCode, Niffler, and similar agents.

## Highest Priority

- Implement the real Kata/containerd execution path. `controller.runTask` currently returns an error unless `RUNNER_LOCAL=true`; this is the main blocker for the secure runner idea.
- Make network policy enforceable, not advisory. Agents should run in a network namespace where direct egress is impossible and all HTTP, HTTPS, DNS, and package-manager traffic is forced through runner-controlled policy.
- Wire `report_result` into the task lifecycle. The MCP tool currently returns an acknowledgement but does not publish the reported status, summary, or artifacts to NATS.
- Add a durable task state model. `tasks` is an in-memory map without locking, persistence, cancellation handling, leases, retries, or recovery after runner restart.
- Define and version the NATS protocol. Task requests, status events, logs, artifacts, heartbeats, and final results should have stable schema versions.

## Kata Execution

- Add a runtime abstraction so local, Docker, and Kata/containerd execution can share the same task setup and result handling.
- Map `max_memory_mb`, `max_cpu`, timeout, process limits, and filesystem limits into actual containerd/Kata settings.
- Run each task with a fresh root filesystem, read-only agent image, writable workspace mount, and no host socket access except the task MCP socket.
- Drop Linux capabilities, use seccomp/AppArmor where applicable, and run as a non-root user inside the agent container.
- Add deterministic cleanup for stopped, failed, and orphaned containers.
- Capture stdout/stderr as streamed NATS log events instead of only collecting combined output at process exit.

## Agent Contract

- Standardize the agent entrypoint contract for OpenCode and Niffler images: required environment variables, expected command, MCP socket location, workspace path, and result reporting behavior.
- Add a small agent harness image that adapts generic CLI agents to the runner contract.
- Support task instructions directly in `TaskRequest`; today the request has hints like `skills` but no first-class prompt or task body.
- Make result reporting explicit: agents should call `report_result`, and process exit should be a fallback/error signal rather than the primary success mechanism.
- Add cancellation support so an external NATS command can stop an active task gracefully before killing the container.

## NATS Design

- Split subjects by event type, for example `devfleet.runner.tasks`, `devfleet.runner.tasks.<id>.cancel`, `devfleet.tasks.<id>.status`, `devfleet.tasks.<id>.logs`, and `devfleet.tasks.<id>.artifacts`.
- Consider JetStream for task durability, at-least-once delivery, result replay, and runner restarts.
- Add queue groups so multiple runner instances can share the same task subject safely.
- Use NATS auth and subject permissions so agents cannot publish arbitrary control messages.
- Add correlation IDs, schema versions, runner IDs, attempt numbers, and timestamps to every event.

## MCP Server

- Replace the minimal MCP implementation with a stricter protocol implementation or add conformance tests around initialize, tool listing, tool calls, errors, and notifications.
- Return structured MCP content for non-string results instead of formatting everything with `fmt.Sprintf`.
- Add per-tool authorization. For example, some tasks may need read-only workspace access, no git push, or no NATS publish.
- Add input validation for every tool argument and reject unknown or malformed values clearly.
- Connect `report_result` to the controller through a callback or channel, and include summaries/artifacts in the published final status.

## Workspace Safety

- Harden path resolution against symlink escapes from the workspace root.
- Enforce `workspace.max_size_mb` with project quotas, overlayfs quotas, or runtime storage limits.
- Add configurable retention for failed workspaces so debugging artifacts can be preserved safely.
- Create an artifact collection step that copies declared artifacts to durable storage before workspace cleanup.
- Record workspace checksums or git diffs in the final result for auditability.

## Git And Secrets

- Avoid passing PATs through long-lived environment variables. Prefer short-lived tokens, mounted secret files, or a credential helper scoped to the task.
- Replace `StrictHostKeyChecking=no` with controlled known-hosts management.
- Add clone depth and sparse checkout options for large repositories.
- Make git push opt-in per task or per policy, not a globally available MCP tool.
- Redact credentials and sensitive environment variables from logs and NATS messages.

## Network Policy

- Start and enforce the DNS proxy or remove the unused DNS config until it exists.
- Block cloud metadata IPs at the network layer, not only by DNS name.
- Add package ecosystem policy for `go`, `npm`, `pip`, `cargo`, and OS package repositories.
- Log allowed and blocked egress decisions as structured audit events.
- Consider separate allowlists for LLM APIs, source control, package registries, docs/search, and arbitrary web fetches.

## Observability

- Add structured logging with task ID, runner ID, agent image, git ref, and event type fields.
- Publish task heartbeats while an agent is running.
- Export metrics for active tasks, queue latency, execution duration, errors, timeouts, proxy blocks, and workspace cleanup failures.
- Add trace IDs that flow from the incoming NATS task through MCP calls, git operations, network fetches, and final status events.

## Reliability

- Protect `Runner.tasks` with a mutex or replace it with a concurrency-safe task manager.
- Handle clone failures as task errors instead of logging and continuing with an empty or partial workspace.
- Ensure `StartedAt` and `EndedAt` reflect actual task lifecycle times rather than status publish time.
- Add graceful shutdown behavior that stops accepting new tasks and cancels or drains active tasks.
- Add retry policy for transient NATS publish failures and final status publishing.

## Tests

- Add unit tests for config defaults, workspace path handling, proxy allow/block matching, and NATS subject construction.
- Add MCP protocol tests over a Unix socket.
- Add controller tests with a fake executor and fake NATS client.
- Add an integration test using a local NATS server and local mode.
- Add a security regression test suite for path traversal, symlink escape, blocked egress, and secret redaction.

## Documentation

- Document the exact OpenCode and Niffler agent image contract once the entrypoint is finalized.
- Add a sequence diagram for task intake, workspace creation, MCP startup, agent execution, result reporting, and cleanup.
- Add example NATS messages for task submission, log streaming, artifact reporting, cancellation, and final status.
- Keep a clear distinction between development local mode and production Kata mode so users do not mistake the prototype for a sandbox.
