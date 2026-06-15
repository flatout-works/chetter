// Package webhook handles GitHub webhook events for the Chetter service.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// TriggerResolver is the subset of the service that the webhook needs to
// resolve PR review triggers for a given repo. Defined as an interface to
// allow mocking in tests.
type TriggerResolver interface {
	ListEnabledPRReviewTriggersByRepo(ctx context.Context, repo string) ([]ReviewTrigger, error)
}

// ReviewTrigger is the resolved data from a single PR review trigger.
type ReviewTrigger struct {
	Name        string
	Prompt      string
	AgentImage  string
	Agent       string
	ProviderID  string
	ModelID     string
	VariantID   string
	TimeoutSec  int
	GitURL      string
	GitRef      string
	Skills      []string
}

// TaskSubmitter is the subset of service.Service that the webhook needs to
// submit review tasks. Defined as an interface to allow mocking in tests.
type TaskSubmitter interface {
	SubmitReviewTask(ctx context.Context, review ReviewContext) error
}

// ReviewContext is the data passed to TaskSubmitter for a single review.
type ReviewContext struct {
	Trigger       string // "label", "fork", "file-pattern", "comment"
	Repo          string // e.g., "chetter/chetter"
	PRNumber      int
	BaseRef       string
	HeadRef       string
	HeadCloneURL  string
	CommentAuthor string // only set for comment triggers
	GitHubToken   string // installation token for the review agent
	Prompt        string // trigger-supplied prompt; empty falls back to the built-in template
	AgentImage    string // trigger-supplied agent image; empty falls back to the default
	Agent         string // reviewer agent name (from the trigger config)
	ProviderID    string // reviewer provider ID (from the trigger config)
	ModelID       string // reviewer model ID (from the trigger config)
	VariantID     string // reviewer variant ID (from the trigger config)
	Skills        []string
	TimeoutSec    int // reviewer task timeout (from the trigger config)
}

// Handler serves GitHub webhook events. Implements http.Handler.
type Handler struct {
	cfg       HandlerConfig
	gh        *Client
	submitter TaskSubmitter
	triggers  TriggerResolver
	recent    *RecentDeliveries
}

// HandlerConfig is the configuration for the webhook handler.
type HandlerConfig struct {
	Disabled      bool
	WebhookSecret string
	MaxBodyBytes  int64
}

// NewHandler creates a webhook Handler. If the configuration is incomplete,
// the returned handler will accept requests but log "webhook disabled" for
// every event (kill switch behavior).
func NewHandler(cfg HandlerConfig, gh *Client, submitter TaskSubmitter, triggers TriggerResolver) *Handler {
	return &Handler{
		cfg:       cfg,
		gh:        gh,
		submitter: submitter,
		triggers:  triggers,
		recent:    NewRecentDeliveries(5*time.Minute, 4096),
	}
}

// ServeHTTP handles an incoming GitHub webhook request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Disabled {
		slog.Info("webhook disabled, ignoring request")
		w.WriteHeader(http.StatusOK)
		return
	}

	maxBody := h.cfg.MaxBodyBytes
	if maxBody == 0 {
		maxBody = 5 * 1024 * 1024 // 5MB
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		slog.Warn("webhook: read body", "err", err)
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if !verifySignature(h.cfg.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		slog.Warn("webhook: invalid signature")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	deliveryID := r.Header.Get("X-GitHub-Delivery")
	event := r.Header.Get("X-GitHub-Event")
	if h.recent.Seen(deliveryID) {
		slog.Info("webhook: duplicate delivery, ignoring", "deliveryID", deliveryID, "event", event)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Respond 200 immediately; process async so GitHub doesn't retry on slowness.
	w.WriteHeader(http.StatusOK)

	go h.handle(event, body)
}

func (h *Handler) handle(event string, body []byte) {
	switch event {
	case EventTypePullRequest:
		h.handlePullRequest(body)
	case EventTypeIssueComment:
		h.handleIssueComment(body)
	default:
		slog.Debug("webhook: ignoring unsupported event", "event", event)
	}
}

// verifySignature checks the HMAC-SHA256 signature from GitHub.
func verifySignature(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(header))
}

func (h *Handler) handlePullRequest(body []byte) {
	var ev PullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		slog.Warn("webhook: parse pull_request", "err", err)
		return
	}

	// Only act on the actions that change reviewable state.
	switch ev.Action {
	case PullRequestActionOpened,
		PullRequestActionSynchronize,
		PullRequestActionReopened,
		PullRequestActionLabeled:
		// continue
	default:
		slog.Debug("webhook: ignoring pull_request action", "action", ev.Action)
		return
	}

	// For "labeled" events, only proceed if the label added is ours.
	if ev.Action == PullRequestActionLabeled && (ev.Label == nil || ev.Label.Name != ChetterReviewLabel) {
		return
	}

	repo := ev.Repository.FullName

	trigger, ok := h.shouldReview(ev, repo)
	if !ok {
		slog.Debug("webhook: PR not eligible for review", "repo", repo, "pr", ev.Number)
		return
	}

	// Auto-label if triggered by something other than the label itself.
	if trigger != "label" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		has, err := h.gh.HasLabel(ctx, repo, ev.Number, ChetterReviewLabel)
		if err != nil {
			slog.Warn("webhook: check label", "repo", repo, "pr", ev.Number, "err", err)
		} else if !has {
			if err := h.gh.AddIssueLabel(ctx, repo, ev.Number, ChetterReviewLabel); err != nil {
				slog.Warn("webhook: add label", "repo", repo, "pr", ev.Number, "err", err)
			}
		}
	}

	h.submitReview(ReviewContext{
		Trigger:      trigger,
		Repo:         repo,
		PRNumber:     ev.Number,
		BaseRef:      ev.PullRequest.Base.Ref,
		HeadRef:      ev.PullRequest.Head.Ref,
		HeadCloneURL: ev.PullRequest.Head.Repo.CloneURL,
	})
}

// shouldReview determines whether a PR needs a review and returns the trigger reason.
func (h *Handler) shouldReview(ev PullRequestEvent, repo string) (string, bool) {
	// 1. Explicit label request.
	for _, l := range ev.PullRequest.Labels {
		if l.Name == ChetterReviewLabel {
			return "label", true
		}
	}

	// 2. PR from a fork (external contributor).
	if ev.PullRequest.Head.Repo.FullName != "" && ev.PullRequest.Head.Repo.FullName != repo {
		return "fork", true
	}

	// 3. Modifies Go/proto/migrations files.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	files, err := h.gh.ListPRFiles(ctx, repo, ev.Number)
	return h.shouldReviewWithFiles(ev, repo, files, err)
}

// shouldReviewWithFiles is the testable inner part of shouldReview. Given a
// pre-fetched list of files (or an error), it returns the trigger and whether
// review is needed. Extracted so tests don't need to mock the GitHub client.
func (h *Handler) shouldReviewWithFiles(ev PullRequestEvent, repo string, files []string, filesErr error) (string, bool) {
	// 1. Explicit label request.
	for _, l := range ev.PullRequest.Labels {
		if l.Name == ChetterReviewLabel {
			return "label", true
		}
	}

	// 2. PR from a fork (external contributor).
	if ev.PullRequest.Head.Repo.FullName != "" && ev.PullRequest.Head.Repo.FullName != repo {
		return "fork", true
	}

	// 3. Modifies Go/proto/migrations files.
	if filesErr != nil {
		slog.Warn("webhook: list files (in testable path)", "err", filesErr)
		return "", false
	}
	if matchesCodePaths(files) {
		return "file-pattern", true
	}

	return "", false
}

// matchesCodePaths returns true if any file matches a Go/proto/migrations pattern.
func matchesCodePaths(files []string) bool {
	for _, f := range files {
		if matchesCodePath(f) {
			return true
		}
	}
	return false
}

// matchesCodePath checks if a single file path matches the patterns
// that warrant a review.
func matchesCodePath(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".go") || strings.HasSuffix(base, ".proto") {
		return true
	}
	if strings.HasPrefix(path, "server/db/migrations/") || strings.Contains(path, "/db/migrations/") {
		return true
	}
	return false
}

func (h *Handler) handleIssueComment(body []byte) {
	var ev IssueCommentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		slog.Warn("webhook: parse issue_comment", "err", err)
		return
	}
	if ev.Action != "created" {
		return
	}
	if !ev.IsPullRequest() {
		return // not a PR comment
	}
	if strings.TrimSpace(ev.Comment.Body) != ReviewTriggerCommand {
		return
	}

	repo := ev.Repository.FullName

	// Verify the commenter has write access (anti-abuse).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hasAccess, err := h.gh.CheckUserHasWriteAccess(ctx, repo, ev.Comment.User.Login)
	if err != nil {
		slog.Warn("webhook: check write access", "user", ev.Comment.User.Login, "err", err)
		return
	}
	if !hasAccess {
		slog.Info("webhook: ignoring /chetter-review from non-writer",
			"user", ev.Comment.User.Login, "repo", repo)
		return
	}

	// Fetch the PR to get the head ref + clone URL.
	prCtx, prCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer prCancel()
	head, base, cloneURL, err := h.gh.GetPullRequest(prCtx, repo, ev.Issue.Number)
	if err != nil {
		slog.Warn("webhook: fetch PR", "err", err)
		return
	}

	h.submitReview(ReviewContext{
		Trigger:       "comment",
		Repo:          repo,
		PRNumber:      ev.Issue.Number,
		BaseRef:       base,
		HeadRef:       head,
		HeadCloneURL:  cloneURL,
		CommentAuthor: ev.Comment.User.Login,
	})
}

// submitReview gets an installation token, finds matching PR review triggers,
// and forwards the review context to the TaskSubmitter for each match.
// Fails closed: if no triggers are configured for the repo, the PR is not reviewed.
func (h *Handler) submitReview(ctx ReviewContext) {
	token, err := h.gh.tokenCache.get(h.gh)
	if err != nil {
		slog.Error("webhook: get GitHub token", "err", err)
		h.postCommentOnFailure(ctx, fmt.Sprintf("Chetter could not authenticate: %v", err))
		return
	}
	ctx.GitHubToken = token

	triggers, err := h.triggers.ListEnabledPRReviewTriggersByRepo(context.Background(), ctx.Repo)
	if err != nil {
		slog.Error("webhook: list pr review triggers", "err", err, "repo", ctx.Repo)
		h.postCommentOnFailure(ctx, CommentReviewFailed)
		return
	}

	if len(triggers) == 0 {
		slog.Info("webhook: no PR review triggers configured for repo; skipping review",
			"repo", ctx.Repo, "pr", ctx.PRNumber)
		return
	}

	for _, t := range triggers {
		rc := ctx
		if t.Prompt != "" {
			rc.Prompt = t.Prompt
		}
		if t.AgentImage != "" {
			rc.AgentImage = t.AgentImage
		}
		if t.GitURL != "" {
			rc.HeadCloneURL = t.GitURL
		}
		if t.GitRef != "" {
			rc.HeadRef = t.GitRef
		}
		if len(t.Skills) > 0 {
			rc.Skills = t.Skills
		}
		rc.Agent = t.Agent
		rc.ProviderID = t.ProviderID
		rc.ModelID = t.ModelID
		rc.VariantID = t.VariantID
		rc.TimeoutSec = t.TimeoutSec
		if err := h.submitter.SubmitReviewTask(context.Background(), rc); err != nil {
			slog.Error("webhook: submit review task", "err", err,
				"trigger", t.Name, "repo", rc.Repo, "pr", rc.PRNumber, "triggerType", rc.Trigger)
			h.postCommentOnFailure(rc, CommentReviewFailed)
			continue
		}
		slog.Info("webhook: review task submitted",
			"trigger", t.Name, "repo", rc.Repo, "pr", rc.PRNumber, "triggerType", rc.Trigger)
	}
}

func (h *Handler) postCommentOnFailure(ctx ReviewContext, body string) {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.gh.CreateIssueComment(c, ctx.Repo, ctx.PRNumber, body); err != nil {
		slog.Warn("webhook: post failure comment", "err", err)
	}
}
