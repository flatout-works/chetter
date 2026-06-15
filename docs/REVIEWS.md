# Chetter PR Reviews

Chetter provides automated code review on pull requests via a GitHub webhook integration. Reviews use a dedicated `pr-reviewer` agent running in the Chetter runner fleet.

---

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
  │     ├─ Evaluate eligibility (see triggers below)
  │     ├─ If eligible:
  │     │     ├─ Auto-add chetter-review label (if not label-triggered)
  │     │     ├─ Generate GitHub App installation token
  │     │     └─ Submit review task
  │     └─ If not → ignore
  │
  └─ issue_comment (created)
        ├─ If body == "/chetter-review" AND commenter has write access:
        │     └─ Submit review task for the PR
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
  │                   │──gen app token                       │                  │
  │                   │──SubmitReviewTask()                  │                  │
  │                   │◀──────────────ConnectRPC claim───────│                  │
  │                   │                                      │──start container▶│
  │                   │                                      │──git clone       │
  │                   │                                      │──gh pr view      │
  │                   │                                      │──review changes  │
  │                   │                                      │──gh pr review────│──▶ GitHub
  │                   │                                      │                  │
  │                   │                    │◀─status: done──│                  │
```

---

## Trigger Paths

Repo-level filtering is now done at trigger level: a trigger's `trigger_config->>'$.repo'` selects which GitHub repo it watches. The webhook handler queries for matching enabled triggers at event time. If no enabled PR review trigger exists for a repo, the webhook event is ignored (no review is submitted).

### 1. Label (`pull_request` event)

PR has the `chetter-review` label applied. Evaluated on all watched PR actions (`opened`, `synchronize`, `reopened`, `labeled`). For the `labeled` action specifically, only the `chetter-review` label triggers — other labels are ignored.

- **Checked in:** `shouldReview()` / `shouldReviewWithFiles()` — scans `ev.PullRequest.Labels`
- **Auto-labeling:** When a review is triggered by fork or file-pattern, Chetter auto-adds the `chetter-review` label so the user can see why a review was triggered.

### 2. Fork (`pull_request` event)

PR comes from an external fork (head repo full name differs from base repo). Automatic review for outside contributors.

- **Checked in:** `shouldReview()` / `shouldReviewWithFiles()` — compares `ev.PullRequest.Head.Repo.FullName` to `repo`

### 3. File Pattern (`pull_request` event)

PR modifies files matching review-worthy patterns:

| Pattern | Scope |
|---|---|
| `*.go` | Go source files |
| `*.proto` | Protobuf definitions |
| `**/db/migrations/*` | Database migrations |

Only the filename is checked for `.go` and `.proto`. For migrations, the path must contain `/db/migrations/`.

- **Checked in:** `shouldReview()` — fetches PR files from GitHub API, then `matchesCodePaths()` checks each file
- **Edge case:** If the GitHub API call fails (timeout, rate limit), the review is skipped entirely.

### 4. Comment (`issue_comment` event)

A user with **write access** to the repo posts `/chetter-review` on a PR.

- **Action filter:** `created` only
- **Anti-abuse:** requires write access via `CheckUserHasWriteAccess()` (collaborator or team member with push/triage/admin permissions)
- **Does not auto-label** the PR

### 5. Manual Task Submission (MCP tool)

Anyone with `chetter_submit_task` access can submit a review directly. This bypasses the webhook entirely — no label, no fork check, no file patterns, no comment parsing, no write-access check.

To manually trigger a review via the MCP tool, craft a task with:
- `agent`: `pr-reviewer` (or another reviewer agent)
- `git_url`/`git_ref`: the PR branch to review
- `env`: set `PR_NUMBER` and `GITHUB_REPO` for the agent's review procedure

---

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
  "provider_id": "opencode-go",
  "model_id": "minimax-m3",
  "timeout_sec": 3600
}
```

Required fields for `pr_review` triggers:
- `name` — unique trigger name
- `trigger_type` — must be `pr_review`
- `repo` — full repository name (e.g. `flatout-works/chetter`)
- `prompt` — instructions sent to the agent
- `agent_image` — runner harness image
- `agent` — agent definition name (e.g. `pr-reviewer`)

### Managing Triggers

| Tool | Purpose |
|---|---|
| `chetter_create_trigger` | Create a trigger (cron or pr_review) |
| `chetter_update_trigger` | Update an existing trigger |
| `chetter_list_triggers` | List all triggers, optionally by type |
| `chetter_delete_trigger` | Delete a trigger by name |
| `chetter_run_trigger` | Run a cron trigger immediately |

### Multiple Triggers Per Repo

Multiple PR review triggers for the same repo are allowed. Each trigger submits a separate review task when a matching PR event arrives. Useful for running different agents (e.g. "deep code review" + "security review") on the same PRs.

## Webhook Configuration

### Environment Variables

| Env Var | Purpose | Required |
|---|---|---|
| `GITHUB_APP_ID` | GitHub App ID | Yes |
| `GITHUB_APP_PRIVATE_KEY` | PEM private key (raw, with newlines) | Yes |
| `GITHUB_WEBHOOK_SECRET` | HMAC-SHA256 secret for signature verification | Yes |
| `GITHUB_WEBHOOK_DISABLED` | `true` to disable the webhook (kill switch) | No |
| `GITHUB_INSTALLATION_ID` | Pre-configured installation ID | No |

No reviewer-specific env vars are needed — agent, model, and timeout come from the trigger configuration in the database.

### Route Registration

```go
wh := webhook.NewHandler(cfg, svc)
mux.Handle("/webhook/github", wh)
```

The webhook handler is registered outside the MCP auth middleware — HMAC signature is its own authentication.

---

## GitHub App: Chetter

Registered at the GitHub organization level. Used as the review identity — posts reviews, adds labels, creates comments.

### Required Permissions

| Permission | Access | Purpose |
|---|---|---|
| Pull requests | Read & Write | Post reviews, approve, request changes |
| Issues | Read & Write | Read linked issues, comment for `/chetter-review` |
| Contents | Read | Read repo files for review context |

### Subscribed Events

- `pull_request` (opened, synchronize, reopened, labeled)
- `issue_comment` (created)

---

## Key Source Files

| File | Purpose |
|---|---|
| `internal/webhook/handler.go` | HTTP handler, signature verification, event routing, eligibility logic |
| `internal/webhook/events.go` | Event payload structs, constants (label name, trigger command) |
| `internal/webhook/dedup.go` | In-memory recent delivery ID dedup (5 min TTL) |
| `internal/webhook/github.go` | GitHub API client (token gen, labels, file listing, comments, write-access check) |
| `internal/webhook/submitter.go` | Converts `ReviewContext` → `SubmitTaskRequest` |
| `internal/webhook/handler_test.go` | Unit and integration tests |
| `internal/service/service.go` | `ListEnabledPRReviewTriggersByRepo()` method for trigger dispatch |
| `internal/store/store.go` | `PRReviewTriggerConfig` struct, `ScheduleRecord` with trigger fields |
| `db/queries/schedules.sql` | `ListEnabledPRReviewTriggersByRepo` query |
| `db/migrations/004_add_trigger_type.sql` | Schema migration for trigger columns |
| `.opencode/agent/pr-reviewer.md` | PR review agent definition |
| `main.go` | Route registration (no hardcoded reviewer config) |

---

## Deduplication

An in-memory set of recent `X-GitHub-Delivery` IDs prevents duplicate processing if GitHub retries a webhook delivery. Entries expire after 5 minutes (configurable). Not persisted across restarts — acceptable since GitHub does not redeliver after a crash.

---

## Error Handling

If the review task submission fails:
1. Chetter posts a comment to the PR: "Chetter could not start the review. Check chetter logs."
2. The error is logged with repo, PR number, trigger name, and trigger reason.

If no PR review triggers are configured for a repo, the webhook ignores the event entirely. If the GitHub API call to list PR files fails (in the file-pattern trigger), the PR is skipped.

---

## Disable / Kill Switch

Set `GITHUB_WEBHOOK_DISABLED=true` — the handler returns 200 to all webhooks without processing. Business as usual for scheduled tasks and cron triggers; only webhook-triggered events stop.
