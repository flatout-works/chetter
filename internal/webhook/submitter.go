// Package webhook handles GitHub webhook events for the Chetter service.
package webhook

import (
	"context"
	"fmt"
	"strings"
)

// TaskSubmitterService is the interface that the webhook package needs from
// the service package. It's defined in the webhook package so webhook doesn't
// need to import service. In main.go, an adapter is provided that satisfies
// this interface by calling the service's SubmitTask method.
type TaskSubmitterService interface {
	// SubmitTask matches the signature of service.Service.SubmitTask. The
	// return value is ignored by the webhook caller.
	SubmitTask(ctx context.Context, req SubmitTaskRequest) (any, error)
}

// SubmitTaskRequest is a minimal copy of service.SubmitTaskRequest. We define
// it here to avoid importing the service package. The service adapter in
// main.go converts from service.SubmitTaskRequest to this type.
type SubmitTaskRequest struct {
	TeamID      string
	Prompt      string
	GitURL      string
	GitRef      string
	AgentImage  string
	Agent       string
	ProviderID  string
	ModelID     string
	VariantID   string
	Harness     string
	Skills      []string
	MCPProfiles []string
	Env         map[string]string
	TimeoutSec  int
	TriggerName string
	TriggerType string
	SessionMode string
	PauseReason string
	TTLHours    int

	DefinitionRepo             string
	AllowGitHubToken           bool
	AllowPrivilegedMCPProfiles bool
}

// NewServiceSubmitter creates a TaskSubmitter that wraps the given service
// adapter. Use this in main.go to wire the webhook handler to the service.
func NewServiceSubmitter(svc TaskSubmitterService) TaskSubmitter {
	return &serviceSubmitter{svc: svc}
}

type serviceSubmitter struct {
	svc TaskSubmitterService
}

// SubmitReviewTask converts a ReviewContext into a SubmitTaskRequest and
// submits it via the service.
func (s *serviceSubmitter) SubmitReviewTask(ctx context.Context, review ReviewContext) error {
	req := buildReviewTaskRequest(review)
	_, err := s.svc.SubmitTask(ctx, req)
	return err
}

// SubmitTask submits an arbitrary task via the service.
func (s *serviceSubmitter) SubmitTask(ctx context.Context, req SubmitTaskRequest) (any, error) {
	return s.svc.SubmitTask(ctx, req)
}

// buildReviewTaskRequest creates a SubmitTaskRequest for a PR review.
func buildReviewTaskRequest(review ReviewContext) SubmitTaskRequest {
	prompt := review.Prompt
	if prompt == "" {
		prompt = reviewPromptTemplate
		prompt = replaceAll(prompt, "{{REPO}}", review.Repo)
		prompt = replaceAll(prompt, "{{PR_NUMBER}}", fmt.Sprintf("%d", review.PRNumber))
		prompt = replaceAll(prompt, "{{BASE_REF}}", review.BaseRef)
		prompt = replaceAll(prompt, "{{HEAD_REF}}", review.HeadRef)
		prompt = replaceAll(prompt, "{{TRIGGER}}", review.Trigger)
	}

	agentImage := review.AgentImage
	if agentImage == "" {
		agentImage = defaultReviewAgentImage
	}

	env := map[string]string{
		"PR_NUMBER":         fmt.Sprintf("%d", review.PRNumber),
		"GITHUB_TOKEN":      review.GitHubToken,
		"GITHUB_REPO":       review.Repo,
		"PR_BASE_REF":       review.BaseRef,
		"PR_HEAD_REF":       review.HeadRef,
		"PR_HEAD_CLONE_URL": review.HeadCloneURL,
	}
	if review.PRURL != "" {
		env["PR_URL"] = review.PRURL
	} else if review.Repo != "" && review.PRNumber > 0 {
		env["PR_URL"] = fmt.Sprintf("https://github.com/%s/pull/%d", review.Repo, review.PRNumber)
	}
	if review.HeadSHA != "" {
		env["PR_HEAD_SHA"] = review.HeadSHA
	}
	env["CHETTER_AGENT_NAME"] = review.Agent
	env["CHETTER_MODEL_ID"] = review.ModelID
	if review.CommentAuthor != "" {
		env["COMMENT_AUTHOR"] = review.CommentAuthor
	}

	return SubmitTaskRequest{
		TeamID:      review.TeamID,
		Prompt:      prompt,
		GitURL:      review.HeadCloneURL,
		GitRef:      review.HeadRef,
		AgentImage:  agentImage,
		Agent:       review.Agent,
		ProviderID:  review.ProviderID,
		ModelID:     review.ModelID,
		VariantID:   review.VariantID,
		Harness:     review.Harness,
		Skills:      review.Skills,
		MCPProfiles: review.MCPProfiles,
		Env:         env,
		TimeoutSec:  review.TimeoutSec,
		TriggerName: review.TriggerName,
		TriggerType: review.TriggerType,
		SessionMode: review.SessionMode,
		PauseReason: review.PauseReason,
		TTLHours:    review.TTLHours,

		DefinitionRepo:             review.Repo,
		AllowGitHubToken:           true,
		AllowPrivilegedMCPProfiles: review.TeamID == "",
	}
}

// defaultReviewAgentImage is the runner image the webhook submitter fills in
// for review tasks when the caller did not override AgentImage. It must match
// the image used by the chetter runners deployed alongside this service.
const defaultReviewAgentImage = "ghcr.io/flatout-works/chetter-runner:main"

// reviewPromptTemplate is the prompt sent to the review agent. The agent
// receives PR context via environment variables and uses gh CLI for
// GitHub operations.
const reviewPromptTemplate = `You are performing a deep code review on a pull request.

## Context

- Repository: {{REPO}}
- PR number: {{PR_NUMBER}}
- Base ref: {{BASE_REF}}
- Head ref: {{HEAD_REF}}
- Trigger: {{TRIGGER}}

Environment variables available to you:
- PR_NUMBER — the PR to review
- PR_URL — the browser URL for the PR
- PR_HEAD_SHA — the immutable head SHA to verify before posting results
- PR_BASE_REF — the base branch name
- PR_HEAD_REF — the head branch name
- PR_HEAD_CLONE_URL — the clone URL for the PR head repository
- GITHUB_TOKEN — GitHub App installation token with PR read/write
- GITHUB_REPO — repository (e.g., my-org/my-repo)
- COMMENT_AUTHOR — set when the trigger was a comment (the user who requested review)
- CHETTER_AGENT_NAME — agent definition name (e.g., "pr-reviewer")
- CHETTER_MODEL_ID — model identifier (e.g., "opencode/minimax-m3")
- CHETTER_TASK_ID — Chetter task identifier

## Procedure

### 1. Understand the PR

Read the PR description, linked issues, and commit messages:
` + "```bash\n" + `gh pr view $PR_NUMBER --json title,body,baseRefName,headRefName,files,commits
` + "```\n\n" + `Understand the intent before reviewing details.

### 2. Review Changed Files

List the changed files:
` + "```bash\n" + `gh pr diff $PR_NUMBER --name-only
` + "```\n\n" + `For each changed file:
- Read the full file (not just the diff) to understand surrounding context
- Check for:
  - Correctness — logic errors, off-by-one, nil pointer dereferences, missing error checks
  - Security — SQL injection, path traversal, secret leaks, unsafe deserialization
  - Performance — unnecessary allocations, N+1 queries, missing indexes, blocking calls
  - Error handling — swallowed errors, missing context in wrapping, panic instead of error
  - Naming — unclear names, stuttering, inconsistent conventions
  - Concurrency — race conditions, missing locks, goroutine leaks
  - Dead code — unreachable branches, unused imports, commented-out code
  - Tests — missing coverage for new logic, test isolation issues

### 3. Verify Compilation and Tests

` + "```bash\n" + `if [ "$GITHUB_REPO" = "chetter/chetter" ]; then
  make -C server check
  make -C runner check
  make -C builder check
elif [ "$GITHUB_REPO" = "my-org/chetter" ]; then
  make check
else
  go test ./...
fi
` + "```\n\n" + `If tests fail, include the failures in the review output.

### 4. Post Review

Post a structured review using ` + "`chetter_pr_review`" + ` with ` + "`task_id=$CHETTER_TASK_ID`" + `, ` + "`repo=$GITHUB_REPO`" + `, and ` + "`pr_number=$PR_NUMBER`" + `. Do not use ` + "`gh pr review`" + ` and do not manually add the Chetter footer; the tool adds the canonical footer and records audit/artifact metadata.

The review body must include:
- Overall assessment (approve / request-changes / comment)
- Summary of findings grouped by category
- Specific line-level suggestions where applicable
- Test results
Do not push directly to main. Do not merge PRs. Only review and post comments.
`

// replaceAll is a simple string replacement helper.
func replaceAll(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}
