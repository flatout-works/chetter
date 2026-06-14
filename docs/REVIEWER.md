# Chetter PR Reviewer — Implementation Plan

## Overview

A deep PR review agent that runs on every meaningful pull request in the repository. Triggered by a GitHub webhook endpoint in the chetter MCP service, not by cron. Posts structured reviews (approve / request-changes) with line-level comments using the "Chetter" GitHub App's identity.

The same GitHub App is used for both creating PRs (existing chetter scheduled tasks) and reviewing them (this feature). Chetter reviews all PRs including its own — the reviewer uses a different agent and model than the author, so a second opinion has value.

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
Check X-GitHub-Delivery + timestamp (replay protection)
       │
       ▼
Route by event type:
   ├─ pull_request (opened/synchronize/reopened/labeled)
   │     ├─ Filter: chetter-review label OR from fork OR modifies *.go|*.proto|migrations/
   │     ├─ If first review for this PR (deduplication via delivery ID):
   │     │     ├─ Auto-label if needed
   │     │     ├─ Generate GitHub App installation token
   │     │     └─ Submit task via svc.SubmitTask()
   │     └─ If duplicate → ignore (in-memory recent set + DB record)
   │
   └─ issue_comment (created)
         ├─ If body == "/chetter-review" AND commenter has write access:
         │     └─ Submit review task for the PR
         └─ Otherwise → ignore
```

### Sequence Diagram

```
GitHub              Chetter                                Runner             OpenCode
  │                   │                                      │                  │
  │──PR opened───────▶│                                      │                  │
  │                   │──200 OK                              │                  │
  │                   │──verify sig                          │                  │
  │                   │──dedup check                         │                  │
  │                   │──gen app token                       │                  │
  │                   │──SubmitTask()                        │                  │
  │                   │◀──────────────ConnectRPC claim───────│                  │
  │                   │                                      │──start container▶│
  │                   │                                      │──git clone        │
  │                   │                                      │──gh pr view      │
  │                   │                    │                 │──review changes  │
  │                   │                    │                 │──gh pr review────│──▶ GitHub
  │                   │                    │                 │                  │
  │                   │                    │◀─status: done──│                  │
```

---

## GitHub App: "Chetter"

Registered in a GitHub organization. The app serves as Chetter's identity on GitHub — assigned as a reviewer, posts review comments, creates PRs, approves/requests-changes.

### Settings

| Setting | Value |
|---|---|
| Name | Chetter |
| Description | Autonomous development agent |
| Webhook URL | `https://<chetter-host>/webhook/github` |
| Webhook secret | Generated, stored as `GITHUB_WEBHOOK_SECRET` env var |

### Permissions

| Permission | Access | Purpose |
|---|---|---|
| Pull requests | Read & Write | Post reviews, approve, request-changes |
| Issues | Read & Write | Read linked issues, comment for `/chetter-review` |
| Contents | Read | Read repo files for review context |

### Events

- `pull_request` (opened, synchronize, reopened, labeled)
- `issue_comment` (created) — for `/chetter-review` command trigger

### Installation

After creation, install the app on your GitHub organization. Note the Installation ID — needed for generating access tokens.

### Manual Setup Steps

1. Create the app at `https://github.com/organizations/YOUR_ORG/settings/apps/new`
2. Set the webhook URL and secret
3. Generate and download the private key (PEM format, saved as `GITHUB_APP_PRIVATE_KEY`)
4. Install on the organization, note the Installation ID
5. Store `GITHUB_APP_ID`, `GITHUB_APP_PRIVATE_KEY`, `GITHUB_WEBHOOK_SECRET` as chetter env vars

---

## Chetter Configuration

### New Env Vars

| Env Var | Purpose | Notes |
|---|---|---|
| `GITHUB_APP_ID` | Numeric GitHub App ID | Required |
| `GITHUB_APP_PRIVATE_KEY` | PEM private key (raw, with newlines) | Required. Or use `GITHUB_APP_PRIVATE_KEY_FILE` for file path. |
| `GITHUB_WEBHOOK_SECRET` | HMAC-SHA256 secret | Required |
| `GITHUB_WEBHOOK_DISABLED` | `true` to kill switch the webhook | Optional, default `false` |
| `GITHUB_REVIEW_ALLOWED_REPOS` | Comma-separated list of repos allowed for review | Optional, default: all |
| `GITHUB_INSTALLATION_ID` | Pre-configured installation ID (skips discovery) | Optional |

### Config Struct

**File: `internal/config/config.go`**

```go
type Config struct {
    // ... existing fields ...
    GitHubAppID          string
    GitHubAppPrivateKey  string
    GitHubAppPrivateKeyFile string
    GitHubWebhookSecret  string
    GitHubWebhookDisabled bool
    GitHubReviewAllowedRepos []string
    GitHubInstallationID  int64
}
```

---

## Webhook Package

**New package: `internal/webhook/`**

Separating this from `internal/service` keeps the service layer focused on business logic. The webhook package is HTTP-handling and GitHub-parsing only; it calls into `service.SubmitTask()` for task submission.

### Files

| File | Purpose |
|---|---|
| `internal/webhook/handler.go` | HTTP handler, signature verification, event routing |
| `internal/webhook/events.go` | Event payload structs (pull_request, issue_comment) |
| `internal/webhook/dedup.go` | In-memory recent delivery IDs (TTL ~5min) |
| `internal/webhook/github.go` | GitHub API helpers (token gen, labels, file listing) |

### `handler.go`

```go
package webhook

type Handler struct {
    cfg         Config
    svc         *service.Service
    tokenCache  *TokenCache       // caches installation tokens
    recent      *RecentDeliveries // dedup
    mu          sync.Mutex
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 1. Read body fully (we need it for HMAC + parsing)
    body, err := io.ReadAll(io.LimitReader(r.Body, 5*1024*1024)) // 5MB cap
    // 2. Verify HMAC-SHA256 signature
    if !verifySignature(h.cfg.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
        http.Error(w, "invalid signature", 401)
        return
    }
    // 3. Check replay protection (timestamp + delivery ID dedup)
    deliveryID := r.Header.Get("X-GitHub-Delivery")
    event := r.Header.Get("X-GitHub-Event")
    if h.recent.Seen(deliveryID) {
        w.WriteHeader(200)
        return
    }
    h.recent.Add(deliveryID)
    // 4. Respond 200 immediately
    w.WriteHeader(200)
    // 5. Process async
    go h.handle(event, body)
}

func (h *Handler) handle(event string, body []byte) {
    switch event {
    case "pull_request":
        h.handlePullRequest(body)
    case "issue_comment":
        h.handleIssueComment(body)
    }
}
```

### Signature Verification

```go
func verifySignature(secret string, body []byte, header string) bool {
    if !strings.HasPrefix(header, "sha256=") {
        return false
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(expected), []byte(header))
}
```

### Replay Protection

In-memory LRU/set of recent `X-GitHub-Delivery` IDs with TTL (~5 minutes). Prevents duplicate processing if GitHub retries the webhook or the handler crashes mid-processing. Not persisted — if chetter restarts, in-flight reviews are lost (acceptable, GitHub will not redeliver).

### `events.go`

Struct definitions for the GitHub event payloads we care about:

```go
type PullRequestEvent struct {
    Action      string      `json:"action"`
    Number      int         `json:"number"`
    PullRequest PullRequest `json:"pull_request"`
    Label       *Label      `json:"label,omitempty"` // for "labeled" action
    Repository  Repository  `json:"repository"`
}

type PullRequest struct {
    Number int    `json:"number"`
    State  string `json:"state"`
    Title  string `json:"title"`
    Body   string `json:"body"`
    Head   struct {
        Ref  string `json:"ref"`
        SHA  string `json:"sha"`
        Repo struct {
            CloneURL string `json:"clone_url"`
        } `json:"repo"`
    } `json:"head"`
    Base struct {
        Ref string `json:"ref"`
    } `json:"base"`
    User struct {
        Login string `json:"login"`
    } `json:"user"`
    Labels []Label `json:"labels"`
}

type IssueCommentEvent struct {
    Action  string     `json:"action"`
    Comment Comment    `json:"comment"`
    Issue   Issue      `json:"issue"` // contains pull_request link
    Repo    Repository `json:"repository"`
}

type Comment struct {
    Body string `json:"body"`
    User struct{ Login string `json:"login"` } `json:"user"`
}
```

### Filter Logic

```go
func shouldReview(pr *PullRequest, repo string, cfg Config) (bool, string) {
    // Allowed repos check
    if len(cfg.AllowedRepos) > 0 && !contains(cfg.AllowedRepos, repo) {
        return false, ""
    }
    // Label check (explicit request)
    if hasLabel(pr.Labels, "chetter-review") {
        return true, "label"
    }
    // Auto-trigger: from fork
    if pr.User.Login != repoOwner(repo) {
        return true, "fork"
    }
    // Auto-trigger: modifies Go/proto/migrations files
    files := listPRFiles(token, repo, pr.Number)
    if anyMatches(files, `**/*.go`, `**/*.proto`, `**/server/db/migrations/**`) {
        return true, "file-pattern"
    }
    return false, ""
}
```

### Auto-Labeling

When the review is triggered by file-pattern or fork, automatically add the `chetter-review` label so the user can see why. Skipped if the label was the trigger (it's already there).

### Task Submission

```go
func (h *Handler) submitReviewTask(pr PullRequest, repo Repository, trigger string) error {
    token, err := h.tokenCache.Get(repo.FullName)
    if err != nil {
        slog.Error("failed to get GitHub token", "repo", repo.FullName, "err", err)
        return err
    }
    
    _, err = h.svc.SubmitTask(context.Background(), service.SubmitTaskInput{
        Prompt:     renderReviewPrompt(pr, repo, trigger),
        GitURL:     pr.Head.Repo.CloneURL,
        GitRef:     pr.Head.Ref,
        Agent:      "pr-reviewer",
        ProviderID: "opencode-go",
        ModelID:    "minimax-m3",
        Env: map[string]string{
            "PR_NUMBER":   strconv.Itoa(pr.Number),
            "GITHUB_TOKEN": token,
            "GITHUB_REPO":  repo.FullName,
        },
        TimeoutSec: 3600,
    })
    return err
}
```

If the task submission fails, post a comment to the PR:
```go
gh comment PR_NUMBER --body "🤖 Chetter review could not start. Check chetter logs."
```

### Token Caching

GitHub installation tokens are valid for 1 hour. Cache them per repo:

```go
type TokenCache struct {
    mu     sync.Mutex
    tokens map[string]cachedToken // key: repo full name
}

type cachedToken struct {
    token string
    expiry time.Time
}

func (c *TokenCache) Get(repo string) (string, error) {
    // Check cache, refresh if <5min remaining
    // On miss: call generateInstallationToken
}
```

---

## GitHub API Helpers

**File: `internal/webhook/github.go`**

### Token Generation

```go
func generateInstallationToken(appID int64, privateKeyPEM string, installationID int64) (string, error) {
    // 1. Sign JWT: {iss: appID, iat: now, exp: now+10min}
    //    Algorithm: RS256, Key: parsed PEM
    token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
        "iss": strconv.FormatInt(appID, 10),
        "iat": time.Now().Unix(),
        "exp": time.Now().Add(10 * time.Minute).Unix(),
    })
    signed, err := token.SignedString(privateKey)
    
    // 2. POST https://api.github.com/app/installations/{installationID}/access_tokens
    req, _ := http.NewRequest("POST", "https://api.github.com/app/installations/"+...+"/access_tokens", nil)
    req.Header.Set("Authorization", "Bearer "+signed)
    req.Header.Set("Accept", "application/vnd.github+json")
    
    // 3. Parse response, return token + expiry
}
```

Uses `github.com/golang-jwt/jwt/v5` (verify it's in `go.mod`).

### Label Management

```go
func addLabel(token, repo string, prNumber int, label string) error {
    url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/labels", repo, prNumber)
    body := fmt.Sprintf(`{"labels": [%q]}`, label)
    req, _ := http.NewRequest("POST", url, strings.NewReader(body))
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Accept", "application/vnd.github+json")
    resp, err := http.DefaultClient.Do(req)
    // check 2xx, return err
}

func listPRFiles(token, repo string, prNumber int) ([]string, error) {
    url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/files", repo, prNumber)
    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Accept", "application/vnd.github+json")
    // paginate if needed (default per_page=30, max 100)
    // return list of filenames
}
```

### Post Comment (for error cases)

```go
func postComment(token, repo string, prNumber int, body string) error {
    url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments", repo, prNumber)
    // POST with {"body": body}
}
```

---

## Route Registration

**File: `main.go`**

```go
wh := webhook.NewHandler(cfg, svc)
mux.HandleFunc("/healthz", ...)
mux.Handle("/mcp", authMiddleware(cfg.MCPAuthToken, mcpHandler))
mux.Handle("/webhook/github", wh.ServeHTTP)
```

The webhook handler is registered outside the auth middleware — HMAC signature is its own authentication.

---

## Agent Definition

**New file: `.opencode/agent/pr-reviewer.md`**

```markdown
---
description: Deep PR review — correctness, security, performance, error handling, style. Posts structured review and approves or requests changes.
model: opencode-go/minimax-m3
mode: primary
permission:
  edit: allow
  bash: allow
---

You perform deep code reviews on pull requests in the target repository.

## Context

The environment provides:
- PR_NUMBER — the PR to review
- GITHUB_TOKEN — GitHub App installation token with PR read/write
- GITHUB_REPO — repository (e.g., flatout-works/flatout)
- AGENT_NAME — your agent definition name (e.g., "pr-reviewer")
- MODEL_ID — your model identifier (e.g., "opencode-go/minimax-m3")

## Procedure

### 1. Understand the PR

Read the PR description, linked issues, and commit messages:
```bash
gh pr view $PR_NUMBER --json title,body,baseRefName,headRefName,files,commits
```

Understand the intent before reviewing details. Linked issues can be read with `gh issue view <num>`.

### 2. Review Changed Files

List the changed files:
```bash
gh pr diff $PR_NUMBER --name-only
```

For each changed file:
- Read the full file (not just the diff) to understand surrounding context
- Check for:
  - **Correctness** — logic errors, off-by-one, nil pointer dereferences, missing error checks
  - **Security** — SQL injection, path traversal, secret leaks, unsafe deserialization
  - **Performance** — unnecessary allocations, N+1 queries, missing indexes, blocking calls
  - **Error handling** — swallowed errors, missing context in wrapping, panic instead of error
  - **Naming** — unclear names, stuttering, inconsistent conventions
  - **Concurrency** — race conditions, missing locks, goroutine leaks
  - **Dead code** — unreachable branches, unused imports, commented-out code
  - **Tests** — missing coverage for new logic, test isolation issues

### 3. Verify Compilation and Tests

Run the relevant checks:
```bash
make -C server check
make -C runner check
make -C builder check
make check
```

If tests fail, include the failures in the review output.

### 4. Post Review

Post a structured review:
```bash
# For approval:
gh pr review $PR_NUMBER --approve --body "..."

# For changes requested:
gh pr review $PR_NUMBER --request-changes --body "..."
```

The review body must include:
- **Overall assessment** — approve / request-changes / comment
- **Summary of findings** — grouped by category (correctness, security, performance, etc.)
- **Specific line-level suggestions** — where applicable
- **Test results** — which checks passed/failed

### 5. PR Footer for Chetter-Created PRs

When creating PRs as a chetter agent, always append this footer to the PR description:

```
---
Generated by [Chetter](https://github.com/flatout-works/chetter)
Agent: ${CHETTER_AGENT_NAME} | Model: ${CHETTER_MODEL_ID} | Runner: ${CHETTER_RUNNER_IMAGE} (${CHETTER_RUNNER_IMAGE_DIGEST})
```

Resolve `${CHETTER_AGENT_NAME}`, `${CHETTER_MODEL_ID}`, `${CHETTER_RUNNER_IMAGE}`, and `${CHETTER_RUNNER_IMAGE_DIGEST}` from the environment variables.

Do not push directly to main. Do not merge PRs. Only review and post comments.
```

---

## Runner: Pass GITHUB_TOKEN to Container

**No code change needed in the runner.** The `req.Env` map already carries env vars from the task submission. In `runDockerAgent` and `runLocalAgent`, the env vars are injected:

- Docker mode: `-e GITHUB_TOKEN=...` on the `docker run` command (already handled by the existing env-var loop)
- Local mode: `append(env, "GITHUB_TOKEN="+req.Env["GITHUB_TOKEN"])` (already handled)

Verify this with a test: submit a task with `GITHUB_TOKEN` in env, confirm the agent can run `gh pr view` inside the container.

---

## Update Existing Schedules with PR Footer

Modify the prompt in all 6 schedule YAML files to append the chetter footer when creating PRs:

### Footer Template

```markdown

---
Generated by [Chetter](https://github.com/flatout-works/chetter)
Agent: $CHETTER_AGENT_NAME | Model: $CHETTER_MODEL_ID | Runner: $CHETTER_RUNNER_IMAGE ($CHETTER_RUNNER_IMAGE_DIGEST)
```

### Schedule-Specific Values

| Schedule | Agent | Model |
|---|---|---|
| `code-quality-audit-daily` | `code-quality-auditor` | from agent def |
| `nightly-changelog-update` | `changelog-maintainer` | from agent def |
| `nightly-docs-update` | `docs-maintainer` | from agent def |
| `nightly-issue-fixer` | (default agent) | from schedule |
| `nightly-vulnerability-scan` | (default agent) | from schedule |
| `weekday-doc-review` | `docs-maintainer` | from agent def |

The agent must read `CHETTER_AGENT_NAME`, `CHETTER_MODEL_ID`, `CHETTER_RUNNER_IMAGE`, and `CHETTER_RUNNER_IMAGE_DIGEST` from env (set by chetter) and substitute into the footer.

---

## Local Development

### Testing Webhook Locally

Options for forwarding GitHub webhooks to a local chetter:

1. **smee.io** (GitHub's own webhook proxy): `smee --url https://smee.io/<channel> --target http://localhost:8080/webhook/github`
2. **ngrok**: `ngrok http 8080` and use the HTTPS URL as the GitHub App webhook URL
3. **Manual POST**: `curl -X POST http://localhost:8080/webhook/github -H "X-Hub-Signature-256: sha256=..." -d @sample-payload.json`

A `make webhook-test` target could POST a sample payload:

```makefile
webhook-test:
    @curl -sS -X POST http://localhost:8080/webhook/github \
        -H "X-GitHub-Event: pull_request" \
        -H "X-GitHub-Delivery: test-$$(date +%s)" \
        -H "X-Hub-Signature-256: sha256=..." \
        -H "Content-Type: application/json" \
        -d @internal/webhook/testdata/pull_request_opened.json
```

### Test Repos

For E2E testing, a test repo (or a branch in the main repo) with deliberate issues. A small Go program with known bugs is a good first PR for the review agent.

---

## Testing Strategy

### Unit Tests

| Component | Test |
|---|---|
| `verifySignature` | Known vectors from GitHub docs, negative cases |
| `generateInstallationToken` | Mock GitHub API, assert JWT signing + API call |
| `shouldReview` | Label present/absent, fork vs branch, file patterns |
| `addLabel` | Mock GitHub API |
| `listPRFiles` | Mock GitHub API, pagination |
| Dedup logic | Same delivery ID twice, different IDs |

### Integration Tests

```go
func TestWebhookHandler_ProcessesPullRequest(t *testing.T) {
    // Spin up chetter in-process
    // POST a sample pull_request.opened payload
    // Assert SubmitTask was called with right args
}
```

### E2E Test

1. Open a PR in a test repo
2. Verify webhook fires
3. Verify task is submitted to chetter
4. Verify runner picks up task
5. Verify review is posted to PR
6. Verify review content matches expectations

---

## Rollback Plan

If the feature needs to be disabled quickly:
- Set `GITHUB_WEBHOOK_DISABLED=true` — chetter returns 200 to all webhooks without processing
- The existing cron schedules are unaffected

---

## File Summary

| File | Action | Description |
|---|---|---|
| `internal/config/config.go` | Edit | Add GitHubAppID, GitHubAppPrivateKey, GitHubWebhookSecret, GitHubWebhookDisabled, GitHubReviewAllowedRepos, GitHubInstallationID |
| `internal/webhook/handler.go` | **New** | HTTP handler, signature verification, event routing |
| `internal/webhook/events.go` | **New** | Event payload structs |
| `internal/webhook/dedup.go` | **New** | Recent deliveries dedup |
| `internal/webhook/github.go` | **New** | GitHub API helpers (token, label, file listing, comment) |
| `internal/webhook/handler_test.go` | **New** | Unit + integration tests |
| `internal/webhook/testdata/` | **New** | Sample webhook payloads for tests |
| `main.go` | Edit | Add `/webhook/github` route, wire up handler |
| `go.mod` | Edit | Ensure `github.com/golang-jwt/jwt/v5` dependency |
| `.opencode/agent/pr-reviewer.md` | **New** | PR review agent definition |
| `schedules/*.yaml` | Edit | Add chetter footer to all PR-creating schedules |
| `builder/Makefile` | Optional | Add `webhook-test` target |

---

## Implementation Order

| Step | Description | Effort | Blocks |
|---|---|---|---|
| 1 | Register GitHub App "Chetter" (manual) | Small | All webhook work |
| 2 | Config: add GitHub App fields to chetter config | Trivial | 3, 4 |
| 3 | GitHub API helpers: token generation, label, file listing, comment | Small | 4 |
| 4 | Webhook handler: signature verification, event routing, task submission, dedup | Medium | 5 |
| 5 | Route: register `/webhook/github` in main.go | Trivial | 6 |
| 6 | Agent definition: `.opencode/agent/pr-reviewer.md` | Small | 7 |
| 7 | Update schedules: add PR footer to all YAML files | Small | 8 |
| 8 | Write unit + integration tests | Medium | 9 |
| 9 | Test end-to-end: open a PR → webhook fires → task submitted → review posted | Medium | Done |

---

## What This Enables

- **Automatic deep code review** on every PR that modifies Go/proto files
- **On-demand review** via `/chetter-review` comment (org members/contributors only) or `chetter-review` label
- **Chetter-authored PRs** with proper attribution (footer with agent/model)
- **Chetter identity** on GitHub — assigned as reviewer, posts as "Chetter" app
- **Self-review of chetter's own PRs** — different model/agent for a second opinion
- **Unified architecture** — same task submission pipeline as scheduled tasks, triggered by webhook instead of cron
- **Kill switch** via env var for quick rollback
- **Per-repo allowlist** to limit which repos chetter reviews
