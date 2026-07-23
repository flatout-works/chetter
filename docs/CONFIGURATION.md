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
   and stores it as the active catalog in `chetter_model_catalogs`.
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
