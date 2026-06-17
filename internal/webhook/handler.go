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
	"strings"
	"time"
)

// TriggerResolver is the subset of the service that the webhook needs to
// resolve triggers for a given repo. Defined as an interface to allow
// mocking in tests.
type TriggerResolver interface {
	ListEnabledPRReviewTriggersByRepo(ctx context.Context, repo string) ([]ReviewTrigger, error)
	ListEnabledIssueTriggersByRepo(ctx context.Context, repo string) ([]ReviewTrigger, error)
}

// ReviewTrigger is the resolved data from a single trigger.
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
	Event       string // which webhook action this trigger responds to (e.g. "opened", "labeled"), empty = all
}

// TaskSubmitter is the subset of service.Service that the webhook needs to
// submit tasks. Defined as an interface to allow mocking in tests.
type TaskSubmitter interface {
	SubmitReviewTask(ctx context.Context, review ReviewContext) error
	SubmitTask(ctx context.Context, req SubmitTaskRequest) (any, error)
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
	case EventTypeIssues:
		h.handleIssues(body)
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
	triggerAction := triggerActionFromPR(ev, repo)
	if triggerAction == "" {
		slog.Debug("webhook: PR not eligible for review", "repo", repo, "pr", ev.Number)
		return
	}

	triggers, err := h.triggers.ListEnabledPRReviewTriggersByRepo(asyncCtx(30*time.Second), repo)
	if err != nil {
		slog.Error("webhook: list pr review triggers", "err", err, "repo", repo)
		return
	}

	h.submitReviewForTrigger(ReviewContext{
		Trigger:      triggerAction,
		Repo:         repo,
		PRNumber:     ev.Number,
		BaseRef:      ev.PullRequest.Base.Ref,
		HeadRef:      ev.PullRequest.Head.Ref,
		HeadCloneURL: ev.PullRequest.Head.Repo.CloneURL,
	}, triggers, triggerAction)
}

// triggerActionFromPR returns the trigger action string for a PR event, or empty if not eligible.
func triggerActionFromPR(ev PullRequestEvent, repo string) string {
	// Label trigger.
	for _, l := range ev.PullRequest.Labels {
		if l.Name == ChetterReviewLabel {
			return TriggerEventLabeled
		}
	}
	// Fork trigger.
	if ev.PullRequest.Head.Repo.FullName != "" && ev.PullRequest.Head.Repo.FullName != repo {
		return TriggerEventFork
	}
	// Opened trigger.
	if ev.Action == PullRequestActionOpened {
		return TriggerEventOpened
	}
	return ""
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

	ackCtx, ackCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer ackCancel()
	ackComment := fmt.Sprintf("@%s requested a review — Chetter is on it.", ev.Comment.User.Login)
	if err := h.gh.CreateIssueComment(ackCtx, repo, ev.Issue.Number, ackComment); err != nil {
		slog.Warn("webhook: post ack comment for comment trigger", "repo", repo, "pr", ev.Issue.Number, "err", err)
	}

	h.submitReviewForTrigger(ReviewContext{
		Trigger:       "comment",
		Repo:          repo,
		PRNumber:      ev.Issue.Number,
		BaseRef:       base,
		HeadRef:       head,
		HeadCloneURL:  cloneURL,
		CommentAuthor: ev.Comment.User.Login,
	}, nil, "comment")
}

// submitReviewForTrigger gets an installation token, filters triggers by event,
// and forwards the review context to the TaskSubmitter for each match.
// If triggers is nil, it fetches them from the resolver.
func (h *Handler) submitReviewForTrigger(ctx ReviewContext, triggers []ReviewTrigger, event string) {
	token, err := h.gh.tokenCache.get(h.gh)
	if err != nil {
		slog.Error("webhook: get GitHub token", "err", err)
		h.postCommentOnFailure(ctx, fmt.Sprintf("Chetter could not authenticate: %v", err))
		return
	}
	ctx.GitHubToken = token

	if triggers == nil {
		triggers, err = h.triggers.ListEnabledPRReviewTriggersByRepo(asyncCtx(30*time.Second), ctx.Repo)
		if err != nil {
			slog.Error("webhook: list pr review triggers", "err", err, "repo", ctx.Repo)
			h.postCommentOnFailure(ctx, CommentReviewFailed)
			return
		}
	}

	// Filter triggers by event. A trigger with no Event set matches all events.
	var matching []ReviewTrigger
	for _, t := range triggers {
		if t.Event == "" || t.Event == event {
			matching = append(matching, t)
		}
	}

	if len(matching) == 0 {
		slog.Info("webhook: no triggers match event", "event", event, "repo", ctx.Repo, "pr", ctx.PRNumber)
		return
	}

	// Add the review label to indicate a review is in progress.
	if ctx.Trigger != "label" {
		labelCtx, labelCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer labelCancel()
		has, err := h.gh.HasLabel(labelCtx, ctx.Repo, ctx.PRNumber, ChetterReviewLabel)
		if err != nil {
			slog.Warn("webhook: check label", "repo", ctx.Repo, "pr", ctx.PRNumber, "err", err)
		} else if !has {
			if err := h.gh.AddIssueLabel(labelCtx, ctx.Repo, ctx.PRNumber, ChetterReviewLabel); err != nil {
				slog.Warn("webhook: add label", "repo", ctx.Repo, "pr", ctx.PRNumber, "err", err)
			}
		}
	}

	for _, t := range matching {
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
		if err := h.submitter.SubmitReviewTask(asyncCtx(30*time.Second), rc); err != nil {
			slog.Error("webhook: submit review task", "err", err,
				"trigger", t.Name, "repo", rc.Repo, "pr", rc.PRNumber, "triggerType", rc.Trigger)
			h.postCommentOnFailure(rc, CommentReviewFailed)
			continue
		}
		slog.Info("webhook: review task submitted",
			"trigger", t.Name, "repo", rc.Repo, "pr", rc.PRNumber, "triggerType", rc.Trigger)
	}
}

// handleIssues handles an issues webhook event.
func (h *Handler) handleIssues(body []byte) {
	var ev IssueEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		slog.Warn("webhook: parse issues", "err", err)
		return
	}

	// Only act on the actions we care about.
	switch ev.Action {
	case "opened", "labeled", "reopened":
		// continue
	default:
		slog.Debug("webhook: ignoring issues action", "action", ev.Action)
		return
	}

	repo := ev.Repository.FullName
	triggers, err := h.triggers.ListEnabledIssueTriggersByRepo(asyncCtx(30*time.Second), repo)
	if err != nil {
		slog.Error("webhook: list issue triggers", "err", err, "repo", repo)
		return
	}

	// Filter triggers by event.
	var matching []ReviewTrigger
	for _, t := range triggers {
		if t.Event == "" || t.Event == ev.Action {
			matching = append(matching, t)
		}
	}
	if len(matching) == 0 {
		return
	}

	token, err := h.gh.tokenCache.get(h.gh)
	if err != nil {
		slog.Error("webhook: get GitHub token", "err", err)
		return
	}

	for _, t := range matching {
		prompt := t.Prompt
		if prompt == "" {
			prompt = fmt.Sprintf("A GitHub issue was %s in %s.\n\nTitle: %s\nURL: %s\n\nBody:\n%s",
				ev.Action, repo, ev.Issue.Title, ev.Issue.HTMLURL, ev.Issue.Body)
		}
		req := SubmitTaskRequest{
			Prompt:     prompt,
			GitURL:     t.GitURL,
			GitRef:     t.GitRef,
			AgentImage: t.AgentImage,
			Agent:      t.Agent,
			ProviderID: t.ProviderID,
			ModelID:    t.ModelID,
			VariantID:  t.VariantID,
			Skills:     t.Skills,
			TimeoutSec: t.TimeoutSec,
			Env: map[string]string{
				"GITHUB_TOKEN": token,
				"GITHUB_REPO":  repo,
				"ISSUE_NUMBER": fmt.Sprintf("%d", ev.Issue.Number),
				"ISSUE_TITLE":  ev.Issue.Title,
				"ISSUE_URL":    ev.Issue.HTMLURL,
				"ISSUE_ACTION": ev.Action,
			},
		}
		if _, err := h.submitter.SubmitTask(asyncCtx(30*time.Second), req); err != nil {
			slog.Error("webhook: submit issue task", "err", err,
				"trigger", t.Name, "repo", repo, "issue", ev.Issue.Number)
			continue
		}
		slog.Info("webhook: issue task submitted",
			"trigger", t.Name, "repo", repo, "issue", ev.Issue.Number, "action", ev.Action)
	}
}

func (h *Handler) postCommentOnFailure(ctx ReviewContext, body string) {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.gh.CreateIssueComment(c, ctx.Repo, ctx.PRNumber, body); err != nil {
		slog.Warn("webhook: post failure comment", "err", err)
	}
}

func asyncCtx(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	go func() { <-ctx.Done(); cancel() }()
	return ctx
}
