# Configuration In Git

Chetter treats automation definitions as code. Agent definitions, skills,
triggers, and reusable task templates are configuration that benefit from pull
request review, diffs, rollback, blame, and branch-based experimentation.

The recommended model is Git as the authoritative definition registry, with
the database as the parsed runtime index and historical cache.

## Goals

- Keep definitions reviewable through normal GitHub pull requests.
- Support global, team, and repository-specific definitions.
- Let tasks record the exact trigger, agent, skills, prompt, and definition
  revision that produced their output.
- Let agents analyze previous task exports and propose improvements as pull
  requests instead of silently changing production behavior.
- Keep runtime lookups fast and resilient by materializing definitions into
  the database after validation.

## Non-Goals

- Do not make the database the only source of truth for definitions.
- Do not require every task run to fetch live files from Git.
- Do not allow meta-improvement agents to bypass review for agent or skill
  definition changes.

## Definition Sources

Chetter can be configured with one or more definition sources. A source points
at a Git repository and an optional path within that repository.

Definitions are resolved from least specific to most specific:

1. Global definitions
2. Team definitions
3. Repository definitions

The most specific definition wins when names collide.

Example layout:

```text
global/
  agents/
    pr-reviewer.md
    issue-triage.md
  skills/
    chetter/SKILL.md
  triggers/
    nightly-docs.yaml
  task-templates/
    improve-agent.md
```

Example scope mapping:

```text
global source: github.com/flatout-works/chetter-definitions
team source:   github.com/acme/automation-definitions
repo source:   github.com/acme/my-service, path .chetter/
```

When a task for `github.com/acme/my-service` references `agent: pr-reviewer`,
Chetter resolves it as:

```text
repo:   repos/acme/my-service/agents/pr-reviewer.md
team:   groups/acme/agents/pr-reviewer.md
global: global/agents/pr-reviewer.md
```

### Scope Directories

Definitions must live under explicit scope directories:

```text
model-catalog.yaml
global/agents/...
global/skills/...
global/triggers/...
global/mcp-endpoints/...
global/task-templates/...
groups/<team-name>/agents/...
groups/<team-name>/skills/...
groups/<team-name>/triggers/...
groups/<team-name>/mcp-endpoints/...
repos/<owner>/<repo>/agents/...
repos/<owner>/<repo>/skills/...
repos/<owner>/<repo>/triggers/...
repos/<owner>/<repo>/task-templates/...
```

`global/...` definitions are global. `groups/<team-name>/...` definitions are team-scoped and the team name must already exist in Chetter. `repos/<owner>/<repo>/...` definitions are repo-scoped and store `<owner>/<repo>` on the materialized definition. Group-scoped trigger definitions create or update triggers with that group's `team_id`; global and repo-scoped trigger definitions are not team-owned. Root-level definition directories are ignored; only `model-catalog.yaml` remains at the repository root.

Supported YAML formats are:

| Path | Required fields | Notes |
|---|---|---|
| `model-catalog.yaml` | `version`, `default_provider`, `default_model`, `providers` | `providers` is a mapping keyed by provider ID. Secret values are not allowed; use env var names such as `api_key_env: DEEPSEEK_API_KEY`. |
| `<scope>/triggers/*.yaml` | `name` | Supported under `global/`, `groups/<team-name>/`, and `repos/<owner>/<repo>/`. `trigger_type` defaults to `cron`; supported values are `cron`, `pr_review`, and `issue`. `repo`, `event`, `match_labels`, `session_mode`, `pause_reason`, and `ttl_hours` are copied into `trigger_config` during sync. |
| `global/mcp-endpoints/*.yaml`, `groups/<team-name>/mcp-endpoints/*.yaml` | `name`, `url` | Global or team-scoped HTTP or SSE endpoint. `auth.token_env` names a variable configured on every runner; static `headers` are persisted and must not contain secrets. |
| `<scope>/agents/*.md` | `identity` | Supported under global, team, and repository scopes. YAML frontmatter must reference a server-managed Git identity by name; it may also include `description`, `provider`, `model`, `mode`, `mcp_endpoints`, and `permission`. The Markdown body is the agent prompt. Identity credentials are never stored in the definitions repository. |

### Trigger Sync Identity and Renames

A trigger materialized from a definition source carries a stable identity derived from the definition source ID and the trigger `name`. Re-syncing an unchanged trigger definition updates the row in place: the trigger ID, trigger runs, usage attribution, and task references all keep pointing at the same trigger, and exactly one cron registration exists per enabled cron trigger.

Removing a trigger definition from the source deletes the corresponding trigger row and tears down its in-memory cron schedule on the next sync, so no stale cron entries fire. Toggling `enabled` off, changing `trigger_type` away from `cron`, or editing `cron_expr` updates in place and never duplicates cron registrations.

Renames are **delete old + create new**. A trigger definition renamed from `nightly` to `daily` is treated as removing `nightly` and adding `daily`: the old trigger row and cron schedule are removed, and a fresh trigger with a new ID is created. Trigger run history and usage attribution recorded against the old name/ID do **not** follow the rename. To preserve history across a rename, keep the trigger `name` stable and change other fields instead.

## Runtime Model

Git is authoritative. The database stores a validated, parsed, active view for runtime
lookup and historical analysis.

Suggested tables:

```text
definition_sources
definitions
definition_sync_runs
definition_change_proposals
```

Implemented registry tables:

- `definition_sources` records Git definition sources. The first implementation
  materializes the configured `DEFINITIONS_REPO` as a global source named
  `default`.
- `definitions` stores active parsed definitions from supported paths with the
  source commit, file path, raw content, and SHA-256 content hash.
- `definition_sync_runs` records success and failure history for each sync.

Supported indexed paths:

```text
global/agents/*.md
global/skills/*.md
global/skills/*/SKILL.md
global/triggers/*.yaml
global/triggers/*.yml
global/mcp-endpoints/*.yaml
global/mcp-endpoints/*.yml
global/task-templates/*.md
groups/<team-name>/{agents,skills,triggers,mcp-endpoints,task-templates}/...
repos/<owner>/<repo>/{agents,skills,triggers,task-templates}/...
```

The runtime DB should store enough information to answer:

- Which definitions are active?
- Which source, scope, and commit did they come from?
- What content hash was used?
- Which definitions were used by a specific task?
- Did the latest sync succeed or fail validation?

## Task Attribution

Every task should capture immutable attribution metadata at submission time:

```text
trigger_name
trigger_id
trigger_definition_hash
agent_name
agent_definition_hash
skill_hashes
prompt_hash
definition_source_commit
```

This lets later analysis connect a session export back to the exact inputs that
caused it. The first implementation step is to add `trigger_name` to task
records, because that unlocks reliable trigger-to-task correlation.

## Change Workflow

Human or outside changes:

```text
1. Open PR against the definitions repo.
2. Review and merge.
3. Chetter detects the new commit through periodic sync, a manual sync, or a webhook.
4. Chetter validates and materializes definitions into the database.
5. Future tasks use the new definitions.
```

Meta-improver changes:

```text
1. Meta-improver lists triggers, tasks, events, and session exports.
2. It resolves the related agent, skill, trigger, and prompt definitions.
3. It edits definition files in a branch.
4. It opens a pull request with rationale and evidence from prior tasks.
5. After merge, Chetter syncs the new active definitions.
```

Direct DB mutations should be reserved for operational overrides such as
temporarily disabling a trigger. Durable definition changes should go through
Git.

## Managed Git Identities

Git identities control commit attribution for task work. They contain only an identity reference, author name, author email, and credential provider; GitHub App credentials remain server-managed and must never be committed to the definitions repository.

Create the recommended global `primary-bot` identity after configuring the GitHub App:

```bash
export CHETTER_API_URL=https://chetter.example.com
# Set CHETTER_TOKEN to the Chetter admin API token through your shell or secret manager.

chetterctl identity create \
  --name primary-bot \
  --git-author-name 'chetterbot[bot]' \
  --git-author-email '292266004+chetterbot[bot]@users.noreply.github.com'

chetterctl identity set-default --name primary-bot
```

The default identity is used by tasks submitted without an agent. Chetter resolves a team-scoped default first, then the global default. An agent definition's `identity:` field takes precedence over both defaults:

```markdown
---
identity: primary-bot
---

You are a focused implementation agent.
```

Use `chetterctl identity set-default --team <team-name> --name <identity>` to set a team default. Admins can also create identities and select their defaults in the **Admin > Git Identities** UI or through the corresponding MCP tools. A task fails clearly when neither an agent identity nor an applicable default identity exists.

## MCP Endpoints

MCP endpoints let tasks connect to remote HTTP or SSE MCP servers during execution. An endpoint definition stores the URL, transport, optional non-secret static headers, and the name of a runner environment variable that holds the bearer token. The token value itself is never stored in the definitions repo, the task database, or the runner RPC payload — only the variable name travels through the system, and the runner imports the value at task time.

Endpoint definitions are YAML files under a global or team scope in the config repo:

```text
global/mcp-endpoints/context.yaml    # global endpoint
groups/engineering/mcp-endpoints/team-only.yaml  # team-scoped
```

Endpoint name must match the file stem (e.g. `context.yaml` must define `name: context`). Names must start with a letter or number and contain only letters, numbers, dot, underscore, or dash. The names `runner-bridge` and `chetter` are reserved.

Example endpoint definition:

```yaml
name: context
transport: http
url: https://mcp.example.com/mcp
headers:
  X-Tenant: engineering
auth:
  type: bearer
  token_env: CONTEXT_MCP_TOKEN
```

Set `CONTEXT_MCP_TOKEN` on each runner deployment. Bearer authentication is represented by this variable name, never its value, in definitions, task data, and runner RPC. The runner imports the value into the selected task environment and passes it to the harness; the value does not appear in Docker arguments.

For SSE transport, set `transport: sse`. Static `headers` are persisted verbatim and must not contain secrets; configure bearer authorization exclusively via `auth.token_env`.

### Scoping

MCP endpoints support global and team scope:

- **Global** endpoints under `global/mcp-endpoints/` are available to all tasks.
- **Team** endpoints (under `groups/<team-name>/mcp-endpoints/`) are available only to tasks owned by that team.

At submission time the server resolves requested endpoint names from both global
and the task's team scope and stores them in the AgentSession configuration
snapshot. If a name exists in both scopes, the team-scoped definition takes
precedence. Claiming resolves the stored names to current endpoint connection
details without persisting bearer token values.

### Attaching endpoints to tasks

Attach endpoints to a task at submit time:

```json
{
  "prompt": "Use the context MCP tools to inspect the service.",
  "agent_image": "chetter-agent:golang",
  "harness": "opencode",
  "mcp_endpoints": ["context"]
}
```

MCP endpoints cannot be attached to resumable tasks. The runner validates that each selected `token_env` variable is set in the runner environment before starting the agent; a missing variable fails the task with a clear error message. Task-provided environment variables with the same name as a selected token env are rejected — the runner-owned value always wins.

### Agent-declared endpoints

Agent definitions can declare MCP endpoints in their YAML frontmatter:

```markdown
---
identity: primary-bot
mcp_endpoints:
  - context
  - github
---

You are a code review agent that uses context and GitHub MCP tools.
```

At submission time, the server reads the agent definition's frontmatter and
merges the declared endpoint names with explicitly requested `mcp_endpoints` in
the AgentSession snapshot. This means an agent that depends on specific MCP
tools will always have them available without the submitter needing to remember
every endpoint name.

### Recovery

`chetter_recover_task` creates a fresh AgentSession under the same Task and
copies the previous session's configuration snapshot, including `mcp_endpoints`.

### Local mode

In local (non-Docker) execution, all runner environment variables are visible to every task process. MCP endpoint tokens from other teams are visible in local mode. Local mode and plain Docker execution are intended for single-trust or convenience use only. Use gVisor for a task security boundary in multi-team or untrusted-workload deployments.

## MCP Surface

Read/sync tools:

```text
chetter_list_definition_sources
chetter_get_definition_source
chetter_sync_definition_source
chetter_list_definitions
chetter_get_definition
```

Proposal tools:

```text
chetter_create_definition_proposal
chetter_list_definition_proposals
chetter_get_definition_proposal
```

## Open Questions

- Whether trigger sync should replace existing DB trigger updates entirely or
  coexist with manual `chetter_update_trigger` as an override path.
- How strict validation should be for unknown agent frontmatter fields and skill
  metadata.
- Whether definition sources should also support GitHub webhook sync in addition
  to the current five-minute polling and manual sync.

## Implementation Status

All five phases are shipped:

1. Task attribution fields including `trigger_name`. &#10003;
2. Definition source schema and read-only sync/indexing. &#10003;
3. Definition MCP read/sync tools. &#10003;
4. The weekly meta-improver agent and trigger. &#10003;
5. PR proposal tooling for definition changes. &#10003;

---

## Model Catalog

Chetter keeps provider and model definitions in a generic YAML catalog that
lives in a Git definitions repo. The server syncs it into the database and resolves
the harness-specific provider/model before a runner receives a task.

Runners do not receive or parse the full catalog. Claimed tasks contain the
resolved `harness`, `provider_id`, `model_id`, and provider metadata needed by
the selected harness to write its local config.

If no definitions repo is configured (`DEFINITIONS_REPO` env var), Chetter
uses a built-in default catalog with common providers (Synthetic, DeepSeek,
Z.ai, Anthropic, OpenCode Zen).

### Setup

1. Create a Git repo (or use an existing one) with a `model-catalog.yaml` at
   the root. The Flatout default repo is `github.com/flatout-works/chetter-config`.
2. Set `DEFINITIONS_REPO` (and optionally `DEFINITIONS_BRANCH`) on the MCP
   server.
3. Start (or restart) the server. It clones the repo, validates the catalog,
   and stores it as the active catalog in `model_catalogs`.
4. Chetter re-pulls the definitions repo every five minutes and updates the DB.
   To refresh immediately, call `chetter_sync_definitions` (admin only) or restart.

Example MCP server environment:

```bash
DEFINITIONS_REPO=git@github.com:flatout-works/chetter-config.git
DEFINITIONS_BRANCH=main
```

The catalog must not contain secret values; use env var names such as
`api_key_env: SYNTHETIC_API_KEY`.

### Shape

```yaml
version: 1
default_provider: synthetic
default_model: hf:zai-org/GLM-5.2

defaults:
  opencode:
    provider: synthetic
    model: hf:zai-org/GLM-5.2
  pi:
    provider: zai
    model: glm-5.2
  claude-code:
    provider: anthropic
    model: claude-sonnet-4-5
  codewhale:
    provider: deepseek
    model: deepseek-chat
  codex:
    provider: openai
    model: gpt-5.4

providers:
  synthetic:
    name: Synthetic
    kind: openai_compatible
    models:
      - id: hf:zai-org/GLM-5.2

  deepseek:
    name: DeepSeek
    kind: openai_compatible
    api_key_env: DEEPSEEK_API_KEY
    base_url: https://api.deepseek.com
    models:
      - id: deepseek-chat

  openai:
    name: OpenAI
    kind: native
    api_key_env: OPENAI_API_KEY
    base_url: https://api.openai.com/v1
    models:
      - id: gpt-5.4

  aws-bedrock:
    name: Amazon Bedrock
    kind: aws_bedrock
    api_key_env: AWS_ACCESS_KEY_ID
    base_url: https://bedrock-runtime.us-east-1.amazonaws.com
    aws_profile: my-profile    # optional AWS SSO profile name
    aws_region: us-east-1      # optional AWS region
    models:
      - id: us.anthropic.claude-sonnet-4-20250514-v1:0
```

`kind: openai_compatible` is enough for OpenCode provider rendering. Native
providers can still be listed for harnesses such as Claude Code, Pi, CodeWhale,
or Codex without
OpenCode trying to render them as OpenAI-compatible endpoints.

Use provider or model `harnesses` overrides when a harness needs a different
ID, API transport, endpoint, credential environment variable, or should
disable an entry.

### LiteLLM

LiteLLM is a first-class router provider for OpenCode, Pi, and Claude Code.
Use one logical provider and map it to the API contract each harness expects:

```yaml
providers:
  litellm:
    name: Corporate LiteLLM
    kind: openai_compatible
    api_key_env: LITELLM_API_KEY
    models:
      - id: coding-model # LiteLLM model_name alias
    harnesses:
      opencode:
        id: litellm
        api: openai-completions
        base_url: https://litellm.example.com/v1
      pi:
        id: litellm
        api: openai-completions
        auth_header: true
        base_url: https://litellm.example.com/v1
      claude-code:
        id: litellm
        api: anthropic-messages
        base_url: https://litellm.example.com
      codewhale:
        disabled: true
```

Set `LITELLM_API_KEY` on every runner, not in the catalog or a submitted task.
Chetter forwards only the resolved provider key to the agent container. The
LiteLLM router hostname must also be included in the runner's
`proxy.allowed_domains` list for gVisor-isolated tasks. OpenCode and Pi use
LiteLLM's OpenAI-compatible endpoint; Claude Code uses its
Anthropic-compatible messages endpoint. CodeWhale is disabled because it does
not yet support generic provider credentials.

### Provider Kinds

| Kind | Protocol | Supported harnesses |
|---|---|---|
| `openai_compatible` | OpenAI Completions API (`/v1/chat/completions`) | OpenCode, Pi† |
| `native` | Harness-native (Responses API, Anthropic API, etc.) | Claude Code, Pi, CodeWhale, Codex |
| `aws_bedrock` | Responses API via AWS SigV4 auth | Codex |

† Pi resolves providers through its own catalog; Chetter supplies defaults via env vars.

See [docs/PROVIDERS.md](PROVIDERS.md) for the full harness × provider matrix.

### Viewing

Use `chetter_get_model_catalog` (no admin required) to see the active
catalog's default provider/model, provider count, model count, and source.
