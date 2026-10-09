---
artifact_contract: ce-unified-plan/v1
artifact_readiness: design-review
execution: code
product_contract_source: "operator request for the user-facing shape of the trigger experiment loop; implements docs/plans/2026-10-08-001-feat-trigger-promotion-plan.md (Phase 2 shipped)"
title: "ux: the trigger experiment and promotion loop over MCP and in the Web UI"
date: 2026-10-09
---

# The Trigger Experiment And Promotion Loop: User-Facing Shape

## Goal

Describe how a developer actually experiences the "experiment in the database,
promote to Git" workflow, over both MCP and the Web UI, and identify what is
already there versus what still needs building.

This is a design proposal, not an implementation plan. The mechanism
(phases 0-3 of `2026-10-08-001-feat-trigger-promotion-plan.md`) is largely
shipped; this document is about the surface a user touches.

## What Already Exists

Worth stating first, because it shrinks the remaining work substantially:

**Web UI today** (`web/src/routes/triggers/`):

- The `Trigger` proto already carries `source_id`, `source_repo_url`,
  `source_branch`, and `source_path`.
- The list page marks Git-managed triggers with a `git` badge that links to the
  definition file on GitHub, and disables the enabled toggle for them.
- The detail page shows a `git-managed` badge and disables both the enabled
  toggle and the Delete button.
- Both pages key off `isGitManaged(trigger) { return !!trigger.sourceId }`.

So the "Git owns this row" half of the model is **already visible and already
enforced in the UI**. What the UI does *not* have is the draft half: no
"promote" action, no explicit draft affordance, and no promotion status.

**MCP today** (after the shipped phases):

- `chetter_create_trigger` creates a draft (`source_id NULL`).
- `chetter_run_trigger_now` / `chetter_update_trigger` iterate on it.
- `chetter_list_triggers` now returns `source` (`"config"`/`"database"`),
  `managed`, `source_id`, and `source_path`, and accepts a `source` filter.
- `chetter_promote_trigger` renders canonical YAML and opens a proposal PR,
  with `dry_run` for preview.

**ConnectRPC today**:

- `TriggerService.PromoteTrigger` exists (added alongside this proposal), so the
  Web UI can reach the same service method. It is a thin adapter: rendering,
  validation, the secret scan, and the proposal call all live in
  `Service.PromoteTrigger`, shared with the MCP tool.

### How the two surfaces relate

MCP and ConnectRPC are **two adapters over the same `Service` methods**. Every
piece of trigger logic lives in `Service`, once: `h.svc.CreateTrigger` and
`svc.CreateTrigger` are the same function, as are `ListTriggers`,
`GetTriggerByName`, and now `PromoteTrigger`.

What is not shared is the *exposure*: an MCP tool needs a registration plus a
`xxxTool` method, and a ConnectRPC RPC needs a proto message pair plus a
handler. Neither is generated from the other, so a new `Service` method reaches
MCP only until someone writes the proto and handler. That adapter is small and
mechanical — but it is real work, and it is why a feature can be MCP-only for a
while.

That gap is now closed for promotion. The remaining UI work below is purely
frontend.

## The Loop, As A User Sees It

```text
1. create          chetter_create_trigger            -> draft, source: database
2. iterate         chetter_run_trigger_now           -> runs like any trigger
                   chetter_update_trigger            -> tweak prompt/schedule
                   (repeat, no PR, no review, no wait)
3. preview         chetter_promote_trigger{dry_run}  -> exact YAML + path
4. promote         chetter_promote_trigger           -> definition proposal PR
5. review          normal PR review                  -> human gate
6. merge           definitions sync                  -> source: config
7. confirmation    chetter_list_triggers             -> managed, source_path set
```

The important property: steps 1-2 have **no PR in the loop at all**, and the
draft keeps running on its schedule throughout step 5. Nobody waits on review to
keep experimenting.

## MCP Surface

### Discovering drafts

```jsonc
// chetter_list_triggers {"source": "database"}
{"triggers": [
  {"name": "nightly-reindex", "source": "database", "managed": false,
   "source_id": "trg_ab12…", "enabled": true, "cron_expr": "0 3 * * *"}
]}
```

The `source` filter is the practical replacement for a dedicated "experimental
triggers" concept: it is how you answer "what have we got that isn't in Git
yet?" — the question that makes the loop usable.

### Promoting

```jsonc
// chetter_promote_trigger {"name": "nightly-reindex", "scope": "repo",
//                          "target_repo": "flatout-works/chetter", "dry_run": true}
{
  "trigger_name": "nightly-reindex",
  "path": "repos/flatout-works/chetter/triggers/nightly-reindex.yaml",
  "content": "# yaml-language-server: $schema=../../../../../…\nname: …\n",
  "dry_run": true
}
```

`dry_run` returning the rendered bytes is the key usability affordance: the user
sees the exact file and its path *before* a branch or PR exists. Re-running
without `dry_run` opens the PR and returns `proposal.pr_url`.

### Error messages as the teaching surface

Because the MCP caller is usually an agent, errors carry much of the UX. The
shipped messages are written to say what to do next:

| Situation | Message shape |
|---|---|
| Promoting a managed trigger | "trigger `x` is already managed by definitions source `defs_default`; edit its definition file instead of promoting it" |
| Secret-shaped content | "rendered definition contains credential-shaped content and was not submitted: line 12: … Remove the secret from the trigger, or re-run with `allow_secret` after reviewing it" |
| Name already in Git | "a trigger definition named `x` already exists at `global/triggers/x.yaml`" |
| Path taken | "definition file `…` already exists; update it directly instead of promoting over it" |
| Sync collision (Phase 3) | "trigger definition `x` would overwrite the database-created trigger of the same name; rename one of them, or add `adopt: true`…" |

## Web UI Surface

### Triggers list

Current, plus a draft affordance. The list already distinguishes managed
triggers with a `git` badge; a draft needs the mirror-image signal:

```text
NAME                          TYPE      ENABLED   SOURCE
nightly-reindex  [draft]       cron      [x]      database
  ↑ amber outline badge; hover: "Not in Git yet — promote to keep it"
changelog-update [git] ↗       cron      [x]      repos/flatout-works/chetter
  ↑ existing gray badge, links to the definition file
```

Add `Source` filtering to the existing filter row so "database" is one click,
mirroring the MCP `source` filter.

### Trigger detail page

The detail page already disables edit/delete for managed triggers. For a draft,
add a promotion panel alongside the existing status badges:

```text
[StatusBadge: cron] [StatusBadge: enabled] [Badge: draft]

┌ Promote to Git ──────────────────────────────────────────┐
│ This trigger runs from the Chetter database only.        │
│ Promoting it adds a definition file and opens a PR.      │
│                                                          │
│ Scope   ( ) Global  (•) Repository  ( ) Team             │
│ Repo    [flatout-works/chetter        ▾]                 │
│ File    repos/flatout-works/chetter/triggers/nightly-…   │
│                                                          │
│                        [Preview YAML]  [Promote…]        │
└──────────────────────────────────────────────────────────┘
```

"Preview YAML" calls `dry_run` and opens a `<Modal>` showing the rendered
content with the file path — the same bytes the PR will contain. "Promote…"
opens a confirmation `<Modal>` stating that merging the PR will keep the row's
id and run history, then creates the proposal and replaces the panel with:

```text
┌ Promotion open ──────────────────────────────────────────┐
│ PR #483 — config: promote trigger nightly-reindex        │
│ Opened 2 minutes ago. This trigger keeps running until   │
│ the PR merges.                                           │
│                              [View PR ↗]   [Retry]       │
└──────────────────────────────────────────────────────────┘
```

All of this must use Flowbite components per `AGENTS.md` — `<Card>`,
`<Select>`, `<Button>`, `<Modal>`, `<Badge>`, `<Alert>`, `<Spinner>` — and a new
`<PromotePanel>`-style extraction is preferable to growing the detail page.

### Failure states the UI must show

- **Secret scan refused** — `<Alert color="red">` naming the offending lines,
  with the `allow_secret` path described rather than offered as a default.
- **Already managed** — the panel must not render at all for managed triggers;
  the existing `git-managed` badge already covers that case.
- **Collision** — an `<Alert color="yellow">` explaining the name is taken.

### The sync collision, surfaced proactively

Phase 3 makes a definition-vs-draft name collision fail the sync. Today that
surfaces as a sync error, which is correct but late. Cheap improvement: the
Triggers page can warn earlier by comparing draft names against names present
in the definitions registry, and badge a draft `[draft · name conflict]` before
anyone lands a colliding file. This is a nice-to-have, not a requirement.

## What Still Needs Building

| Surface | Status |
|---|---|
| MCP `source`/`managed`/`source_path` on `chetter_list_triggers` | **shipped** |
| MCP `source` filter | **shipped** |
| MCP `chetter_promote_trigger` (+ `dry_run`) | **shipped** |
| MCP secret scan and refusal messages | **shipped** |
| ConnectRPC `TriggerService.PromoteTrigger` | **shipped** |
| UI: `git` badge, disabled toggle/delete for managed | **already existed** |
| UI: draft badge + `Source` filter | **to build** |
| UI: promote panel, YAML preview, promotion-open state | **to build** |
| Phase 4 promotion state on the trigger row | **to build** |

Everything on the server side now exists; the remaining work is frontend, plus
the Phase 4 schema for authoritative promotion state.

### The one server-side decision worth revisiting

Refusals return `FailedPrecondition`, so the UI can distinguish "you can fix
this" (already managed, secret-shaped content, name taken) from a genuine
server fault. If a future refusal is not user-correctable it should not reuse
that code.

### Why Phase 4 matters for the UI

Without `promotion_state` / `promotion_pr_number` on the `triggers` table, the
detail page cannot render "Promotion open" without correlating the trigger
against `definition_change_proposals` on every page load. A promotion PR that
was closed unmerged also leaves no trace. Phase 4 makes the panel state
authoritative rather than inferred, and it is the same schema change the plan
already specifies.

## Proposed Build Order

1. **UI draft badge + `Source` filter** — small, immediately useful, no new RPC.
2. **UI promote panel with YAML preview** — the core affordance; the RPC it
   needs already ships.
3. **Phase 4 schema + promotion-open state** — makes the panel authoritative
   rather than inferred.

Steps 1-2 deliver the loop visually end to end. Step 3 improves fidelity.

## Deliberate Non-Goals

- **No "promote automatically" or background promotion.** The whole point is
  that a human decides when a draft becomes permanent.
- **No editing of managed triggers in the UI.** Git stays authoritative; the
  existing disabled controls are the correct behavior, not a limitation to fix.
- **No draft-specific retention or expiry policy** in this proposal. If drafts
  accumulate, that is a separate feature with its own design.
- **No new MCP tools.** `chetter_promote_trigger` plus the existing trigger
  tools cover the loop.

## Open Questions

- Should the promote panel default to the trigger's own `git_url` repo when the
  draft has one, rather than asking for scope and repo every time? This would
  remove two of the four form fields in the common case.
- Should a draft be visually separated into its own list section ("Not yet in
  Git") rather than distinguished only by a badge? Clearer, but it complicates
  sorting and pagination.
- Should closing a promotion PR clear the promotion-open state, or leave the
  draft running untouched? The plan leaves this open; the UI answer affects
  whether the panel needs a "Promotion closed" state.
