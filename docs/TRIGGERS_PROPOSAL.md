# Proposal: Unified "Trigger" Concept for Tasks

## Problem

Chetter currently has two parallel mechanisms for producing tasks:

1. **Cron schedules** — registered via `chetter_schedule_task`, persisted in `chetter_schedules`, fired by a cron engine in-process.
2. **GitHub PR webhooks** — hardcoded into `buildWebhookHandler` in `main.go`. The reviewer agent name (`"pr-reviewer"`), provider (`"opencode-go"`), model (`"minimax-m3"`), and timeout (`3600`) are hardcoded string literals.

The hardcoded reviewer config in `main.go:180-183` makes it impossible to:

- Run a second reviewer alongside the default (e.g. one for code, one for security).
- Switch models/agents without redeploying the server.
- Review multiple repos with different reviewer configurations.
- Audit/list which review configs are active.

We previously considered putting these in env vars, but that has the same limitation as the hardcoded values: it conflates "how to configure the *system*" with "how to configure a *particular review job*". Reviewer configs are user data, not infrastructure config — they should live in the same place as cron schedules.

## Proposal: Generalize Schedules to "Triggers"

Rename and generalize the schedule concept. A **trigger** is a named, persistent configuration that knows how to produce tasks. The trigger type determines *when* it fires; everything else (prompt, agent, model, repo, env, timeout) is shared.

### New model: `chetter_triggers`

A new table (or a generalized `chetter_schedules` table) with a `trigger_type` column:

| trigger_type | When it fires | Required fields |
|---|---|---|
| `cron` | On a cron schedule | `cron_expr` |
| `pr_review` | On a `pull_request` or `issue_comment` event matching the repo | `repo` (e.g. `flatout-works/chetter`) |

Other trigger types can be added later (`schedule_release`, `linear_ticket`, `sentry_alert`, etc.) without changing the table shape — only the `trigger_type`-specific validation rules and the dispatcher code.

### Sharing the rest

All non-trigger-type fields are identical between cron and PR review triggers:

- `prompt` — the task body sent to the agent
- `git_url`, `git_ref`
- `agent_image`, `agent`, `provider_id`, `model_id`, `variant_id`
- `skills`
- `timeout_sec`
- `enabled`

This is a clean generalization. The current schedule data maps to `trigger_type = 'cron'` 1:1.

### Storage

Two options:

**Option A — Single generalized table.** Add a `trigger_type` column and a `trigger_config` JSON column to `chetter_schedules` (or rename to `chetter_triggers`). Old rows get `trigger_type = 'cron'` and an empty `trigger_config`. New PR review triggers get `trigger_type = 'pr_review'` and `trigger_config = {"repo": "..."}`.

**Option B — Separate tables.** Keep `chetter_schedules` as-is. Add `chetter_pr_review_configs` with the same shape plus the PR-specific columns. A unified view in the service layer joins them for listing.

I recommend **Option A**. It avoids the join pattern at the DB layer, lets the dispatcher iterate one table to find all live triggers, and makes "list all triggers" a single query. The JSON column carries the type-specific data.

### MCP tools

Replace the schedule tools with a single unified set:

| New tool |
|---|
| `chetter_create_trigger` |
| `chetter_update_trigger` |
| `chetter_list_triggers` |
| `chetter_delete_trigger` |
| `chetter_run_trigger` |

The old `chetter_schedule_task` / `chetter_update_schedule` / `chetter_list_schedules` / `chetter_delete_schedule` / `chetter_run_schedule` tools are removed in the same change. No backward-compatibility aliases.

The new `create_trigger` input has:

```json
{
  "name": "deep-pr-review",
  "trigger_type": "pr_review",
  "repo": "flatout-works/chetter",
  "prompt": "You are performing a deep code review...",
  "agent": "pr-reviewer",
  "provider_id": "opencode-go",
  "model_id": "minimax-m3",
  "timeout_sec": 3600
}
```

For a cron trigger, `repo` is omitted and `cron_expr` is set instead. Each trigger is identified by `name` (unique per team, as schedules are today).

### Webhook dispatch

`buildWebhookHandler` in `main.go` no longer carries the reviewer config. Instead:

1. On startup, the service loads all enabled triggers with `trigger_type = 'pr_review'` from the DB.
2. The webhook handler, on each event, looks up matching triggers by `repo` and submits a task per match.

This replaces the in-memory `map[string]cfg` with a DB-backed registry. Triggers can be created/updated at runtime; the dispatch logic is generic.

### Disabling the reviewer system

The kill switch stays at the GitHub config level: if `GITHUB_WEBHOOK_DISABLED=true` or the GitHub App isn't configured, the webhook route simply isn't registered. Individual triggers are disabled via `enabled=false`.

### Auth and scoping

Triggers get `team_id` auto-stamping the same way schedules do. Team-scoped tokens see only their team's triggers on list.

## Why not just use env vars for this?

- The user wants to set the reviewer agent at runtime, not at deploy time. The current schedule system already supports this pattern; PR review should follow the same pattern.
- Multiple reviewers per server should be possible. With one global env var you can only have one configuration; with DB-backed triggers you can have N.
- The same `prompt` + `agent` + `model` + `timeout` machinery that schedules use would be reinvented as "env-var-loaded config" if we went the env-var route. Reusing the table and the dispatcher avoids that duplication.

## Migration plan (when approved)

1. Add `trigger_type` and `trigger_config` columns to `chetter_schedules`. Backfill existing rows with `trigger_type='cron'` and empty `trigger_config`.
2. Refactor `Service.CreateSchedule` to `Service.CreateTrigger` internally, validating the trigger_type-specific required fields.
3. Add the dispatcher: on PR webhook events, query triggers with `trigger_type='pr_review'` and `trigger_config->>'repo' = $repo`, submit a task per match.
4. Remove the hardcoded values from `main.go:180-183`. Webhook config now only carries the GitHub App connection details.
5. Replace the schedule MCP tools with the new trigger tools in a single breaking change.
6. Add tests for trigger validation per type, dispatcher matching, and the existing schedule flow.

## Decisions

- **Multiple triggers per repo: allowed.** A single repo can have any number of PR review triggers — useful for "deep review" vs "quick lint" against the same code. Repo is not unique per trigger; only `name` is unique.
- **List endpoint shows `trigger_type`** as a column. Distinguishes cron from PR review at a glance; the dispatcher can filter on it.
- **No backward-compat aliases.** The schedule tools are removed in the same change as the new trigger tools ship.
- **Multi-repo per trigger: not in v1.** Each PR review trigger covers exactly one repo. The `repo` field is a single string.
