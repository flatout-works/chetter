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
	"regexp"
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

// AuditLogger is the interface for recording server-side audit events.
type AuditLogger interface {
	LogAuditEvent(ctx context.Context, params AuditEventParams) error
}

// AuditEventParams holds the data for a single audit log entry.
type AuditEventParams struct {
	EventType        string
	SourceType       string
	SourceID         string
	TargetType       string
	TargetID         string
	Repo             string
	GitHubEvent      string
	GitHubAction     string
	GitHubDeliveryID string
	ParentEventID    string
	Detail           string
	Payload          json.RawMessage
}

// ArtifactRecorder is the interface for recording task artifacts discovered
// from webhook events (issues, PRs, comments with Chetter footer signatures).
type ArtifactRecorder interface {
	RecordArtifact(ctx context.Context, params RecordArtifactParams) error
}

// RecordArtifactParams holds the data for a single task artifact entry.
type RecordArtifactParams struct {
	TaskID          string
	AgentSessionID  string
	SessionRunID    string
	ArtifactType    string
	Repo            string
	Number          int
	URL             string
	Ref             string
	SHA             string
	DiscoverySource string
}

// ReviewTrigger is the resolved data from a single trigger.
type ReviewTrigger struct {
	TeamID      string
	Name        string
	TriggerType string
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
	Event       string   // which webhook action this trigger responds to (e.g. "opened", "labeled"), empty = all
	MatchLabels []string // required issue labels; empty = all labels match
	SessionMode string
	PauseReason string
	TTLHours    int
}

// TaskSubmitter is the subset of service.Service that the webhook needs to
// submit tasks. Defined as an interface to allow mocking in tests.
type TaskSubmitter interface {
	SubmitReviewTask(ctx context.Context, review ReviewContext) error
	SubmitTask(ctx context.Context, req SubmitTaskRequest) (any, error)
}

// SessionResumer is the interface for resuming paused agent sessions.
type SessionResumer interface {
	ResumeSessionForPR(ctx context.Context, repo string, prNumber int) error
}

// ReviewContext is the data passed to TaskSubmitter for a single review.
type ReviewContext struct {
	TeamID        string
	TriggerName   string
	TriggerType   string
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
	SessionMode   string
	PauseReason   string
	TTLHours      int
}

// Handler serves GitHub webhook events. Implements http.Handler.
type Handler struct {
	cfg       HandlerConfig
	gh        *Client
	submitter TaskSubmitter
	triggers  TriggerResolver
	audit     AuditLogger
	artifacts ArtifactRecorder
	resumer   SessionResumer
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
func NewHandler(cfg HandlerConfig, gh *Client, submitter TaskSubmitter, triggers TriggerResolver, audit AuditLogger, artifacts ArtifactRecorder, resumer SessionResumer) *Handler {
	return &Handler{
		cfg:       cfg,
		gh:        gh,
		submitter: submitter,
		triggers:  triggers,
		audit:     audit,
		artifacts: artifacts,
		resumer:   resumer,
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

	go h.handle(event, body, deliveryID)
}

func (h *Handler) handle(event string, body []byte, deliveryID string) {
	switch event {
	case EventTypePullRequest:
		h.handlePullRequest(body, deliveryID)
	case EventTypeIssueComment:
		h.handleIssueComment(body, deliveryID)
	case EventTypeIssues:
		h.handleIssues(body, deliveryID)
	case EventTypePullRequestReview:
		h.handlePullRequestReview(body, deliveryID)
	case EventTypePullRequestReviewComment:
		h.handlePullRequestReviewComment(body, deliveryID)
	default:
		slog.Debug("webhook: ignoring unsupported event", "event", event)
	}
}

func (h *Handler) handlePullRequestReview(body []byte, deliveryID string) {
	var ev PullRequestReviewEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		slog.Warn("webhook: parse pull_request_review", "err", err)
		return
	}
	if ev.Action != "submitted" {
		return
	}
	h.resumeSessionForPRFeedback(ev.Repository.FullName, ev.PullRequest.Number, ev.Review.User.Login, deliveryID, EventTypePullRequestReview, ev.Action)
}

func (h *Handler) handlePullRequestReviewComment(body []byte, deliveryID string) {
	var ev PullRequestReviewCommentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		slog.Warn("webhook: parse pull_request_review_comment", "err", err)
		return
	}
	if ev.Action != "created" {
		return
	}
	h.resumeSessionForPRFeedback(ev.Repository.FullName, ev.PullRequest.Number, ev.Comment.User.Login, deliveryID, EventTypePullRequestReviewComment, ev.Action)
}

func (h *Handler) resumeSessionForPRFeedback(repo string, prNumber int, author, deliveryID, eventType, action string) {
	if h.resumer == nil || repo == "" || prNumber <= 0 {
		return
	}
	if author != "" {
		appLogin, _ := h.gh.GetAppLogin(asyncCtx(15 * time.Second))
		if appLogin != "" && author == appLogin {
			slog.Info("webhook: skipping Chetter app review feedback", "repo", repo, "pr", prNumber, "event", eventType)
			return
		}
	}
	h.logAudit(AuditEventParams{
		EventType:        "webhook_received",
		SourceType:       "webhook",
		SourceID:         deliveryID,
		TargetType:       "pull_request",
		TargetID:         fmt.Sprintf("%s#%d", repo, prNumber),
		Repo:             repo,
		GitHubEvent:      eventType,
		GitHubAction:     action,
		GitHubDeliveryID: deliveryID,
		Detail:           fmt.Sprintf("%s/%s for %s#%d", eventType, action, repo, prNumber),
	})
	if err := h.resumer.ResumeSessionForPR(asyncCtx(30*time.Second), repo, prNumber); err != nil {
		slog.Warn("webhook: resume session for pr feedback", "err", err, "repo", repo, "pr", prNumber, "event", eventType)
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

func (h *Handler) handlePullRequest(body []byte, deliveryID string) {
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

	// Gate fork and opened triggers on author write access.
	if triggerAction == TriggerEventFork || triggerAction == TriggerEventOpened {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if !h.checkAuthorWriteAccess(ctx, repo, ev.PullRequest.User.Login, deliveryID) {
			return
		}
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

func (h *Handler) handleIssueComment(body []byte, deliveryID string) {
	var ev IssueCommentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		slog.Warn("webhook: parse issue_comment", "err", err)
		return
	}
	if ev.Action != "created" {
		return
	}

	repo := ev.Repository.FullName

	h.logAudit(AuditEventParams{
		EventType:        "webhook_received",
		SourceType:       "webhook",
		SourceID:         deliveryID,
		TargetType:       "issue",
		TargetID:         fmt.Sprintf("%s#%d", repo, ev.Issue.Number),
		Repo:             repo,
		GitHubEvent:      EventTypeIssueComment,
		GitHubAction:     ev.Action,
		GitHubDeliveryID: deliveryID,
		Detail:           fmt.Sprintf("issue_comment/%s for %s#%d", ev.Action, repo, ev.Issue.Number),
	})

	h.discoverArtifacts(ev.Comment.Body, repo, ev.Issue.Number, ev.Issue.HTMLURL, "issue_comment")

	if ev.IsPullRequest() && h.resumer != nil {
		if err := h.resumer.ResumeSessionForPR(asyncCtx(30*time.Second), repo, ev.Issue.Number); err != nil {
			slog.Warn("webhook: resume session for pr", "err", err, "repo", repo, "pr", ev.Issue.Number)
		}
	}

	if ev.IsPullRequest() {
		// PR comment — handle /chetter-review trigger.
		if strings.TrimSpace(ev.Comment.Body) != ReviewTriggerCommand {
			return
		}
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
		return
	}

	// Issue comment — check for issue triggers with event "comment".
	triggers, err := h.triggers.ListEnabledIssueTriggersByRepo(asyncCtx(30*time.Second), repo)
	if err != nil {
		slog.Error("webhook: list issue triggers for comment", "err", err, "repo", repo)
		return
	}
	// Extract issue label names.
	issueLabels := make([]string, len(ev.Issue.Labels))
	for i, lbl := range ev.Issue.Labels {
		issueLabels[i] = lbl.Name
	}

	var matching []ReviewTrigger
	for _, t := range triggers {
		if t.Event != "" && t.Event != "comment" {
			continue
		}
		if !triggerMatchesLabels(t.MatchLabels, issueLabels) {
			continue
		}
		matching = append(matching, t)
	}
	if len(matching) == 0 {
		return
	}

	// Gate issue comment triggers on author write access.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if !h.checkAuthorWriteAccess(ctx, repo, ev.Comment.User.Login, deliveryID) {
			return
		}
	}

	// Bot-comment filtering: skip comments from the Chetter App itself unless
	// the trigger explicitly allows bot comments.
	appLogin, _ := h.gh.GetAppLogin(asyncCtx(15 * time.Second))
	isBotComment := appLogin != "" && ev.Comment.User.Login == appLogin

	token, err := h.gh.tokenCache.get(h.gh)
	if err != nil {
		slog.Error("webhook: get GitHub token for issue comment", "err", err)
		return
	}
	for _, t := range matching {
		if isBotComment && !triggerAllowsBotComments(t) {
			slog.Info("webhook: skipping bot comment for trigger", "trigger", t.Name, "issue", ev.Issue.Number)
			continue
		}
		prompt := t.Prompt
		if prompt == "" {
			prompt = fmt.Sprintf(
				"A comment was added to issue #%d in %s.\n\nTitle: %s\nURL: %s\n\nComment by %s:\n%s",
				ev.Issue.Number, repo, ev.Issue.Title, ev.Issue.HTMLURL,
				ev.Comment.User.Login, ev.Comment.Body)
		}
		req := SubmitTaskRequest{
			TeamID:      t.TeamID,
			Prompt:      prompt,
			GitURL:      t.GitURL,
			GitRef:      t.GitRef,
			AgentImage:  t.AgentImage,
			Agent:       t.Agent,
			ProviderID:  t.ProviderID,
			ModelID:     t.ModelID,
			VariantID:   t.VariantID,
			Skills:      t.Skills,
			TimeoutSec:  t.TimeoutSec,
			TriggerName: t.Name,
			TriggerType: t.TriggerType,
			SessionMode: t.SessionMode,
			PauseReason: t.PauseReason,
			TTLHours:    t.TTLHours,
			Env: map[string]string{
				"GITHUB_TOKEN": token,
				"GITHUB_REPO":  repo,
				"ISSUE_NUMBER": fmt.Sprintf("%d", ev.Issue.Number),
				"ISSUE_TITLE":  ev.Issue.Title,
				"ISSUE_URL":    ev.Issue.HTMLURL,
				"ISSUE_BODY":   ev.Issue.Body,
				"COMMENT_BODY": ev.Comment.Body,
				"COMMENT_USER": ev.Comment.User.Login,
			},
		}
		if _, err := h.submitter.SubmitTask(asyncCtx(30*time.Second), req); err != nil {
			slog.Error("webhook: submit issue comment task", "err", err,
				"trigger", t.Name, "repo", repo, "issue", ev.Issue.Number)
			h.logAudit(AuditEventParams{
				EventType:        "task_submit_failed",
				SourceType:       "trigger",
				SourceID:         t.Name,
				TargetType:       "issue",
				TargetID:         fmt.Sprintf("%s#%d", repo, ev.Issue.Number),
				Repo:             repo,
				GitHubDeliveryID: deliveryID,
				Detail:           fmt.Sprintf("failed to submit task for issue #%d via trigger %s: %v", ev.Issue.Number, t.Name, err),
			})
			continue
		}
		slog.Info("webhook: issue comment task submitted",
			"trigger", t.Name, "repo", repo, "issue", ev.Issue.Number)
		h.logAudit(AuditEventParams{
			EventType:        "task_submitted",
			SourceType:       "trigger",
			SourceID:         t.Name,
			TargetType:       "issue",
			TargetID:         fmt.Sprintf("%s#%d", repo, ev.Issue.Number),
			Repo:             repo,
			GitHubDeliveryID: deliveryID,
			Detail:           fmt.Sprintf("task submitted for issue #%d via trigger %s (bot_comment=%v)", ev.Issue.Number, t.Name, isBotComment),
		})
	}
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
		rc.TeamID = t.TeamID
		rc.TriggerName = t.Name
		rc.TriggerType = t.TriggerType
		rc.Agent = t.Agent
		rc.ProviderID = t.ProviderID
		rc.ModelID = t.ModelID
		rc.VariantID = t.VariantID
		rc.TimeoutSec = t.TimeoutSec
		rc.SessionMode = t.SessionMode
		rc.PauseReason = t.PauseReason
		rc.TTLHours = t.TTLHours
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
func (h *Handler) handleIssues(body []byte, deliveryID string) {
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

	h.logAudit(AuditEventParams{
		EventType:        "webhook_received",
		SourceType:       "webhook",
		SourceID:         deliveryID,
		TargetType:       "issue",
		TargetID:         fmt.Sprintf("%s#%d", repo, ev.Issue.Number),
		Repo:             repo,
		GitHubEvent:      EventTypeIssues,
		GitHubAction:     ev.Action,
		GitHubDeliveryID: deliveryID,
		Detail:           fmt.Sprintf("issues/%s for %s#%d", ev.Action, repo, ev.Issue.Number),
	})

	if ev.Action == "opened" {
		h.discoverArtifacts(ev.Issue.Body, repo, ev.Issue.Number, ev.Issue.HTMLURL, "issue")
	}

	// Gate issue triggers on author write access.
	if ev.Action == "opened" || ev.Action == "reopened" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if !h.checkAuthorWriteAccess(ctx, repo, ev.Issue.User.Login, deliveryID) {
			return
		}
	}

	triggers, err := h.triggers.ListEnabledIssueTriggersByRepo(asyncCtx(30*time.Second), repo)
	if err != nil {
		slog.Error("webhook: list issue triggers", "err", err, "repo", repo)
		return
	}

	// Extract issue label names. For labeled events, compare against the label
	// that was just added so bug-label triggers don't re-fire for unrelated labels.
	issueLabels := make([]string, len(ev.Issue.Labels))
	for i, lbl := range ev.Issue.Labels {
		issueLabels[i] = lbl.Name
	}
	if ev.Action == "labeled" && ev.Label != nil {
		issueLabels = []string{ev.Label.Name}
	}

	// Filter triggers by event and labels.
	var matching []ReviewTrigger
	for _, t := range triggers {
		if t.Event != "" && t.Event != ev.Action {
			continue
		}
		if !triggerMatchesLabels(t.MatchLabels, issueLabels) {
			continue
		}
		matching = append(matching, t)
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
			TeamID:      t.TeamID,
			Prompt:      prompt,
			GitURL:      t.GitURL,
			GitRef:      t.GitRef,
			AgentImage:  t.AgentImage,
			Agent:       t.Agent,
			ProviderID:  t.ProviderID,
			ModelID:     t.ModelID,
			VariantID:   t.VariantID,
			Skills:      t.Skills,
			TimeoutSec:  t.TimeoutSec,
			TriggerName: t.Name,
			TriggerType: t.TriggerType,
			SessionMode: t.SessionMode,
			PauseReason: t.PauseReason,
			TTLHours:    t.TTLHours,
			Env: map[string]string{
				"GITHUB_TOKEN": token,
				"GITHUB_REPO":  repo,
				"ISSUE_NUMBER": fmt.Sprintf("%d", ev.Issue.Number),
				"ISSUE_TITLE":  ev.Issue.Title,
				"ISSUE_URL":    ev.Issue.HTMLURL,
				"ISSUE_BODY":   ev.Issue.Body,
				"ISSUE_ACTION": ev.Action,
			},
		}
		if _, err := h.submitter.SubmitTask(asyncCtx(30*time.Second), req); err != nil {
			slog.Error("webhook: submit issue task", "err", err,
				"trigger", t.Name, "repo", repo, "issue", ev.Issue.Number)
			h.logAudit(AuditEventParams{
				EventType:        "task_submit_failed",
				SourceType:       "trigger",
				SourceID:         t.Name,
				TargetType:       "issue",
				TargetID:         fmt.Sprintf("%s#%d", repo, ev.Issue.Number),
				Repo:             repo,
				GitHubDeliveryID: deliveryID,
				Detail:           fmt.Sprintf("failed to submit task for issue #%d via trigger %s: %v", ev.Issue.Number, t.Name, err),
			})
			continue
		}
		slog.Info("webhook: issue task submitted",
			"trigger", t.Name, "repo", repo, "issue", ev.Issue.Number, "action", ev.Action)
		h.logAudit(AuditEventParams{
			EventType:        "task_submitted",
			SourceType:       "trigger",
			SourceID:         t.Name,
			TargetType:       "issue",
			TargetID:         fmt.Sprintf("%s#%d", repo, ev.Issue.Number),
			Repo:             repo,
			GitHubDeliveryID: deliveryID,
			Detail:           fmt.Sprintf("task submitted for issue #%d via trigger %s on action %s", ev.Issue.Number, t.Name, ev.Action),
		})
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

func (h *Handler) logAudit(params AuditEventParams) {
	if h.audit == nil {
		return
	}
	if err := h.audit.LogAuditEvent(asyncCtx(10*time.Second), params); err != nil {
		slog.Warn("webhook: log audit event", "err", err, "event_type", params.EventType)
	}
}

// checkAuthorWriteAccess returns true if the user has write or admin access to
// the repo. If the check fails or the user lacks access, it logs a message and
// returns false so the caller can abort processing.
func (h *Handler) checkAuthorWriteAccess(ctx context.Context, repo, username, deliveryID string) bool {
	hasAccess, err := h.gh.CheckUserHasWriteAccess(ctx, repo, username)
	if err != nil {
		slog.Warn("webhook: check write access", "user", username, "err", err, "repo", repo)
		return false
	}
	if !hasAccess {
		slog.Info("webhook: ignoring trigger from non-writer", "user", username, "repo", repo)
		h.logAudit(AuditEventParams{
			EventType:        "webhook_author_gate_denied",
			SourceType:       "webhook",
			SourceID:         deliveryID,
			TargetType:       "repo",
			TargetID:         repo,
			Repo:             repo,
			GitHubEvent:      "author_gate",
			GitHubDeliveryID: deliveryID,
			Detail:           fmt.Sprintf("user %s lacks write access to %s", username, repo),
		})
		return false
	}
	return true
}

var taskIDFooterRe = regexp.MustCompile(`Task:\s*(task_[a-f0-9]+)`)
var agentSessionIDFooterRe = regexp.MustCompile(`Session:\s*(sess_[a-f0-9]+)`)
var sessionRunIDFooterRe = regexp.MustCompile(`Run:\s*(run_[a-f0-9]+)`)

func (h *Handler) discoverArtifacts(text, repo string, number int, url, artifactType string) {
	if h.artifacts == nil {
		return
	}
	matches := taskIDFooterRe.FindStringSubmatch(text)
	if len(matches) < 2 {
		return
	}
	taskID := matches[1]
	agentSessionID := ""
	if sessionMatches := agentSessionIDFooterRe.FindStringSubmatch(text); len(sessionMatches) >= 2 {
		agentSessionID = sessionMatches[1]
	}
	sessionRunID := ""
	if runMatches := sessionRunIDFooterRe.FindStringSubmatch(text); len(runMatches) >= 2 {
		sessionRunID = runMatches[1]
	}
	if err := h.artifacts.RecordArtifact(asyncCtx(10*time.Second), RecordArtifactParams{
		TaskID:          taskID,
		AgentSessionID:  agentSessionID,
		SessionRunID:    sessionRunID,
		ArtifactType:    artifactType,
		Repo:            repo,
		Number:          number,
		URL:             url,
		DiscoverySource: "webhook",
	}); err != nil {
		slog.Warn("webhook: record artifact", "err", err, "taskID", taskID, "type", artifactType)
	}
}

func triggerAllowsBotComments(t ReviewTrigger) bool {
	return strings.Contains(t.Event, "bot_comments:true")
}

// triggerMatchesLabels checks if any of the issue's labels match the trigger's
// required labels. If the trigger has no match_labels, all issues match.
func triggerMatchesLabels(triggerLabels, issueLabels []string) bool {
	if len(triggerLabels) == 0 {
		return true
	}
	for _, req := range triggerLabels {
		for _, lbl := range issueLabels {
			if strings.EqualFold(req, lbl) {
				return true
			}
		}
	}
	return false
}
