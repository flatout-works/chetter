# Chetter Manual

Status: **Current operator guide**

This manual covers setup, configuration, and common operations. For a feature
inventory, see [FEATURES.md](FEATURES.md). For roadmap work, see [PLAN.md](PLAN.md).

## Overview

Chetter is a self-hosted MCP server and web control plane for running autonomous development agents on a fleet of runners.

```text
AI client / web UI
      |
      | MCP / HTTP
      v
Chetter server + TiDB/MySQL/PostgreSQL
      |
      | ConnectRPC claim, heartbeat, events
      v
Runner fleet -> Docker/gVisor or Kubernetes task containers -> agent harness
```

Main binaries:

- `chetter`: server, MCP endpoint, web/API endpoint, triggers, auth, task queue.
- `chetterctl`: token management CLI.
- `runner`: runner harness service in `runner/`.

## Quick Start With Compose

1. Clone and configure:

```bash
git clone https://github.com/flatout-works/chetter.git
cd chetter
cp .env.example .env
```

2. Edit `.env` and set at minimum:

| Variable | Purpose |
|---|---|
| `CHETTER_MCP_AUTH_TOKEN` | External admin bearer token used by Compose and Kubernetes examples. Compose maps it to the server's `MCP_AUTH_TOKEN`. |
| `CHETTER_RUNNER_RPC_TOKEN` | Optional dedicated runner RPC token. If empty in Compose, it defaults to `CHETTER_MCP_AUTH_TOKEN`. |
| Provider key | At least one usable LLM/provider key, depending on selected harness and model. |

3. Build images if needed:

```bash
./deploy/build.sh
```

4. Start with bundled local TiDB:

```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

5. Or start with an external TiDB, MySQL, or PostgreSQL database by setting `DATABASE_DSN` and omitting the local override:

```bash
docker compose --env-file .env -f deploy/compose.yaml up -d
```

6. Verify:

```bash
curl http://localhost:18088/healthz
```

Open the web UI at `http://localhost:18090` and log in with `CHETTER_MCP_AUTH_TOKEN`.

## Ports

| Host port | Container port | Purpose |
|---|---|---|
| `18088` | `8080` | MCP endpoint and health endpoint. |
| `18090` | `8090` | Web UI and ConnectRPC API. |

The underlying server env vars are `HTTP_ADDR` and `WEB_ADDR`.

## Database Support

Chetter supports [TiDB](https://www.pingcap.com/tidb/), MySQL-compatible databases such as AWS Aurora MySQL, and PostgreSQL 16 or newer. TiDB remains the preferred default because it speaks the MySQL wire protocol while adding capabilities Chetter's roadmap can use, including vector search for semantic task/event retrieval, HTAP via TiFlash for fleet analytics, and TiDB Cloud for zero-ops managed deployments.

Set `CHETTER_DB_DIALECT=mysql` for MySQL/Aurora, `CHETTER_DB_DIALECT=tidb` for TiDB, or `CHETTER_DB_DIALECT=postgres` for PostgreSQL. A `postgres://` or `postgresql://` `DATABASE_DSN` is auto-detected when the dialect is unset.

Use a PostgreSQL URL such as `postgres://chetter:password@db.example:5432/chetter?sslmode=require`. PostgreSQL uses the native `pgx` driver. Chetter creates a missing database when the connecting role has `CREATE DATABASE`; otherwise, pre-create it before starting Chetter.

### Connection Resilience

The server retries transient database errors — connection refused, broken pipe, TiDB leader
change — at startup and during transactions using exponential backoff (100ms → 500ms → 1s → 2s → 5s,
up to 3 retries for transactions, up to 60s at startup). Non-transient errors such as bad
credentials, missing databases, and constraint violations fail immediately. This lets the server
ride through brief TiDB leader transfers or PostgreSQL restarts without manual operator
intervention.

> **Local vs. real TiDB.** The bundled database in `deploy/compose.local.yaml` runs TiDB's single-container `unistore` *test* engine — convenient for local dev (it serves Chetter's plain MySQL-protocol workload), but it has no TiFlash, so vector search and HTAP do not run on it. Connect to a real TiDB via `DATABASE_DSN` for those features and for production.

## Authentication

There are three token contexts to keep distinct:

| Token | Where used | Notes |
|---|---|---|
| `MCP_AUTH_TOKEN` | Server binary admin token. | Required by the server process. Compose/K8s examples set this from external `CHETTER_MCP_AUTH_TOKEN`. |
| `CHETTER_MCP_AUTH_TOKEN` | Deployment-facing admin token and agent MCP token. | Use this in `.env`, Kubernetes secrets, and clients unless running the binary directly. |
| `CHETTER_RUNNER_RPC_TOKEN` | Runner-to-server ConnectRPC token. | Required by the server. Compose falls back to `CHETTER_MCP_AUTH_TOKEN` if this is empty. |

Team tokens are stored hashed in the configured database. A user and token can belong to one or more teams, which matches Okta-style group membership: Okta groups map to Chetter teams. Team-scoped tokens see the union of their teams' tasks, triggers, schedule runs, sessions, event callbacks, and artifacts.

Created resources still have one owning `team_id`. If a non-admin token belongs to multiple teams, create/update/delete operations that need an owner require `team_id` or `team_name`; single-team tokens default to their only team. Admin tokens can create global resources by omitting team ownership.

Create a scoped token with `chetterctl`:

```bash
chetterctl token create --team engineering --user alice --name alice-cli
```

Team tokens can optionally expire. The `chetter_create_token` MCP tool accepts an optional `expires_in_hours` field; omission or zero means no expiry. Expired tokens are rejected at authentication time. `chetter_list_tokens` exposes the `expires_at` timestamp (null when no expiry is set).

### OIDC SSO for the Web UI

When the OIDC environment variables below are set, the web UI supports login through an OIDC provider (Okta and any other OIDC-compliant IdP). The flow:

- The unauthenticated UI redirects to `/auth/login`, which sends the browser to the IdP's authorization endpoint.
- `/auth/callback` exchanges the code, verifies the ID token (signature, issuer, audience, nonce), maps the user's groups to a Chetter scope, and issues a short-lived signed session cookie (HttpOnly, `SameSite=Strict`). On HTTPS deployments the cookie uses the browser-enforced `__Host-chetter-session` name (Secure, Path=/, no Domain); plain HTTP deployments keep the legacy `chetter_session` name. The raw ID token is never stored in the session cookie.
- The ConnectRPC web API and `/api/v1/repos` accept the session cookie in addition to bearer tokens. MCP/runner endpoints remain bearer-token-only.
- Logout clears the cookie and redirects to the IdP's end-session endpoint when the provider advertises one, sending the browser back to the app origin derived from `OIDC_REDIRECT_URL` (not request headers).

Group-to-scope mapping (configurable): the `OIDC_ADMIN_GROUP` (default `chetter-admin`) grants full admin scope; any group matching `OIDC_TEAM_GROUP_PREFIX` (default `chetter-`) maps to a team named after the suffix, resolved to the matching team in the database when one exists. A team's `okta_group_id` or `okta_group_name` column, when populated, overrides that convention: a group matching either value binds directly to that team, even when the team name does not follow the prefix. Sessions are stateless JWTs signed with `OIDC_SESSION_SECRET` (or a key derived from `MCP_AUTH_TOKEN`); no session table is used.

Managed Git identities control commit attribution for agent work and are configured via `chetterctl identity` or the **Admin > Git Identities** UI — see [CONFIGURATION.md](CONFIGURATION.md#managed-git-identities).

## Environment Variables

### Server

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `HTTP_ADDR` | No | `:8080` | MCP listen address. |
| `WEB_ADDR` | No | `:8090` | Web UI and ConnectRPC API listen address. |
| `MCP_AUTH_TOKEN` | Yes | empty | Server admin bearer token. Empty and `change-me*` values are rejected. |
| `CHETTER_RUNNER_RPC_TOKEN` | Yes | empty | Dedicated runner ConnectRPC token. Empty and `change-me*` values are rejected. |
| `DATABASE_DSN` | Yes for binary | empty | TiDB/MySQL DSN or PostgreSQL URL. Compose local override can provide bundled TiDB. |
| `CHETTER_DB_DIALECT` | No | auto-detect | Optional database dialect override: `tidb`, `mysql`, or `postgres`. |
| `DEFAULT_AGENT_IMAGE` | No | `ghcr.io/flatout-works/chetter-agent-base:latest` | Default agent dev container image used when task or trigger config omits `agent_image`. |
| `AGENT_IMAGE_PREFIX` | No | empty | Registry/namespace prefix prepended to unqualified `agent_image` values. For chetter-config images, set `ghcr.io/flatout-works` so `chetter-agent:golang` resolves to `ghcr.io/flatout-works/chetter-agent:golang`. Fully qualified image refs are left unchanged. |
| `DEFAULT_TASK_TIMEOUT_SEC` | No | `600` | Default task timeout. |
| `CHETTER_MAX_TASK_TIMEOUT_SEC` | No | unset | Operator-configurable ceiling for `timeout_sec` on tasks and triggers. |
| `CHETTER_MAX_SESSION_TTL_HOURS` | No | unset | Operator-configurable ceiling for resumable session TTL. |
| `CHETTER_MAX_PENDING_TASKS` | No | `0` | Global admission cap on tasks waiting to be claimed (`pending`). `0` disables the limit. When reached, all ingress paths reject new work with a retryable capacity error and record a `task_admission_rejected` audit event; completed/cancelled/claimed tasks release capacity. See [DEPLOYMENT.md](DEPLOYMENT.md) and issue #50. |
| `CHETTER_CALLBACK_MAX_DEPTH` | No | `5` | Maximum depth of the provenance chain for tasks spawned by `create_task` event callbacks. Each callback-spawned task records its parent (`callback_parent_task_id`) and depth (`callback_depth`); a spawn that would exceed the limit is rejected with an `event_callback_recursion_limit` error in `task_events` and the audit log, so a misconfigured `task.completed` → `create_task` loop cannot grow the queue unboundedly. The callback itself stays enabled — only the specific recursive chain is stopped. `0` disables the guard. See issue #312. |
| `EVENTS_RETENTION_DAYS` | No | `0` | Retention for `task_events`. A positive value enables reaper pruning; `0` disables it. |
| `AUDIT_RETENTION_DAYS` | No | `0` | Retention for `audit_log`. A positive value enables reaper pruning; `0` disables it. |
| `ARTIFACT_RETENTION_DAYS` | No | `0` | Retention for `task_artifacts` and `agent_sessions`. A positive value enables reaper pruning; `0` disables it. |
| `DEFINITIONS_REPO` | No | empty | Git repo for synced model catalog and definitions. |
| `DEFINITIONS_BRANCH` | No | `main` | Definitions repo branch. |
| `CHETTER_ALLOW_UNISOLATED` | No | `false` | Documented escape hatch for single-tenant/trusted deployments that intentionally run without gVisor. When unset (hardened default), every task requires enforced isolation (gVisor/runsc) and is refused by runners that cannot enforce it. When `true`, only resumable sessions and tasks explicitly configured with `isolation: required` require isolation. Set it on the server **and** on every runner in the trusted deployment. See issue #291. |
| `CHETTER_METRICS_AUTH_TOKEN` | No | empty | When set, the Prometheus `/metrics` endpoint requires this bearer token. When empty (default), `/metrics` stays unauthenticated for backward compatibility. |
| `CHETTER_ALLOW_TOKEN_LOGIN` | No | `true` | Controls whether the web UI accepts API bearer tokens via the login form and `localStorage`. When `false`, only OIDC/SSO sessions are accepted by the browser UI (the login form is hidden and browser-stored tokens are ignored/cleared). API and MCP bearer authentication is unaffected. Disable it in deployments that use OIDC as the only browser login path. |
| `ARCANE_SERVER_URL` | No | empty | Optional Arcane scanner URL. |
| `ARCANE_API_KEY` | No | empty | Optional Arcane API key. |
| `GITHUB_APP_ID` | For GitHub app | `0` | GitHub App ID. |
| `GITHUB_APP_PRIVATE_KEY_B64` | For GitHub app | empty | Base64-encoded GitHub App private key PEM. |
| `GITHUB_INSTALLATION_ID` | No | `0` | Deprecated single-installation fallback. New deployments should omit it; Chetter resolves the installation from signed webhooks or repository identity. |
| `CHETTER_SELF_TEST_GITHUB_REPO` | No | — | Dedicated `owner/repo` cloned by the `full` deployment self-test to verify GitHub App credential minting and repository access. |
| `GITHUB_WEBHOOK_SECRET` | For GitHub webhook | empty | HMAC-SHA256 webhook secret. |
| `GITHUB_WEBHOOK_DISABLED` | No | `false` | Webhook kill switch. |
| `OIDC_ISSUER_URL` | For OIDC SSO | empty | OIDC provider issuer URL (Okta: `https://<tenant>.okta.com`). When set with `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `OIDC_REDIRECT_URL`, the web UI enables SSO login. |
| `OIDC_CLIENT_ID` | For OIDC SSO | empty | OIDC client ID registered with the IdP. |
| `OIDC_CLIENT_SECRET` | For OIDC SSO | empty | OIDC client secret. |
| `OIDC_REDIRECT_URL` | For OIDC SSO | empty | OIDC redirect URL, must match the IdP app registration (e.g. `https://chetter.example.com/auth/callback`). |
| `OIDC_ADMIN_GROUP` | No | `chetter-admin` | IdP group that grants full admin scope. |
| `OIDC_TEAM_GROUP_PREFIX` | No | `chetter-` | IdP group prefix for team mapping: `chetter-<team>` maps to team `<team>`. Empty disables team mapping. |
| `OIDC_SESSION_SECRET` | No | derived from `MCP_AUTH_TOKEN` | HMAC key signing web session JWTs (minimum 32 bytes). Rotating it (or `MCP_AUTH_TOKEN` when unset) signs out all web sessions. |
| `OIDC_SESSION_TTL` | No | `8h` | Web session lifetime (Go duration, e.g. `1h`, `24h`). |

### Deployment self-tests

Administrators can start an end-to-end deployment check from the Diagnostics page or with `chetter_run_self_test`. The command requires one of these profiles:

- `quick` checks the default OpenCode and model path.
- `harnesses` checks OpenCode, Claude Code, Pi, CodeWhale, and Codex with their configured defaults.
- `providers` checks each model-catalog provider through OpenCode.
- `full` combines harness and provider checks and adds the GitHub credential check when `CHETTER_SELF_TEST_GITHUB_REPO` is configured.

Every check is a normal runner task. Passing requires a successful terminal task and runner-observed evidence that the harness discovered and invoked the authenticated `chetter_runner_self_test_echo` MCP tool. Use `chetter_self_test_status` with the returned run ID to inspect aggregate and per-check results.

### Runner And Agent Containers

| Variable | Purpose |
|---|---|
| `CHETTER_SERVER_URL` | Server URL used by the runner. |
| `CHETTER_RUNNER_AUTH_TOKEN` | Runner config token env. Compose fills this from `CHETTER_RUNNER_RPC_TOKEN` for current runner fallback compatibility. |
| `CHETTER_MCP_AUTH_TOKEN` | MCP token injected into agents for Chetter MCP tools. |
| `CHETTER_MCP_URL` | MCP URL injected into agents. |
| `CHETTER_TASK_ID` | Protected durable Task identifier injected for the current execution. |
| `CHETTER_AGENT_SESSION_ID` | Protected AgentSession identifier injected for the current execution. |
| `CHETTER_USER_PROMPT_ID` | Protected UserPrompt identifier injected for the current execution. |
| `CHETTER_EXECUTION_ID` | Protected immutable ExecutionAttempt identifier used for attribution and fencing. |
| `EXECUTION_BACKEND` | Execution backend selector: `docker`, `kubernetes`, or development-only `local`. Default `docker`. |
| `USE_GVISOR` | Enables Docker `runsc` execution when `true`. |
| `CHETTER_ALLOW_UNISOLATED` | Escape hatch for single-tenant/trusted deployments without gVisor: accept isolation-requiring tasks even when the runner cannot enforce a sandbox. Must match the server setting. See issue #291. |
| `CHETTER_PROXY_ALLOWED_DOMAINS` | Optional HTTP/HTTPS egress allowlist. |
| `CHETTER_PROXY_BLOCKED_DOMAINS` | Optional HTTP/HTTPS egress blocklist. |
| `CHETTER_DNS_BLOCKED_DOMAINS` | Optional DNS blocklist. |
| `GITHUB_TOKEN` | Temporary compatibility token for repositories the configured GitHub App cannot access, such as fork heads. App credentials take precedence for task repositories. |
| `CHETTER_GITHUB_CREDENTIAL_URL`, `CHETTER_GITHUB_CREDENTIAL_TOKEN` | Runner-managed execution-scoped credential bridge values. Tasks cannot override them; operators should not configure them. |
| `SYNTHETIC_API_KEY`, `DEEPSEEK_API_KEY`, `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY` | Provider keys forwarded when configured. |
| `MEM9_API_KEY`, `MEM9_API_URL`, `MEM9_DEBUG`, `MEM9_HOME` | Optional Mem9 persistent memory integration. |
| `CHETTER_CLAUDE_MAX_TURNS` | Claude Code harness turn cap (`--max-turns`); default `500`. Unset or invalid values fall back to the default. |
| `CHETTER_CLAUDE_MAX_MCP_OUTPUT_TOKENS` | Sets `MAX_MCP_OUTPUT_TOKENS` for the Claude Code harness; default `50000` (the documented 25000 default truncates runner-bridge payloads like PR diffs). |
| `CHETTER_CLAUDE_MAX_OUTPUT_TOKENS` | When set, passed through to `CLAUDE_CODE_MAX_OUTPUT_TOKENS`. |
| `CHETTER_CLAUDE_MAX_BUDGET_USD` | Optional per-task spend ceiling for the Claude Code harness, passed as `--max-budget-usd`. Subagent spend counts toward it. |

The complete runner environment and `runner.yaml` reference, including
container resource limits (`CHETTER_CONTAINER_MEMORY`, `CHETTER_CONTAINER_CPU`,
`CHETTER_CONTAINER_PIDS`), lives in [runner/README.md](../runner/README.md).

### Data Retention And Storage Pruning

The server reaper can bound growth in the high-volume historical tables. It
deletes rows whose `created_at` is older than the configured number of days:

| Variable | Tables | Example |
|---|---|---|
| `EVENTS_RETENTION_DAYS` | `task_events` | `30` |
| `AUDIT_RETENTION_DAYS` | `audit_log` | `90` |
| `ARTIFACT_RETENTION_DAYS` | `task_artifacts`, `agent_sessions` | `180` |

Retention is disabled by default. An unset variable and an explicit value of
`0` both preserve rows indefinitely, so existing deployments are not affected
until an operator opts in. Each setting is independent; configure only the
tables that should be pruned. For example:

```dotenv
EVENTS_RETENTION_DAYS=30
AUDIT_RETENTION_DAYS=90
ARTIFACT_RETENTION_DAYS=180
```

The cleanup is application-level and works with TiDB, MySQL, and PostgreSQL.
The reaper deletes rows in batches of up to 1,000 to limit transaction size and
lock contention, and reports deleted counts in the server log. Changes to these
variables take effect after restarting the server. Retention deletes data
permanently, so choose values that satisfy operational, audit, and compliance
requirements before enabling it.

## Optional Mem9 Integration

Chetter supports [Mem9](https://mem9.ai/) persistent memory for the OpenCode
harness, but does not require or enable it by default. Mem9 is enabled only when
the runner starts with a non-empty `MEM9_API_KEY`. Without that key, Chetter does
not add the Mem9 plugin to the generated OpenCode configuration. Setting
`MEM9_API_URL` or `MEM9_DEBUG` without an API key does not enable the plugin.

Configure Mem9 in the runner environment, such as in the deployment `.env` used
by Compose:

```dotenv
MEM9_API_KEY=your-secret-key
MEM9_API_URL=https://api.mem9.ai
MEM9_DEBUG=false
```

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `MEM9_API_KEY` | To enable Mem9 | empty | Enables the integration and authenticates Mem9 API requests. Keep it secret. |
| `MEM9_API_URL` | No | `https://api.mem9.ai` in Compose | Overrides the Mem9 API endpoint. It is inert when `MEM9_API_KEY` is empty. |
| `MEM9_DEBUG` | No | `false` in Compose | Controls Mem9 plugin debug output. It is inert when `MEM9_API_KEY` is empty. |
| `MEM9_HOME` | No | empty | Overrides Mem9's state/configuration directory when supported by the plugin. |
| `MEM9_PLUGIN_SPEC` | No | `@mem9/opencode` | Advanced runner-side override for the OpenCode plugin package specification. |

Enablement is runner-wide, not per task. A configured key enables Mem9 for every
OpenCode task claimed by that runner; other harnesses do not use this integration.
The runner owns the `MEM9_*` environment variables, so task-supplied environment
values cannot replace runner credentials or opt an individual task in or out.

The standard agent base image preinstalls the Mem9 OpenCode npm package, and the
default runner network policy allows `api.mem9.ai`. These provide offline-ready
support but do not activate Mem9 or make an API request without plugin activation.
A repository can still explicitly declare plugins in its own OpenCode config;
that is repository-controlled behavior rather than Chetter's Mem9 integration.

To disable Mem9, remove or empty `MEM9_API_KEY` in the runner environment and
restart the affected runners. No Mem9-specific image change is required.

## YAML Configuration And Validation

Chetter-owned YAML files have JSON Schemas under `schemas/` and are validated by the code paths that load them. Third-party YAML files such as Kubernetes manifests, Compose files, `buf.yaml`, and `sqlc.yaml` use their own upstream validators instead.

| YAML file | Schema | Runtime validation |
|---|---|---|
| `runner/runner.yaml`, `runner/runner.docker.yaml` | `schemas/runner.schema.json` | Runner startup parses with strict known-field checks. |
| Definitions repo `model-catalog.yaml` | `schemas/model-catalog.schema.json` | Definitions sync parses with strict known-field checks and catalog semantic validation. |
| Definitions repo scoped `triggers/*.yaml` paths | `schemas/trigger.schema.json` | Definitions sync parses with strict known-field checks and trigger semantic validation. |
| Definitions repo scoped `mcp-endpoints/*.yaml` paths | `schemas/mcp-endpoint.schema.json` | Definitions sync parses with strict known-field checks and endpoint semantic validation. Bearer token values stay in runner environment variables. |
| Agent definition frontmatter in scoped `agents/*.md` paths | `schemas/agent-frontmatter.schema.json` | Definitions sync validates optional YAML frontmatter when present. Frontmatter may include `mcp_endpoints`. Plain Markdown without frontmatter is accepted. |

Validation errors fail the definitions sync before new definitions are materialized. Trigger definition errors include the path, for example `triggers/nightly.yaml: unknown trigger_type "..."`.

The definitions repo layout, YAML formats, trigger sync identity rules,
managed Git identities, and MCP endpoints are documented in
[CONFIGURATION.md](CONFIGURATION.md).

### Runner YAML

Runner config files use this shape:

```yaml
server:
  url: http://localhost:8080
  auth_token: ""

runner:
  workspace_root: /var/lib/runner
  max_concurrent: 10

proxy:
  listen_addr: :18080
  allowed_domains:
    - github.com
  blocked_domains:
    - pastebin.com

dns:
  listen_addr: :53
  upstream: 8.8.8.8:53
  blocked_domains:
    - 169.254.169.254

git:
  ssh_key_path: ""
  pat: ""

execution:
  runtime: docker
  harness: opencode
  use_gvisor: true
  container_memory: 4g
  container_cpu: 2
  container_pids: 256

deploy:
  provider: local
  registry: ""
  chetter_url: chetter.flatout.works

chetter_mcp:
  url: ""
  auth_token: ""
```

| Field | Default | Purpose |
|---|---|---|
| `server.url` | `CHETTER_SERVER_URL` env | Server URL used by the runner. |
| `server.auth_token` | First of `CHETTER_RUNNER_AUTH_TOKEN`, `CHETTER_RUNNER_RPC_TOKEN`, `MCP_AUTH_TOKEN`, `CHETTER_MCP_AUTH_TOKEN` | Runner-to-server ConnectRPC bearer token. |
| `runner.workspace_root` | `/var/lib/runner` | Host/container directory for task workspaces. |
| `runner.max_concurrent` | `10` | Maximum concurrent tasks per runner process. |
| `proxy.listen_addr` | `:18080` | HTTP/HTTPS proxy listen address used for network filtering. |
| `proxy.allowed_domains` | empty | Optional outbound HTTP/HTTPS allowlist. Empty means allowlist is disabled. |
| `proxy.blocked_domains` | empty | Optional outbound HTTP/HTTPS blocklist. |
| `dns.listen_addr` | `:53` | DNS proxy listen address. |
| `dns.upstream` | `8.8.8.8:53` | Upstream DNS server. |
| `dns.blocked_domains` | empty | Optional DNS blocklist. |
| `git.ssh_key_path` | empty | Optional SSH key path for clone operations. |
| `git.pat` | empty | Optional compatibility PAT for repositories outside the GitHub App installation. GitHub App credentials are preferred for normal deployments. |
| `execution.runtime` | empty | Reserved runtime selector. Current Docker/local mode is selected by runner mode/env. |
| `execution.harness` | empty, falls back to OpenCode | Default harness when a task or trigger does not specify one. Supported: `opencode`, `claude-code`, `pi`, `codewhale`, `codex`. |
| `execution.use_gvisor` | `USE_GVISOR=true` env | Enables Docker `--runtime=runsc` for task containers. |
| `execution.allow_unisolated` | `CHETTER_ALLOW_UNISOLATED=true` env | Escape hatch for trusted single-tenant deployments without gVisor: the runner accepts isolation-requiring tasks even when it cannot enforce a sandbox. See issue #291. |
| `execution.container_memory` | empty | Optional runner-side Docker memory cap, passed as `--memory` and `--memory-swap` (for example `4g`, `8192m`). Task limits may be stricter but cannot raise this cap. Empty means no runner-imposed cap. OOM-killed tasks report `failure_category=resource_limit`. |
| `execution.container_cpu` | empty | Optional CPU cap in cores, passed as `--cpus` (for example `1.5`). |
| `execution.container_pids` | empty | Optional PID cap, passed as `--pids-limit` (for example `256`). |
| `deploy.provider` | `local` | Reserved deployment provider metadata. |
| `deploy.registry` | empty | Reserved image registry metadata. |
| `deploy.chetter_url` | `chetter.flatout.works` | Reserved public URL metadata. |
| `chetter_mcp.url` | empty | MCP URL injected into task environments when configured. |
| `chetter_mcp.auth_token` | `CHETTER_MCP_AUTH_TOKEN` env | Upstream MCP credential retained by the runner relay; task containers receive an execution-scoped capability instead. |

Runner-owned MCP endpoints do not use the static upstream token inside task
containers. For each execution the runner creates a random capability, writes
it only to owner-readable harness configuration, and registers it with the
per-execution MCP server and optional Chetter MCP relay. Relay access is revoked
when the execution server closes. Unauthorized relay requests return HTTP 401
without contacting the configured upstream MCP service. Rejection-count
increases are reported in runner heartbeats and persisted as system-scoped
`runner_mcp_relay_request_rejected` audit events.

## Submit A Task

Use `chetter_submit_task` from an MCP client, the web UI, or an OpenCode command.

Example input:

```json
{
  "prompt": "Add input validation to all API handlers and run the tests.",
  "git_url": "https://github.com/my-org/my-repo",
  "git_ref": "main",
  "agent_image": "chetter-agent:golang",
  "harness": "opencode",
  "timeout_sec": 1800
}
```

An admin can attach MCP endpoints to a task:

```json
{
  "prompt": "Use the context MCP tools to inspect the service.",
  "agent_image": "chetter-agent:golang",
  "harness": "opencode",
  "mcp_endpoints": ["context"]
}
```

For a resumable session:

```json
{
  "prompt": "Create a PR for the next documentation improvement.",
  "git_url": "https://github.com/flatout-works/chetter",
  "git_ref": "main",
  "harness": "opencode",
  "session_mode": "resumable",
  "pause_reason": "waiting_for_pr_feedback",
  "ttl_hours": 72
}
```

To require enforced isolation (gVisor) for a specific task, for example
untrusted input in a deployment that otherwise opted out via
`CHETTER_ALLOW_UNISOLATED`:

```json
{
  "prompt": "Triage this untrusted repository report.",
  "agent_image": "chetter-agent:golang",
  "isolation": "required"
}
```

Isolation-requiring tasks (resumable, explicitly configured, or every task in a
hardened deployment) only run on runners that enforce a sandbox and fail fast
with `failure_category=harness_error` / `error_category=isolation_unavailable`
when no capable runner exists. See issue #291.

## MCP Tool Reference

### Tasks

| Tool | Purpose |
|---|---|
| `chetter_submit_task` | Submit a one-off development task. |
| `chetter_task_status` | Get task status and result details. |
| `chetter_list_tasks` | List recent tasks with optional status filter. |
| `chetter_cancel_task` | Cancel a pending or running task. |
| `chetter_clear_queue` | Admin-only cancellation of all pending tasks. |
| `chetter_task_events` | Full event history for a task. |
| `chetter_task_progress` | Distilled task progress timeline. |
| `chetter_task_latest_event` | Latest task event. |
| `chetter_task_export` | Markdown transcript for a completed task. |
| `chetter_rerun_task` | Re-run a terminal task with the same prompt, model, image, env, and timeout. |
| `chetter_recover_task` | Recover a terminal task in a fresh session, optionally with a custom prompt, using the previous session export as context. |
| `chetter_extend_task` | Extend the deadline of a pending or running task. |

### Sessions

| Tool | Purpose |
|---|---|
| `chetter_list_agent_sessions` | List recent agent sessions. |
| `chetter_agent_session_status` | Get an agent session and its runs. |
| `chetter_resume_agent_session` | Resume a paused session with a follow-up prompt. |

See [SESSIONS.md](SESSIONS.md) for the session model and operations.

### Triggers And Schedule Runs

| Tool | Purpose |
|---|---|
| `chetter_create_trigger` | Create a cron, PR review, or issue trigger. |
| `chetter_update_trigger` | Update a trigger. |
| `chetter_list_triggers` | List triggers, optionally by type/enabled state. |
| `chetter_delete_trigger` | Delete a trigger. |
| `chetter_run_trigger` | Run a cron trigger immediately. |
| `chetter_list_schedule_runs` | List schedule run history. |

See [TRIGGERS.md](TRIGGERS.md) for cron schedules, PR review automation, and webhook configuration.

### Runner Fleet

| Tool | Purpose |
|---|---|
| `chetter_runner_health` | Fleet diagnostics and heartbeat ages. |
| `chetter_drain_runner` | Ask a runner to stop claiming new work and exit after current work. |

### GitHub Artifact Observability

| Tool | Purpose |
|---|---|
| `chetter_list_task_artifacts` | Admin-only artifact browser/filter. |

### Runner Bridge MCP Tools (Agent-Side)

These tools are exposed by a runner-local MCP endpoint. They give task agents
controlled GitHub write operations with automatic task attribution, audit logging,
and Chetter signatures. They are not exposed by the control-plane MCP server.

| Tool | Purpose |
|---|---|
| `chetter_create_issue` | Create a GitHub issue with a canonical Chetter signature and artifact/audit records. `task_id` is auto-injected by the runner. |
| `chetter_issue_comment` | Comment on a GitHub issue or PR with Chetter signature and artifact/audit records. |
| `chetter_create_pr` | Create a GitHub pull request with Chetter signature and artifact/audit records. |
| `chetter_pr_review` | Submit a review on a GitHub PR with Chetter signature and artifact/audit records. |

Agents must use these tools rather than direct `gh` or `curl` commands for GitHub
writes so that every artifact receives a task-linked audit record and a canonical
Chetter footer. The `gh` wrapper blocks write commands and guides agents to the
MCP tools. Read-only `gh` commands remain available for inspection.

### Admin, Definitions, And Audit

| Tool | Purpose |
|---|---|
| `chetter_create_token`, `chetter_list_tokens`, `chetter_delete_token` | Admin token management. |
| `chetter_create_team`, `chetter_list_teams`, `chetter_delete_team`, `chetter_list_users` | Admin team/user management. |
| `chetter_create_git_identity`, `chetter_list_git_identities`, `chetter_update_git_identity`, `chetter_delete_git_identity`, `chetter_set_git_identity_default` | Managed Git identity management (author name, email, credential provider). See [CONFIGURATION.md](CONFIGURATION.md#managed-git-identities). |
| `chetter_get_model_catalog` | Read the active model catalog summary. |
| `chetter_sync_definitions` | Admin manual sync of the definitions repo. |
| `chetter_list_audit_events` | Admin audit log query. |
| `chetter_usage_summary` | Aggregate token usage and cost totals grouped by team, trigger, and repository with time-window and filters. Admins see all teams; team tokens see only their data. |

### Conditional Arcane Tools

Registered only when `ARCANE_SERVER_URL` and `ARCANE_API_KEY` are configured:

- `chetter_arcane_scanner_status`
- `chetter_arcane_environment_summary`
- `chetter_arcane_list_images`
- `chetter_arcane_image_summary`
- `chetter_arcane_list_vulnerabilities`

## Common Operations

### Health

```bash
curl http://localhost:18088/healthz
```

A `/readyz` endpoint on both the MCP server (port 8080) and web API (port 8090) performs a database ping (1s timeout) and returns 503 if the schema is not applied or the database is unreachable. Use it for Kubernetes readiness probes; use `/healthz` for liveness probes.

A Prometheus `/metrics` endpoint on the MCP server (port 8080, no auth required) exposes standard Go runtime and process collectors plus `chetter_*` gauges for task counts by status, runner fleet health (active/stale, available/occupied slots), cumulative MCP relay rejections (`chetter_mcp_relay_rejected_requests`), and webhook delivery status. All custom gauges have bounded cardinality — no task, runner, token, or user IDs appear as labels.

### Logs

```bash
docker compose -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f
docker compose -f deploy/compose.yaml -f deploy/compose.local.yaml logs -f chetter-mcp
```

### Restart After `.env` Changes

```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d
```

### Stop

```bash
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml down
```

### Migrations

```bash
make migrate
make migrate-status
```

The container entrypoint applies the matching dialect's Goose migrations before
starting Chetter. PostgreSQL migrations are also embedded in the server binary:
`ApplySchema` applies pending migrations under an advisory lock before checking
the current bootstrap schema, so direct binary execution follows the same
upgrade path. An existing PostgreSQL Chetter schema must retain its
`goose_db_version` table; startup refuses an unversioned schema rather than
inferring a potentially unsafe baseline across data-moving migrations.

## Deploying

For production Kubernetes deployment (EKS or similar), see [EKS.md](EKS.md) for
complete manifests, node group setup, RBAC, ingress, and gVisor node
configuration. For local k3s validation, see [K3S.md](K3S.md). Docker + gVisor
host setup, the Kubernetes gVisor DaemonSet/RuntimeClass, graceful shutdown,
and network isolation are covered in [DEPLOYMENT.md](DEPLOYMENT.md).

## Agent Images

Tasks run inside dev container images selected by `agent_image`. Image
sources, resolution (`AGENT_IMAGE_PREFIX`), available variants, custom images,
the per-container environment contract, and trigger-type environment variables
are documented in [IMAGES.md](IMAGES.md).

## Harness Interface Support Matrix

Use the `harness` field on tasks and triggers to select the agent runtime (`opencode`, `claude-code`, `pi`, `codewhale`, or `codex`). For the full capability matrix — execution models, config generation, streaming, session export, isolation support, and more — see [HARNESSES.md](HARNESSES.md).

## Arcane Deployment

Chetter's production deployment uses Arcane GitOps. GitHub Actions does not build Docker images.

Deployment flow:
1. Push to `main`.
2. GitHub Actions runs `make check`.
3. The workflow calls Arcane's API to sync GitOps, build images on wowbagger, push to GHCR, and redeploy the Chetter project.
4. Arcane redeploys containers from GHCR images.

Required GitHub repository secrets:
- `ARCANE_URL` — Arcane base URL (e.g. `https://wowbagger.krampe.se`)
- `ARCANE_API_KEY` — API key with project build/deploy permissions
- `ARCANE_CHETTER_PROJECT_ID` — Chetter project ID
- `ARCANE_CHETTER_GITOPS_ID` — GitOps sync ID

Arcane GitOps must use Compose path `compose.yaml` with directory sync enabled.

## Runner Concurrency

Each runner can handle multiple tasks simultaneously via `RUNNER_MAX_CONCURRENT`. Each task gets its own Docker container with its own port, so tasks are process-isolated even within a single runner.

| | Multiple tasks per runner | More runners |
|---|---|---|
| Overhead | One process, one heartbeat stream, one Docker connection | N× process overhead, N× heartbeats |
| Resource efficiency | Lower baseline CPU/memory when idle | Each runner consumes resources even when idle |
| Task pickup | Semaphore slot frees immediately | New runner must spin up |
| Blast radius | Runner crash/OOM kills all in-flight tasks | Only one task lost per runner failure |
| Debugging | Interleaved logs from concurrent tasks | Clean per-runner logs |

**Recommended:** `RUNNER_MAX_CONCURRENT=2` or `3` per runner pod. For production, 4 pods with `MAX_CONCURRENT=2` = 8 concurrent tasks, with only 2 tasks lost per pod failure.

## Related Docs

- [FEATURES.md](FEATURES.md) — current capability reference.
- [TRIGGERS.md](TRIGGERS.md) — cron schedules and PR review automation.
- [SESSIONS.md](SESSIONS.md) — resumable sessions.
- [HARNESSES.md](HARNESSES.md) — harness architecture.
- [CONFIGURATION.md](CONFIGURATION.md) — configuration-as-code, definitions repo, model catalog.
- [PROVIDERS.md](PROVIDERS.md) — provider support matrix.
- [EXECUTION.md](EXECUTION.md) — execution backends (docker, kubernetes, local).
- [DEPLOYMENT.md](DEPLOYMENT.md) — deployment and sandboxing guides.
- [IMAGES.md](IMAGES.md) — agent images and container contract.
- [K3S.md](K3S.md) — local k3s validation.
- [EKS.md](EKS.md) — production EKS installation.
- [LITELLM.md](LITELLM.md) — LiteLLM gateway integration.
- [PLAN.md](PLAN.md) — roadmap.
