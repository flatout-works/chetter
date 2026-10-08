---
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
execution: code
product_contract_source: "operator request for a fast trigger experiment loop; extends the definitions sync model and the definition proposal workflow"
title: "feat: experiment with triggers in the database and promote them to chetter-config"
date: 2026-10-08
---

# Experiment With Triggers In The Database, Promote To Git

## Goal

Let a developer create and iterate on a trigger in the running Chetter database
without touching `chetter-config` or waiting for a pull request, then promote
that trigger into `chetter-config` as reviewed YAML at a deliberate commit
point.

The workflow:

```text
create in DB  ->  iterate at runtime speed  ->  promote
                                             ->  definition proposal PR
                                             ->  merge  ->  next sync makes it Git-managed
```

The first phase is deliberately narrow:

- make the existing DB-versus-Git ownership distinction visible and safe;
- add one promotion action that reuses the existing definition proposal
  machinery instead of adding new PR plumbing;
- guarantee that promoted YAML is byte-identical to what a hand-written
  definition would look like, verified by a round-trip golden test.

Do not add a general config editor, a Git write path outside pull requests, or
a second source of truth.

## Non-Goals

- Not a web UI editor in this plan. The Web UI can consume the same service
  methods later; no UI work is in scope beyond exposing the managed flag.
- Not permission changes. A trigger created through `chetter_create_trigger`
  remains subject to the existing auth scoping.
- Not a change to how Git-managed triggers are authored by hand. Direct file
  edits, the nightly automation triggers, and existing pull requests keep
  working exactly as they do now.
- Not secret management. Promoting a definition that contains secret material
  is refused, not supported.

## Current State (validated against this commit)

The database-first experiment loop **already works today**. That is the key
finding: this plan is mostly about making it safe and finishable, not about
building it.

Three facts, each verified in source:

1. **Database-created triggers are never touched by sync.**
   `Service.CreateTrigger` builds `repository.CreateTriggerParams`, which has no
   `SourceID` field, so the row gets `source_id = NULL`. The sync's orphan
   cleanup skips exactly those rows:

   ```go
   // internal/service/model_catalog_tools.go
   for _, trigger := range existingTriggers {
       if !trigger.SourceID.Valid || trigger.SourceID.String != defaultDefinitionSourceID {
           continue
       }
       ...
   }
   ```

   A DB-created trigger therefore survives every sync, stays running, and is
   never deleted by a yaml change.

2. **Promotion already preserves trigger identity and run history.**
   `UpsertTrigger` is `INSERT ... ON DUPLICATE KEY UPDATE` against the unique
   key `uq_chetter_triggers_name (name)`, and it assigns
   `source_id = VALUES(source_id)`. When a same-named yaml trigger arrives, the
   sync **adopts the existing row instead of inserting a new one**, so the row
   `id` is preserved and every `trigger_runs` entry stays attached. This is the
   behavior the workflow needs — but it is implicit and unguarded, which is what
   `H1` below addresses.

3. **The proposal seam exists.**
   `chetter_create_definition_proposal` already accepts
   `files: [{path, content}]`, opens a pull request, and records a
   `definition_change_proposals` row (`source_id`, `repo`, `branch`,
   `base_branch`, `pr_number`, `pr_url`, `files` JSON, `status`) with live PR
   status. Promotion needs a renderer, not new PR plumbing.

What does *not* exist:

- No way to see which triggers are Git-managed from `chetter_list_triggers`.
  The ownership signal is invisible to callers, which blocks the workflow at
  step one.
- No renderer from a trigger row to canonical definition YAML.
- No collision guard: the sync sets `source_id` by name unconditionally.
- No scope or path information on the row, so a promotion cannot infer whether
  the definition belongs under `global/`, `groups/<team>/`, or
  `repos/<owner>/<repo>/`.

## Lifecycle Model

One nullable column (`triggers.source_id`) already expresses the states, so the
model needs no new machinery:

| State | `source_id` | Sync behavior | Exit |
|---|---|---|---|
| **draft** | `NULL` | never upserted, never deleted | promote, or keep |
| **promotion pending** | `NULL` (+ tracked proposal) | still untouched, still runs on cron | PR merges |
| **managed** | `defs_default` | upserted by name; orphan-deleted when its file is removed | — |

A draft keeps running on its schedule while its promotion PR is open. This is
deliberate: the alternative (freezing the row at promotion time) would mean the
trigger stops working the moment someone starts the paperwork, which defeats the
speed argument. The PR merge is the commit point.

Escaped database edits after a PR opens are therefore possible but harmless:
they are overwritten by the sync on merge, and the PR diff shows what will win.

## Source Of Truth And Ownership

The rule to state and enforce:

- **Git is authoritative for Git-managed triggers.** If `source_id` is set, the
  yaml file wins on every sync, and server-side update/delete through direct
  APIs should be refused with an actionable error pointing at the definitions
  repo. UI control disabling is not an authorization boundary.
- **The database is authoritative for drafts.** A draft is never modified by
  sync. Promotion is the only sanctioned path from draft to managed.
- **Runtime state never flows back to Git.** Run history, `last_run_at`,
  `next_run_at`, and task attribution stay in the database.

### H1 — Collision must fail loudly, not silently adopt

Because the upsert keys on `name` and assigns `source_id` unconditionally, this
sequence silently destroys a draft:

1. a developer creates draft `nightly-reindex` and iterates on it for a week;
2. someone else lands an unrelated yaml trigger that also happens to be named
   `nightly-reindex`;
3. the next sync overwrites the draft's prompt, config, and model selection and
   re-attributes its entire run history to the Git definition.

No diff is shown, and no audit event is written. The developer's work is gone
and the only evidence is a changed row.

Required behavior: refuse the adoption by default and record an audit event
(`trigger_sync_collision`). An explicit yaml opt-in (`adopt: true`, added to
`schemas/trigger.schema.json`) permits the current behavior where it is
genuinely wanted, such as a deliberate migration. The error must name both the
draft trigger and the definition file so an operator can resolve it in one step.

This is the single most important correctness item in the plan, because it
converts a silent data-loss path into a loud one, and it is a prerequisite for
encouraging developers to keep long-lived drafts.

## The YAML Round-Trip Contract

This is the crux of the feature, and it is subtler than "marshal the row".

Parsing is **lossy**, so rendering a database row naively produces YAML that
differs from a hand-written definition in at least three ways:

1. **`agent_image` is resolved on write.** `parseTriggerDefsForSync` stores
   `s.resolveAgentImage(td.AgentImage)`, which prepends `AGENT_IMAGE_PREFIX`
   (from `AGENT_IMAGE_PREFIX`). A definition file saying `agent_image: golang`
   is stored as `ghcr.io/flatout-works/chetter-agent:golang`. Rendering the row
   verbatim would emit the fully qualified reference and break
   `chetter-config`'s convention of using short variant names.

2. **Flat yaml keys are merged into the config blob.** `ParseTriggerYAML` folds
   `session_mode`, `pause_reason`, `ttl_hours`, `repo`, `event`, and
   `match_labels` into the `trigger_config` JSON object. The row therefore has
   no `session_mode` column; rendering the row verbatim would emit a raw
   `trigger_config` JSON string instead of the flat keys authors write.

3. **The schema comment's relative depth varies by scope.** Every definition
   opens with a `# yaml-language-server: $schema=` line whose relative path
   depends on how deep the file sits:

   | Scope | Path prefix |
   |---|---|
   | `global/triggers/` | `../../../chetter/schemas/trigger.schema.json` |
   | `groups/<team>/triggers/` | `../../../../chetter/schemas/trigger.schema.json` |
   | `repos/<owner>/<repo>/triggers/` | `../../../../../chetter/schemas/trigger.schema.json` |

   The comment is stripped by the parser and stored nowhere, so the renderer
   must recompute it from the promotion's target scope. Getting this wrong
   fails the round-trip test for every file outside `global/`.

Therefore the renderer must **invert all three transforms**:

```go
// render a definition-level TriggerDef, not the database row
func triggerDefFromRecord(rec store.TriggerRecord, prefix string) (definitions.TriggerDef, error)
func renderTriggerYAML(td definitions.TriggerDef) (string, error)
```

The inverse is only well defined if it is explicit and tested. Two rules keep it
honest:

- **Render from `TriggerDef`, never from `store.TriggerRecord`.** The record
  layer is post-resolution and post-flattening; the definition layer is the
  canonical authoring shape.
- **Rendering is templated, not `yaml.Marshal` of a generic map.** Key order,
  the `prompt: |-` block scalar, and the two header comment lines in
  `chetter-config` (the `$schema` line above, plus a free-text `# Trigger: ...`
  description) are conventions that `yaml.Marshal` will not reproduce. A naive
  marshal yields a large, ugly diff on every promotion even when nothing
  semantic changed.

### The golden test that makes this safe

Phase 1's verification, and the reason to build the renderer first:

> For every trigger definition file in `chetter-config`, assert
> `render(parse(file)) == file`.

Byte-identical, for all 31 definitions across the 7 scope roots under
`global/`, `groups/`, and `repos/` (including the team directory
`groups/Chetter Core/`, whose name contains a space). Any lossy or reordering
transform shows up immediately as a diff.

The test is independently useful — it pins the canonical definition shape — and
it means a promotion can never silently reformat or corrupt the definitions
repo. Where a file legitimately cannot round-trip (for example it uses a
spelling the canonical renderer normalizes), the test carries an explicit,
reviewed allowlist rather than a blanket tolerance.

## Data Model

No schema change is required for Phases 0–2. The draft/managed distinction and
the proposal linkage already exist via `triggers.source_id` and
`definition_change_proposals`.

Phase 4 adds promotion tracking, which does require schema work. Follow the
schema-change checklist in `AGENTS.md` exactly:

- `promotion_state` (`VARCHAR(32) NULL`) and `promotion_pr_number` (`INT NULL`)
  on `triggers`;
- add both to the `CREATE TABLE` in `internal/store/schema.go`;
- confirm `internal/store/schema_postgres.go` derives the PostgreSQL types
  correctly;
- add an `ensureTriggerMetadataColumns` entry per column (one `ALTER TABLE` per
  column — TiDB rejects a multi-column statement whose `AFTER` clause
  references a column added in the same statement);
- add ordered Goose migrations in `db/migrations/` and
  `db/postgres/migrations/`;
- update `db/queries/triggers.sql` and `db/postgres/queries/triggers.sql`;
- run `make generate` and `cd internal/data && go run ./cmd/genfacade`;
- run `make check-sql-parity`.

Phase 4 also needs a promotion-state writer, with audit events for each
transition.

## Tool Surface

### Phase 0 tool surface

Extend `chetter_list_triggers` output with an ownership field, because the
workflow is unusable while ownership is invisible:

- `source`: `"database"` when `source_id IS NULL`, else the source ID;
- `managed`: boolean convenience derived from the above.

Filtering by `source` lets an operator list exactly the drafts, which is the
practical replacement for the missing "experimental triggers" concept, and it is
what a promotion-candidate review needs.

### Phase 2 tool surface

```
chetter_promote_trigger
  name        required  draft trigger name
  scope       required  "global" | "team" | "repo"
  team_name   required when scope="team"
  target_repo required when scope="repo"   e.g. "flatout-works/chetter"
  path        optional  override the derived file path
  title/body  optional  PR metadata overrides
  draft_pr    optional  open as a draft pull request
```

Behavior:

1. load the trigger by name; **refuse if `source_id` is already set** with a
   message naming the owning definition file;
2. render canonical YAML via the round-trip renderer, targeting
   `<scope-root>/triggers/<name>.yaml`;
3. refuse if the rendered content fails the secret scan (below);
4. refuse if a definition file already exists at the target path, or any
   definition in the source already uses this name;
5. create the pull request through the existing proposal path and record the
   proposal row;
6. return the PR URL and the rendered content, so the caller can review the
   exact diff before merge.

Path derivation mirrors the sync's own layout rules
(`triggers/*.yaml` under each scope root), so a promoted file lands exactly
where the scanner expects it — including computing the `$schema` comment depth
for that scope.

## Security

- **Secret scan before render.** Database prompts are free-form and can contain
  keys, tokens, or internal hostnames. `chetter-config` is explicit that secret
  values must not be committed. Promotion therefore scans the rendered content
  for credential-shaped patterns (assignments to `*_KEY`, `*_TOKEN`,
  `*_SECRET`, bearer tokens, private key headers) and refuses with the offending
  line reported. This is a guard against accidents, not a proof of safety; the
  PR review remains the real gate.
- **No direct Git writes.** Promotion only opens a pull request. There is no
  commit-to-branch or push-to-main path associated with this feature.
- **No privilege escalation.** Promotion requires the same authorization as
  creating a definition proposal today; it does not grant new rights over
  `chetter-config`.

## Promotion Workflow

```text
chetter_create_trigger + chetter_run_trigger_now      (iterate, no PR)
        |
        v
chetter_promote_trigger                               (render -> proposal PR)
        |
        v
definition proposal PR with canonical YAML diff       (human review)
        |
        v
merge  ->  definitions sync  ->  source_id = defs_default, run history preserved
```

The `id`-preserving upsert is what makes the final arrow gapless: the trigger
keeps its identity and its run history across the transition, so the promotion
does not reset usage attribution.

Name stability is required: renaming a trigger between "PR opened" and "PR
merged" breaks the name-keyed link and produces a duplicate row plus an orphan.
Phase 2 therefore freezes `name` while a proposal is open, and
Phase 4's tracked state enforces it.

## Implementation Phases

### Phase 0 — Visibility (small, unblocks the workflow)

- Add `source`/`managed` to `chetter_list_triggers` output and to the store
  record conversion.
- Add `source` filtering.
- Document the draft-versus-managed lifecycle in `docs/TRIGGERS.md`.

Exit: an operator can list drafts and tell managed triggers apart at a glance.

### Phase 1 — Round-trip renderer and golden test (no new tools)

- Implement `triggerDefFromRecord` and `renderTriggerYAML`.
- Add the round-trip golden test over the real `chetter-config` trigger set,
  with an explicit allowlist for any file that cannot round-trip.
- Add unit coverage for all three inverse transforms: `AGENT_IMAGE_PREFIX`
  resolution, flat-key/`trigger_config` flattening, and per-scope schema-comment
  depth.

Exit: `render(parse(file)) == file` holds for every trigger definition, and the
test fails if any transform drifts.

This phase has no user-visible behavior change and is independently valuable: it
pins the canonical definition shape for the whole repo.

### Phase 2 — Promotion action

- Add `chetter_promote_trigger` with the behavior above.
- Add the secret scan.
- Add name-freeze validation.
- Reuse `chetter_create_definition_proposal`; do not duplicate PR plumbing.

Exit: a draft created through `chetter_create_trigger` can be promoted end to
end, and the resulting PR contains YAML identical to what a maintainer would
have written by hand.

### Phase 3 — Collision guard (`H1`)

- Add the `adopt` opt-in to `schemas/trigger.schema.json` and the parser.
- Refuse un-opted-in adoption of a row with `source_id IS NULL` during sync.
- Emit a `trigger_sync_collision` audit event on both refusal and adoption.

Exit: a name collision cannot silently overwrite a draft, and deliberate
adoption is explicit and audited.

Phase 3 is ordered last only because it touches the sync path, which is worth
landing separately from the feature; it should not ship later than the point
where developers are encouraged to keep long-lived drafts.

### Phase 4 — Tracked promotion state (schema)

- Add the schema per the Data Model section.
- Record the proposal on the trigger row at promotion time.
- Surface promotion state in `chetter_list_triggers` and in proposal status.

Exit: the UI can render "draft / promotion open / managed" without correlating
two lists, and a stale promotion PR is detectable.

## Testing Strategy

- **Round-trip golden test** (Phase 1) — the primary correctness gate, run over
  the real definition set.
- **Inverse-transform unit tests** (Phase 1) — prefix resolution, flat-key
  flattening, and per-scope schema-comment depth, asserted in both directions.
- **Promotion integration test** (Phase 2) — create a draft via the service,
  promote it, assert the rendered file content, and assert the proposal row.
- **Refusal tests** (Phase 2) — already-managed trigger, secret-shaped content,
  existing path, duplicate name, and rename-while-open.
- **Adoption tests** (Phase 3) — collision refusal, audited adoption with
  `adopt: true`, and draft preservation in the refusal path.
- **Identity-preservation regression** (Phase 2) — extend the existing
  `TestSyncDefinitionsPreservesTriggerIdentityAndCronEntry` to start from a
  promoted draft and assert the row `id` and run history survive the sync.
- **Parity** — `make check` plus `make check-sql-parity` once Phase 4 adds
  columns and queries.

## Rollout

Phase 1 and Phase 2 are additive: no existing trigger changes behavior, and the
new tool is inert until called. Phase 3 changes sync behavior on collision, so
it should be announced before release; in practice a collision is already a bug,
and the guard converts silent data loss into a startup-adjacent error.

No migration step is needed for existing rows: drafts already have
`source_id = NULL`, and managed triggers already have the default source ID.

## Risks And Mitigations

| Risk | Mitigation |
|---|---|
| Renderer drifts from hand-authored conventions | Round-trip golden test over the real definition set, with an explicit allowlist |
| A promoted draft silently overwrites another draft | Phase 3 collision guard with audit events; refuse by default |
| Secret material promoted into `chetter-config` | Pre-render secret scan, plus unchanged PR review |
| Promotion resets run history | Name-keyed upsert preserves `id`; regression test asserts it |
| Rename between PR open and merge creates a duplicate | Freeze `name` while a proposal is open |
| Drafts accumulate forever | `source` filtering plus a promotion-candidate view (age, last run, usage) in Phase 4 |
| Round-trip normalizes a file that authors wrote differently | Explicit allowlist entry, reviewed — never a blanket tolerance |

## Out Of Scope

- Editing or deleting Git-managed triggers from the API or UI.
- Promotion of agents, skills, task templates, or MCP endpoints. The same
  pattern will likely apply, but each has a different canonical shape and should
  get its own round-trip analysis.
- Automatic promotion of stale drafts.
- Direct commits to `chetter-config` without a pull request.

## Open Questions

- Should `chetter_promote_trigger` default to a draft PR, so an operator can
  inspect the rendered diff before it is reviewable by others? A draft default
  is friendlier, but it adds a "mark ready" step to every promotion.
- Should a promotion PR that is closed unmerged clear any tracked state, or
  leave the draft running untouched? Leaving it untouched seems right, but it
  needs a decision before Phase 4 records state on the row.
- Should the draft-versus-managed distinction also gate `chetter_update_trigger`
  today? Enforcement is stated as a rule in this plan but not yet implemented,
  and implementing it is a behavior change worth deciding on deliberately.

## Definition Of Done

- `chetter_list_triggers` reports ownership, and drafts are filterable.
- Every trigger definition in `chetter-config` round-trips byte-identically
  through parse and render.
- A draft created through the API can be promoted to a pull request that
  contains hand-authored-equivalent YAML.
- Promotion is refused for managed triggers, secret-shaped content, path
  collisions, and duplicate names.
- A name collision cannot silently overwrite a draft; deliberate adoption is
  opt-in and audited.
- Merging a promotion preserves the trigger's identity and run history.
- `make check` and `make check-sql-parity` pass, with the full test matrix above.
