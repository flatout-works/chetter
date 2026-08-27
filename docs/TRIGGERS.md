# Chetter Triggers

Chetter automates work through a unified trigger system. All trigger types are
created and managed with the same trigger tools and can be defined either
dynamically (MCP/API/web UI) or as YAML in the definitions repo.

Supported trigger types:

| Type | What it does |
|---|---|
| `cron` | Submits a task on a recurring schedule (also called a schedule). |
| `pr_review` | Submits a review task when a matching GitHub pull request event arrives. |
| `issue` | Submits a triage/response task when a matching GitHub issue event arrives. |

Trigger tools:

- `chetter_create_trigger`
- `chetter_update_trigger`
- `chetter_list_triggers`
- `chetter_delete_trigger`
- `chetter_run_trigger`
- `chetter_list_trigger_runs`

This document covers cron schedules and PR review triggers. Issue triggers use
the same trigger tools with `trigger_type: issue` and the webhook environment
variables listed in [IMAGES.md](IMAGES.md#trigger-type-environment-variables).
Trigger definitions synced from Git are covered in
[CONFIGURATION.md](CONFIGURATION.md#trigger-definitions).

---

# Cron Schedules

A schedule is a **persisted task template** with a cron expression. On each cron
fire, Chetter:

1. Creates a new task from the template (same prompt, git_url, git_ref, agent_image, etc.)
2. Stamps it with the team's `team_id` (if the schedule was created with a scoped token)
3. Queues it as a pending task for runners to claim
4. Records the run in `trigger_runs`

## Schedule Fields

| Field | Required | Default | Description |
|---|---|---|---|
| `name` | Yes | — | Unique schedule name. Used as the stable identifier for updates and deletion. |
| `cron_expr` | Yes | — | 5-field cron expression (e.g. `0 4 * * *`) or descriptor (`@hourly`, `@daily`). Parsed by `robfig/cron` in UTC. |
| `prompt` | Yes | — | The task prompt. Executed by the runner's configured harness on each fire. |
| `git_url` | No | — | Repository URL to clone before running the task. |
| `git_ref` | No | `main` | Branch, tag, or commit to check out. |
| `agent_image` | Yes | — | Runner Docker image. Falls back to `DEFAULT_AGENT_IMAGE` if omitted and configured. |
| `agent` | No | — | Agent definition name (e.g. `changelog-maintainer`). |
| `harness` | No | — | Runner harness: `opencode`, `claude-code`, `pi`, `codewhale`, or `codex`. Defaults to the runner's `execution.harness` config. |
| `provider_id` | No | — | LLM provider (e.g. \`opencode\`). |
| `model_id` | No | — | LLM model (e.g. `deepseek-v4-pro`). |
| `variant_id` | No | — | Model variant (e.g. `high`, `minimal`). |
| `skills` | No | `[]` | Array of skill names passed to the runner. |
| `timeout_sec` | No | `600` | Task timeout in seconds. Defaults to `DEFAULT_TASK_TIMEOUT_SEC`. |
| `enabled` | No | `true` | Whether the schedule is active. Disabled schedules do not fire. |

## Managing Cron Schedules

### Create a Schedule

**Tool:** `chetter_create_trigger` (with `trigger_type: cron`)

Example input:

```json
{
  "name": "nightly-docs-update",
  "trigger_type": "cron",
  "cron_expr": "0 4 * * *",
  "prompt": "Review recent repository changes and update documentation...",
  "git_url": "https://github.com/flatout-works/chetter",
  "git_ref": "main",
  "agent_image": "ghcr.io/flatout-works/chetter-runner:main",
  "agent": "docs-maintainer",
  "timeout_sec": 3600
}
```

### List Schedules

**Tool:** `chetter_list_triggers` (with `trigger_type: cron`)

- `trigger_type: "cron"` — returns only cron schedules
- `enabled_only: true` — returns only enabled triggers

### Run a Schedule Immediately

**Tool:** `chetter_run_trigger`

```json
{"name": "nightly-docs-update"}
```

This submits one task from the cron trigger right now, without waiting for the cron expression.

### Update a Schedule

**Tool:** `chetter_update_trigger`

```json
{
  "name": "nightly-docs-update",
  "cron_expr": "0 5 * * *",
  "enabled": false
}
```

Only provided fields are changed. The trigger is re-registered in the cron runner after update.

### Delete a Schedule

**Tool:** `chetter_delete_trigger`

```json
{"name": "nightly-docs-update"}
```

## Cron Expression Reference

Schedules run in **UTC**. The parser supports standard 5-field cron and descriptors:

| Descriptor | Meaning |
|---|---|
| `@yearly` / `@annually` | Once per year, midnight Jan 1 |
| `@monthly` | Once per month, midnight first day |
| `@weekly` | Once per week, midnight Sunday |
| `@daily` / `@midnight` | Once per day, midnight |
| `@hourly` | Once per hour, minute 0 |

Standard 5-field format: `min hour dom month dow`

Examples:
- `0 4 * * *` — daily at 04:00 UTC
- `0 */6 * * *` — every 6 hours
- `30 8 * * 1` — Monday at 08:30 UTC

## Schedule Runs

Each time a schedule fires, a row is created in the `trigger_runs` table:

| Column | Description |
|---|---|
| `schedule_id` | The parent schedule |
| `task_id` | The task created by this run |
| `status` | Run status (submitted, etc.) |
| `scheduled_for` | The nominal cron fire time |

This lets you trace which task was created by which schedule fire.

## Schedule Tips

- Use `enabled: false` to pause a schedule without deleting it.
- Update `cron_expr` to change the schedule without losing the rest of the template.
- Schedules created with a team-scoped token only appear in `chetter_list_triggers` for that team.
- The `next_run_at` field is computed and updated automatically after each activation.
- If a schedule's name already exists, `chetter_create_trigger` will fail — use `chetter_update_trigger` instead.

---

# PR Review Automation

Chetter provides automated code review on pull requests via a GitHub webhook
integration. Reviews use a dedicated `pr-reviewer` agent running in the Chetter
runner fleet.

## Architecture

```
GitHub PR event
       │
       ▼
POST /webhook/github
       │
       ├─ Respond 200 immediately (process async in goroutine)
       │
       ▼
Verify HMAC-SHA256 signature
       │
       ├─ Invalid → log + 401
       │
       ▼
Check X-GitHub-Delivery (replay/dedup protection)
       │
       ▼
Route by event type:
  ├─ pull_request (opened/synchronize/reopened/labeled)
  │     ├─ Evaluate eligibility (label or fork)
  │     ├─ If eligible:
  │     │     ├─ Select client from signed installation.id
  │     │     ├─ Look up matching PR review triggers in DB
  │     │     ├─ Auto-add chetter-review label (after trigger match, skip if label-triggered)
  │     │     └─ Submit one review task per matching trigger
  │     └─ If not → ignore
  │
  └─ issue_comment (created)
        ├─ If body == "/chetter-review" AND commenter has write access:
        │     ├─ Post acknowledgment comment
        │     ├─ Look up matching PR review triggers in DB
        │     ├─ Auto-add chetter-review label
        │     └─ Submit one review task per matching trigger
        └─ Otherwise → ignore
```

### Review Flow

```
GitHub              Chetter                                Runner             OpenCode
  │                   │                                      │                  │
  │──PR event────────▶│                                      │                  │
  │                   │──200 OK                              │                  │
  │                   │──verify sig                          │                  │
  │                   │──dedup check                         │                  │
  │                   │──select signed installation          │                  │
  │                   │──query DB triggers for repo          │                  │
  │                   │──add label (if not label-triggered)  │                  │
  │                   │──SubmitReviewTask()                  │                  │
  │                   │◀──────────────ConnectRPC claim───────│                  │
  │                   │                                      │──start container▶│
  │                   │                                      │──git clone       │
  │                   │                                      │──gh pr view      │
  │                   │                                      │──review changes  │
  │                   │                                      │──review MCP tool─│──▶ GitHub
  │                   │                                      │                  │
  │                   │                    │◀─status: done──│                  │
```

## Trigger Paths

Repo-level filtering is done at trigger level: a trigger's `trigger_config->>'$.repo'` selects which GitHub repo it watches. The webhook handler queries for matching enabled triggers at event time. If no enabled PR review trigger exists for a repo, the webhook event is ignored (no review is submitted).

### 1. Label (`pull_request` event)

PR has the `chetter-review` label applied. Evaluated on all watched PR actions (`opened`, `synchronize`, `reopened`, `labeled`). For the `labeled` action specifically, only the `chetter-review` label triggers — other labels are ignored.

- **Checked in:** `shouldReview()` — scans `ev.PullRequest.Labels`
- **Auto-labeling:** When a review is triggered by fork or comment, Chetter auto-adds the `chetter-review` label in `submitReview` after confirming at least one matching trigger exists, so the label always indicates a review task was actually submitted. Label-triggered reviews skip auto-labeling.

### 2. Fork (`pull_request` event)

PR comes from an external fork (head repo full name differs from base repo). Automatic review for outside contributors.

- **Checked in:** `shouldReview()` — compares `ev.PullRequest.Head.Repo.FullName` to `repo`

### 3. Comment (`issue_comment` event)

A user with **write access** to the repo posts `/chetter-review` on a PR.

- **Action filter:** `created` only
- **Anti-abuse:** requires write access via `CheckUserHasWriteAccess()` (collaborator or team member with push/triage/admin permissions). The Chetter App's own bot login bypasses this gate — GitHub's collaborators API does not grant "write" to App bots (they get permissions through the installation).
- **Acknowledgment:** Posts a comment `@user requested a review — Chetter is on it.` to the PR
- **Auto-labeling:** Adds the `chetter-review` label via `submitReview` when at least one matching trigger is found

### 4. Manual Task Submission (MCP tool)

Anyone with `chetter_submit_task` access can submit a review directly. This bypasses the webhook entirely — no label, no fork check, no file patterns, no comment parsing, no write-access check.

To manually trigger a review via the MCP tool, craft a task with:
- `agent`: `pr-reviewer` (or another reviewer agent)
- `git_url`/`git_ref`: the PR branch to review
- `env`: set `PR_NUMBER` and `GITHUB_REPO` for the agent's review procedure

## Configuring PR Review Triggers

PR reviews are configured via **triggers** — the same mechanism used for cron schedules. A trigger with `trigger_type=pr_review` tells Chetter to watch a specific GitHub repository via the webhook.

### Creating a PR Review Trigger

Use the `chetter_create_trigger` MCP tool:

```json
{
  "name": "deep-pr-review",
  "trigger_type": "pr_review",
  "repo": "flatout-works/chetter",
  "prompt": "You are performing a deep code review...",
  "agent": "pr-reviewer",
  "provider_id": "opencode",
  "model_id": "minimax-m3",
  "timeout_sec": 3600
}
```

Required fields for `pr_review` triggers:
- `name` — unique trigger name
- `trigger_type` — must be `pr_review`
- `repo` — full repository name (e.g. `flatout-works/chetter`)
- `agent` — agent definition name (e.g. `pr-reviewer`)

Optional fields:
- `prompt` — instructions sent to the agent; falls back to the built-in review template if omitted
- `agent_image` — runner harness image; falls back to `DEFAULT_AGENT_IMAGE` if omitted

### Multiple Triggers Per Repo

Multiple PR review triggers for the same repo are allowed. Each trigger submits a separate review task when a matching PR event arrives. Useful for running different agents (e.g. "deep code review" + "security review") on the same PRs.

## Webhook Configuration

### Environment Variables

| Env Var | Purpose | Required |
|---|---|---|
| `GITHUB_APP_ID` | GitHub App ID | Yes |
| `GITHUB_APP_PRIVATE_KEY_B64` | PEM private key, base64-encoded | Yes |
| `GITHUB_WEBHOOK_SECRET` | HMAC-SHA256 secret for signature verification | Yes |
| `GITHUB_WEBHOOK_DISABLED` | `true` to disable the webhook (kill switch) | No |
| `GITHUB_INSTALLATION_ID` | Deprecated single-installation fallback | No |

No reviewer-specific env vars are needed — agent, model, and timeout come from the trigger configuration in the database.

### Route Registration

```go
wh := webhook.NewHandler(cfg, svc)
mux.Handle("/webhook/github", wh)
```

The webhook handler is registered outside the MCP auth middleware — HMAC signature is its own authentication.

## GitHub App: Chetter

Install the same App on every organization or user account whose repositories
Chetter automates. Signed webhook payloads select the installation for event
handling; manual and scheduled tasks resolve it from `owner/repo`. Installation
tokens are cached per installation and task Git credentials are restricted to
the task repository.

### Required Permissions

| Permission | Access | Purpose |
|---|---|---|
| Pull requests | Read & Write | Post reviews, approve, request changes |
| Issues | Read & Write | Read linked issues, comment for `/chetter-review` |
| Contents | Read | Read repo files for review context |

### Subscribed Events

- `pull_request` (opened, synchronize, reopened, labeled)
- `issue_comment` (created)

## Deduplication

An in-memory set of recent `X-GitHub-Delivery` IDs prevents duplicate processing if GitHub retries a webhook delivery. Entries expire after 5 minutes (configurable). Not persisted across restarts — acceptable since GitHub does not redeliver after a crash.

## Manually Testing External-Event Triggers (Web UI)

`pr_review` and `issue` triggers fire from GitHub webhook events, so there is no
cron expression or "Run Now" path to exercise them. The trigger detail page
(`/triggers/{name}`) therefore shows a **Test Trigger** action for these types.

A test run:

1. Collects the minimum event context in a per-type form: repository,
   event/action, and PR or issue number (plus optional simulated labels for
   issue triggers with `match_labels`).
2. Resolves the repository installation and fetches **authoritative metadata
   from GitHub** — PR base/head refs and head clone URL, or issue
   title/body/labels — rather than trusting editable branch/ref fields.
3. Reuses the same trigger dispatch path as a real webhook delivery: the
   trigger must be enabled and configured for the repo, the simulated event
   must match the trigger's configured `event`, and `match_labels` must match
   the simulated labels (or the issue's actual GitHub labels when none are
   given). Disabled triggers, repo mismatches, event mismatches, and label
   mismatches all fail with a clear error.
4. Submits the task with the trigger's exact task configuration (prompt,
   agent, model, skills, timeout, session mode, isolation), stamps it with
   `submission_source = trigger_test`, and records a `trigger_test_run` audit
   event so test-originated work is distinguishable from real deliveries.

The resulting task IDs are shown in the UI and link to the task pages; the
same tasks appear in the trigger's Recent Runs table.

### Relationship to `chetter_submit_task` and `chetter_run_trigger`

- `chetter_submit_task` is for **direct tasks** — it bypasses the webhook
  entirely and does not model external-event context (PR refs, issue labels,
  trigger matching).
- `chetter_run_trigger` covers **cron triggers only** (it runs the trigger's
  template immediately).
- The Test Trigger flow exists specifically to test **external-event trigger
  wiring** (matching, task configuration, and end-to-end dispatch) against a
  real GitHub PR or issue without waiting for a new webhook event.

## Error Handling

If the review task submission fails:
1. Chetter posts a comment to the PR: "Chetter could not start the review. Check chetter logs."
2. The error is logged with repo, PR number, trigger name, and trigger reason.

If no PR review triggers are configured for a repo, the webhook ignores the event entirely. If the GitHub API call to list PR files fails (in the file-pattern trigger), the PR is skipped.

## Disable / Kill Switch

Set `GITHUB_WEBHOOK_DISABLED=true` — the handler returns 200 to all webhooks without processing. Business as usual for scheduled tasks and cron triggers; only webhook-triggered events stop.

---

# Event Callbacks

Event callbacks complement triggers: instead of **submitting tasks** from
external events, they **react to task lifecycle events** that happen inside
Chetter. A callback watches a task event type and fires one of three actions.
Callbacks are stored in the database (not in the definitions repo) and can be
global or team-scoped. Callback failures are logged; there is no delivery queue
or dead-lettering for callback actions.

## Event Types And Actions

| Field | Values |
|---|---|
| `event_type` | Any task event type, e.g. `task.started`, `task.completed`, `task.failed`, or a wildcard prefix like `task.completed.*`. Matched exactly or by prefix when it ends in `.*` |
| `action_type` | `create_task`, `webhook`, or `slack` |
| `action_config` | JSON object — shape depends on the action |

Actions:

- **`create_task`** — spawns a new Chetter task. Config: `{"prompt": "...", "git_url": ..., "env": {...}}`. The prompt is rendered as a Go text/template with event fields (`.TaskID`, `.EventType`, `.Status`, `.Summary`, `.Error`, `.Payload`, …), and the spawned task's env gets `CHETTER_EVENT_ID`, `CHETTER_EVENT_TYPE`, and `CHETTER_EVENT_TASK_ID`. Spawns guarded by the recursion limit (`CHETTER_CALLBACK_MAX_DEPTH`, default 5): each spawned task records its parent and depth, and a chain exceeding the limit is rejected with an `event_callback_recursion_limit` error (the callback itself stays enabled).
- **`webhook`** — POSTs (or a configured method) to a URL with rendered JSON body, custom headers, and template support. Non-2xx responses fail the dispatch attempt.
- **`slack`** — posts to a Slack incoming webhook URL using the same webhook machinery.

## Managing Callbacks

| Tool | Purpose |
|---|---|
| `chetter_create_event_callback` | Create a callback (`name`, `event_type`, `action_type`, `action_config`). |
| `chetter_update_event_callback` | Update fields of a callback by name. |
| `chetter_list_event_callbacks` | List callbacks, optionally filtered by enabled state and event type. |
| `chetter_delete_event_callback` | Delete a callback by name. |

The web UI also has an event callbacks page for administration.

# Trigger Definitions In Git

Trigger definitions live as YAML files under `global/triggers/`, `groups/<team-name>/triggers/`, or `repos/<owner>/<repo>/triggers/` in the config repo set via `DEFINITIONS_REPO`. They are auto-synced into the database on startup and on `chetter_sync_definitions`. See [CONFIGURATION.md](CONFIGURATION.md) for the full architecture and the [trigger sync identity rules](CONFIGURATION.md#trigger-sync-identity-and-renames).

Example trigger definition:

```yaml
name: issue-bug-triage
enabled: true
trigger_type: issue
repo: flatout-works/chetter
event: opened
match_labels:
  - bug
git_url: https://github.com/flatout-works/chetter
git_ref: main
agent: issue-triage
harness: opencode
timeout_sec: 1800
session_mode: none
prompt: |-
  Triage the issue and comment with next steps.
```

## Included Schedules

The Chetter repository ships these schedules in its config:

| Schedule | Cron | Purpose |
|---|---|---|
| `chetter-nightly-vulnerability-scan` | `0 1 * * *` | Scan Go deps + Docker images for vulnerabilities, create PR with safe fixes |
| `chetter-nightly-changelog-update` | `0 3 * * *` | Update `CHANGELOG.md` from recent commits |
| `chetter-nightly-docs-update` | `0 4 * * *` | Update project documentation to match implementation |
| `chetter-nightly-website-presentation-update` | `0 5 * * *` | Update marketing website and architecture presentation |
| `next-feature-creator` | `*/30 * * * *` | Analyze repo and create GitHub issue for next feature/fix |

### Vulnerability Scan Schedule

The vulnerability scan is the most detailed schedule. It:

1. Checks for existing open security PRs (avoids duplicates)
2. Calls Arcane MCP tools to scan Docker images
3. Runs `govulncheck` and `osv-scanner` on all Go modules
4. Applies minimal safe fixes via `go get` + `go mod tidy`
5. Runs `make -C server check` and `make -C runner check`
6. Creates a PR with findings summary

Requires the `vuln-scan` skill.

## Key Source Files

| File | Purpose |
|---|---|
| `internal/webhook/handler.go` | HTTP handler, signature verification, event routing, eligibility logic |
| `internal/webhook/events.go` | Event payload structs, constants (label name, trigger command) |
| `internal/webhook/dedup.go` | In-memory recent delivery ID dedup (5 min TTL) |
| `internal/webhook/github.go` | GitHub API client (token gen, labels, file listing, comments, write-access check) |
| `internal/webhook/submitter.go` | Converts `ReviewContext` → `SubmitTaskRequest` |
| `internal/service/service.go` | `ListEnabledPRReviewTriggersByRepo()` method for trigger dispatch |
| `internal/store/store.go` | `PRReviewTriggerConfig` struct, `ScheduleRecord` with trigger fields |
| `db/queries/schedules.sql` | `ListEnabledPRReviewTriggersByRepo` query |
| `db/migrations/004_add_trigger_type.sql` | Schema migration for trigger columns |
| `../.opencode/agent/pr-reviewer.md` | PR review agent definition |
| `main.go` | Route registration (no hardcoded reviewer config) |
