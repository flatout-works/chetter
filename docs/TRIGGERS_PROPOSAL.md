# Triggers: Future Work

The unified trigger system described in the original proposal has been fully implemented. This document tracks remaining items not yet shipped.

## Implemented

- Generalized `chetter_schedules` table with `trigger_type` and `trigger_config` JSON column (Option A)
- `chetter_create_trigger` / `update_trigger` / `list_triggers` / `delete_trigger` / `run_trigger` MCP tools
- Old schedule tools removed in a single breaking change
- DB-backed PR review dispatch: webhook handler queries `pr_review` triggers by repo
- Hardcoded reviewer config removed from `main.go`
- `team_id` auto-stamping and scoping on triggers
- Multiple PR review triggers per repo allowed
- `trigger_type` shown in list output
- `chetter-review` label only added when a trigger actually fires (no file-pattern auto-review)
- Empty prompt allowed for `pr_review` triggers (falls back to built-in review template)

## Not yet implemented

### Additional trigger types

The `trigger_type` column supports arbitrary values. New types can be added by:

1. Adding the type string constant to `internal/store/`.
2. Adding type-specific validation in `Service.CreateTrigger` / `UpdateTrigger`.
3. Adding a dispatcher that watches for the trigger condition and calls `SubmitTask`.

Candidates:

| trigger_type | When it fires | Required fields |
|---|---|---|
| `schedule_release` | On a GitHub release event | `repo` |
| `linear_ticket` | On a Linear webhook event | `team_id` (Linear), `project_id` |
| `sentry_alert` | On a Sentry alert webhook | `project_slug` |

### Multi-repo per trigger

Each PR review trigger currently covers exactly one repo (`trigger_config.repo` is a single string). To support a trigger that watches multiple repos, `repo` could become a JSON array (e.g. `["flatout-works/chetter", "flatout-works/other"]`). This requires updating the DB query in `ListEnabledPRReviewTriggersByRepo` to check JSON containment instead of string equality.
