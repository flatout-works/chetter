# Chetter — Last Week in Review (Jul 26 – Aug 2, 2026)

Summary of the larger new features implemented during the last 7 days,
compiled from CHANGELOG.md and git history (119 commits).

## Infrastructure & Execution

- **Kubernetes pod execution backend** (#242) — biggest addition of the week:
  `execution.backend: kubernetes` runs each task as an independent K8s Pod
  instead of a Docker container, with runtime-class (gVisor/kata), namespace,
  service account, workspace PVC/hostPath, image pull policy, readiness
  polling, and log/workspace event streaming. Deploy manifests for K3s and
  generic K8s included.
- **Per-task resource limits** (#273, #54) — per-task memory
  (`--memory`/`--memory-swap`), CPU (`--cpus`), and PID (`--pids-limit`)
  limits enforced as real Docker flags on serve, resume, and RPC paths;
  configured runner caps act as hard ceilings; OOM-killed containers now fail
  with a structured `resource_limit` failure category.
- **Runner resource & capacity reporting** (#144) — heartbeats report
  effective container limits and fleet capacity, surfaced in the fleet web UI.

## GitHub Integration

- **Multi-installation support** — the GitHub App now resolves the
  installation *per repository* instead of a single configured
  `GITHUB_INSTALLATION_ID` (legacy fallback kept), with installation-isolated
  token/credential caches and per-task `github_repo`/`github_installation_id`
  metadata persisted via new migrations.
- **PR review webhook** now also triggers on `synchronize` (push to branch)
  and `reopened`, not just `opened`/label.

## Security & Hardening

- **Secret redaction** (#247) — exact known secret values (from env vars
  matching SECRET/TOKEN/KEY/PASSWORD) replaced with `[REDACTED]` in all runner
  output before persistence; held in memory only.
- **Stored XSS fix** — agent-generated markdown sanitized with DOMPurify in
  the web UI.
- **Runner workspace path confinement** — absolute paths, `..` traversal, and
  symlink escapes rejected for extra files/workspace writes.
- **MCP auth hardening** — per-task MCP servers and the execution-scoped
  relay require bearer tokens; credential config files written 0600.
- **Runner mutations fenced to execution claims** — task/execution/runner/claim
  fencing on RPC mutations.
- **Cross-team definition confidentiality fix** (#262) — definitions resolved
  to global + caller's own team only (fail closed).

## Observability & Ops

- **Prometheus `/metrics` endpoint** (#92) — task counts by status, fleet
  health, webhook delivery gauges, bounded cardinality.
- **`/readyz` endpoint** (#93) — schema + DB ping readiness probe with K8s
  probe wiring.
- **MCP relay rejection metric** — cumulative rejected-request counts in
  heartbeats.
- **Stable trigger identity across definition syncs** (#256) — deterministic
  trigger IDs, cron reconciliation, no more stale cron entries firing against
  deleted triggers.

## API / Task Features

- **Task recovery with editable custom prompt** (#272) — `RecoverTask`
  accepts a custom prompt used verbatim while still attaching the previous
  session export.
- **Central server-side task/trigger validation** (#80) — harness whitelist,
  session mode, timeout/TTL limits, canonical repo syntax enforced at a single
  chokepoint across MCP, API, and webhook.
- **API token expiry** (#246) — `expires_at` on tokens, optional
  `expires_in_hours` at creation, enforcement in the auth resolver.
- **Structured failure classification** (#98) — `failure_category`/
  `failure_message` on tasks with colored badges and filters in the web UI.
- **Event callbacks web UI page** (#130) — list/create/edit/delete previously
  MCP-only, now via ConnectRPC + UI.

## Data & Storage

- **Reaper GC for session artifacts** (#127) — TTL-based cleanup of expired
  checkpoints/exports (default 24h).
- **PostgreSQL native full-text search** — `to_tsvector`/
  `websearch_to_tsquery` with GIN indexes across all four FTS paths.
- **PostgreSQL startup migrations** — ordered Goose migrations now applied in
  `ApplySchema` before bootstrap DDL.

## Web UI / Docs

- **Compact Pi session activity view** — new `compact` flag on `ExportTask`;
  bounded tool-result previews on task detail.
- **Website relaunch** — new modern site (`website/current/`), old site moved
  to `website/old/`.
- **Mem9 persistent memory** documented for the OpenCode harness (opt-in,
  runner-wide via `MEM9_API_KEY`).

## Themes

The through-line of the week: **Kubernetes execution + per-task resource
control**, a **major security hardening pass** (XSS, secrets, path
confinement, auth, claim fencing, team scoping), and **deeper observability**
(metrics, readiness, failure classification).
