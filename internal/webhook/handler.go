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
	Agent         string // reviewer agent name (from HandlerConfig)
	ProviderID    string // reviewer provider ID (from HandlerConfig)
	ModelID       string // reviewer model ID (from HandlerConfig)
	TimeoutSec    int    // reviewer task timeout (from HandlerConfig)
}

// Handler serves GitHub webhook events. Implements http.Handler.
type Handler struct {
	cfg       HandlerConfig
	gh        *Client
	submitter TaskSubmitter
	recent    *RecentDeliveries
}

// HandlerConfig is the configuration for the webhook handler.
type HandlerConfig struct {
	Disabled           bool
	WebhookSecret      string
	ReviewerAgent      string
	ReviewerProviderID string
	ReviewerModelID    string
	ReviewerTimeoutSec int
	AllowedRepos       []string
	MaxBodyBytes       int64
}

// NewHandler creates a webhook Handler. If the configuration is incomplete,
// the returned handler will accept requests but log "webhook disabled" for
// every event (kill switch behavior).
func NewHandler(cfg HandlerConfig, gh *Client, submitter TaskSubmitter) *Handler {
	return &Handler{
		cfg:       cfg,
		gh:        gh,
		submitter: submitter,
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
	if !h.isAllowedRepo(repo) {
		slog.Debug("webhook: repo not in allowlist", "repo", repo)
		return
	}

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

func (h *Handler) isAllowedRepo(repo string) bool {
	if len(h.cfg.AllowedRepos) == 0 {
		return true
	}
	for _, r := range h.cfg.AllowedRepos {
		if r == repo {
			return true
		}
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
	if !h.isAllowedRepo(repo) {
		return
	}

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

// submitReview gets an installation token and forwards the review context
// to the TaskSubmitter. On failure, posts a comment to the PR.
func (h *Handler) submitReview(ctx ReviewContext) {
	token, err := h.gh.tokenCache.get(h.gh)
	if err != nil {
		slog.Error("webhook: get GitHub token", "err", err)
		h.postCommentOnFailure(ctx, fmt.Sprintf("Chetter could not authenticate: %v", err))
		return
	}
	ctx.GitHubToken = token
	ctx.Agent = h.cfg.ReviewerAgent
	ctx.ProviderID = h.cfg.ReviewerProviderID
	ctx.ModelID = h.cfg.ReviewerModelID
	ctx.TimeoutSec = h.cfg.ReviewerTimeoutSec

	if err := h.submitter.SubmitReviewTask(context.Background(), ctx); err != nil {
		slog.Error("webhook: submit review task", "err", err,
			"repo", ctx.Repo, "pr", ctx.PRNumber, "trigger", ctx.Trigger)
		h.postCommentOnFailure(ctx, CommentReviewFailed)
		return
	}
	slog.Info("webhook: review task submitted",
		"repo", ctx.Repo, "pr", ctx.PRNumber, "trigger", ctx.Trigger)
}

func (h *Handler) postCommentOnFailure(ctx ReviewContext, body string) {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.gh.CreateIssueComment(c, ctx.Repo, ctx.PRNumber, body); err != nil {
		slog.Warn("webhook: post failure comment", "err", err)
	}
}
