# Configuration In Git

Chetter should treat automation definitions as code. Agent definitions, skills,
triggers, and reusable task templates are configuration that benefit from pull
request review, diffs, rollback, blame, and branch-based experimentation.

The recommended model is Git as the authoritative definition registry, with
TiDB as the parsed runtime index and historical cache.

## Goals

- Keep definitions reviewable through normal GitHub pull requests.
- Support global, team, and repository-specific definitions.
- Let tasks record the exact trigger, agent, skills, prompt, and definition
  revision that produced their output.
- Let agents analyze previous task exports and propose improvements as pull
  requests instead of silently changing production behavior.
- Keep runtime lookups fast and resilient by materializing definitions into
  TiDB after validation.

## Non-Goals

- Do not make TiDB the only source of truth for definitions.
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
repo:   .chetter/agents/pr-reviewer.md
team:   agents/pr-reviewer.md
global: agents/pr-reviewer.md
```

## Runtime Model

Git is authoritative. TiDB stores a validated, parsed, active view for runtime
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
agents/*.md
skills/*.md
skills/*/SKILL.md
triggers/*.yaml
triggers/*.yml
task-templates/*.md
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
4. Chetter validates and materializes definitions into TiDB.
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

Initial read/sync tools:

```text
chetter_list_definition_sources
chetter_get_definition_source
chetter_sync_definition_source
chetter_list_definitions
chetter_get_definition
```

Later proposal tools:

```text
chetter_create_definition_proposal
chetter_list_definition_proposals
chetter_get_definition_proposal
```

The first meta-improver can work without all of these tools because the runner
already clones a repository and has Chetter MCP access to tasks, exports, and
triggers. These tools make the model explicit and usable from any MCP client.

## Open Questions

- Whether a repository-specific source should always be the task repository, or
  whether each team can map arbitrary repositories to definition sources.
- Whether trigger sync should replace existing DB trigger updates entirely or
  coexist with manual `chetter_update_trigger` as an override path.
- How strict validation should be for unknown agent frontmatter fields and skill
  metadata.
- Whether definition sources should also support GitHub webhook sync in addition
  to the current five-minute polling and manual sync.

## Implementation Phases

1. Add task attribution fields, starting with `trigger_name`. ✓
2. Add definition source schema and read-only sync/indexing. ✓
3. Add definition MCP read/sync tools. ✓
4. Add the weekly meta-improver agent and trigger. ✓
5. Add PR proposal tooling for definition changes.
