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
  │     ├─ Evaluate eligibility (label or fork)
  │     ├─ If eligible:
  │     │     ├─ Generate GitHub App installation token
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
  │                   │──gen app token                       │                  │
  │                   │──query DB triggers for repo          │                  │
  │                   │──add label (if not label-triggered)  │                  │
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

- **Checked in:** `shouldReview()` — scans `ev.PullRequest.Labels`
- **Auto-labeling:** When a review is triggered by fork or comment, Chetter auto-adds the `chetter-review` label in `submitReview` after confirming at least one matching trigger exists, so the label always indicates a review task was actually submitted. Label-triggered reviews skip auto-labeling.

### 2. Fork (`pull_request` event)

PR comes from an external fork (head repo full name differs from base repo). Automatic review for outside contributors.

- **Checked in:** `shouldReview()` — compares `ev.PullRequest.Head.Repo.FullName` to `repo`

### 3. Comment (`issue_comment` event)

A user with **write access** to the repo posts `/chetter-review` on a PR.

- **Action filter:** `created` only
- **Anti-abuse:** requires write access via `CheckUserHasWriteAccess()` (collaborator or team member with push/triage/admin permissions)
- **Acknowledgment:** Posts a comment `@user requested a review — Chetter is on it.` to the PR
- **Auto-labeling:** Adds the `chetter-review` label via `submitReview` when at least one matching trigger is found

### 4. Manual Task Submission (MCP tool)

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

## Multi-Agent Review Orchestration

Chetter can also run a definitions-driven multi-agent review workflow. The example config repo includes:

- `agents/review-orchestrator.md`
- `agents/standard-pr-reviewer.md`
- `agents/adversarial-pr-reviewer.md`
- `agents/review-synthesizer.md`
- `skills/pr-review-workflow/SKILL.md`
- `mcp-profiles/chetter-orchestration.yaml`
- `triggers/chetter-pr-review-orchestrator.yaml`

The trigger starts one orchestrator task for a PR. The orchestrator uses the attached `chetter-orchestration` MCP profile to submit a standard review task and an adversarial review task without GitHub write inheritance, waits for both to finish with text-free `chetter_task_state`, and starts a synthesizer task with child exports copied into its workspace by the server. `definition_repo` keeps repo-scoped base-repo definitions available for fork PR synthesis without minting GitHub credentials. The orchestration profile carries the full Chetter MCP bearer authority, so use it only in trusted self-hosted deployments until scoped MCP tokens or proxy enforcement exist. The synthesizer does not receive Chetter MCP credentials or GitHub write inheritance; it produces a marked final review body, then the orchestrator verifies the PR head SHA and asks `chetter_pr_review` to post that marked body by task ID without reading transcript contents.

The review task environment includes stable PR context:

- `PR_URL`
- `PR_HEAD_SHA`
- `PR_BASE_REF`
- `PR_HEAD_REF`
- `PR_HEAD_CLONE_URL`

Child review tasks should use `PR_HEAD_CLONE_URL` and `PR_HEAD_REF` so fork PRs are reviewed from the correct source branch. Review prompts should verify `PR_HEAD_SHA` before posting or synthesizing results.

This workflow intentionally uses normal definitions plus configured MCP profiles. It does not add a Chetter-specific orchestration API. The trusted self-hosted MVP still relies on a powerful Chetter MCP token for orchestration; production multi-tenant deployments need scoped MCP tokens or proxy-side enforcement before treating the MCP profile allowlist as a security boundary.

## Webhook Configuration

### Environment Variables

| Env Var | Purpose | Required |
|---|---|---|
| `GITHUB_APP_ID` | GitHub App ID | Yes |
| `GITHUB_APP_PRIVATE_KEY_B64` | PEM private key, base64-encoded | Yes |
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
