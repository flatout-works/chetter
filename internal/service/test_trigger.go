package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/webhook"
)

// TestTriggerInput is the payload for a manual test run of an external-event
// trigger (pr_review or issue). The server fetches authoritative repository
// metadata from GitHub (PR refs, issue title/body/labels) rather than
// trusting editable client-supplied branch/ref fields, mirroring the real
// webhook dispatch path.
type TestTriggerInput struct {
	Name        string
	Repo        string
	PRNumber    int
	Event       string
	IssueNumber int
	Labels      []string
}

// TestTriggerOutput reports the task(s) created by a manual test run.
type TestTriggerOutput struct {
	TriggerName string
	TriggerType string
	TaskIDs     []string
	// Trigger is the resolved trigger record, included so API handlers can
	// return the full trigger without a second lookup.
	Trigger store.TriggerRecord
}

// testRunSubmissionSource marks tasks created by the manual trigger test flow
// so they can be distinguished from real webhook deliveries and cron runs.
const testRunSubmissionSource = "trigger_test"

// prReviewTestEvents are the pull_request actions the manual test flow can
// simulate. They correspond to the actions the webhook handler dispatches for
// (see triggerActionFromPR), excluding the derived "fork" and "comment"
// reasons.
var prReviewTestEvents = []string{"opened", "synchronize", "reopened", "labeled"}

// issueTestEvents are the issues actions the manual test flow can simulate.
// They correspond to the actions handleIssues dispatches for.
var issueTestEvents = []string{"opened", "reopened", "labeled"}

// TestTrigger manually invokes an external-event trigger with a synthetic
// event, reusing the same trigger resolution, matching, and task
// configuration as a real GitHub webhook delivery. Cron triggers are not
// supported here — use RunTriggerNow for those.
func (s *Service) TestTrigger(ctx context.Context, in TestTriggerInput) (TestTriggerOutput, error) {
	if in.Name == "" {
		return TestTriggerOutput{}, fmt.Errorf("name is required")
	}
	sch, err := s.triggerForToolAccess(ctx, in.Name)
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("get trigger: %w", err)
	}
	switch sch.TriggerType {
	case store.TriggerTypePRReview, store.TriggerTypeIssue:
	default:
		return TestTriggerOutput{}, fmt.Errorf("trigger %q is a %s trigger; test runs are only supported for pr_review and issue triggers (use Run Now for cron triggers)", in.Name, sch.TriggerType)
	}
	if s.github == nil {
		return TestTriggerOutput{}, fmt.Errorf("github app is not configured; cannot test %s trigger %q", sch.TriggerType, in.Name)
	}
	switch sch.TriggerType {
	case store.TriggerTypePRReview:
		out, err := s.testPRReviewTrigger(ctx, in)
		if err != nil {
			return TestTriggerOutput{}, err
		}
		out.Trigger = triggerToStoreRecord(sch)
		return out, nil
	case store.TriggerTypeIssue:
		out, err := s.testIssueTrigger(ctx, in)
		if err != nil {
			return TestTriggerOutput{}, err
		}
		out.Trigger = triggerToStoreRecord(sch)
		return out, nil
	}
	return TestTriggerOutput{}, fmt.Errorf("trigger %q is a %s trigger; test runs are only supported for pr_review and issue triggers", in.Name, sch.TriggerType)
}

// testPRReviewTrigger resolves the authoritative PR metadata from GitHub and
// submits a review task for every enabled pr_review trigger that matches the
// simulated event, exactly like the pull_request webhook path.
func (s *Service) testPRReviewTrigger(ctx context.Context, in TestTriggerInput) (TestTriggerOutput, error) {
	if in.Repo == "" {
		return TestTriggerOutput{}, fmt.Errorf("repo is required to test pr_review trigger %q", in.Name)
	}
	if in.PRNumber <= 0 {
		return TestTriggerOutput{}, fmt.Errorf("pr_number is required to test pr_review trigger %q", in.Name)
	}
	if !containsTestEvent(prReviewTestEvents, in.Event) {
		return TestTriggerOutput{}, fmt.Errorf("event %q is not supported for pr_review test runs (supported: %s)", in.Event, strings.Join(prReviewTestEvents, ", "))
	}

	// Resolve the repository installation and fetch authoritative PR metadata
	// from GitHub instead of trusting editable branch/ref fields.
	gh, err := s.github.ClientForRepo(ctx, in.Repo)
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("resolve GitHub installation for %s: %w", in.Repo, err)
	}
	headRef, baseRef, cloneURL, err := gh.GetPullRequest(ctx, in.Repo, in.PRNumber)
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("fetch pull request %s#%d: %w", in.Repo, in.PRNumber, err)
	}

	triggers, err := s.ListEnabledPRReviewTriggersByRepo(ctx, in.Repo)
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("list pr review triggers: %w", err)
	}
	trigger, ok := findReviewTrigger(triggers, in.Name)
	if !ok {
		return TestTriggerOutput{}, fmt.Errorf("trigger %q is disabled or does not watch repo %q", in.Name, in.Repo)
	}
	if trigger.Event != "" && trigger.Event != in.Event {
		return TestTriggerOutput{}, fmt.Errorf("trigger %q does not respond to %q events (configured event: %q)", in.Name, in.Event, trigger.Event)
	}

	// Mirror the webhook's ReviewContext construction (submitReviewForTrigger):
	// trigger-supplied prompt/agent/model/skills/timeout win, and the PR's
	// head branch is always authoritative for pr_review triggers.
	rc := webhook.ReviewContext{
		Trigger:              in.Event,
		Repo:                 in.Repo,
		PRNumber:             in.PRNumber,
		BaseRef:              baseRef,
		HeadRef:              headRef,
		HeadCloneURL:         cloneURL,
		GitHubInstallationID: gh.InstallationID,
		Prompt:               trigger.Prompt,
		AgentImage:           trigger.AgentImage,
		Agent:                trigger.Agent,
		ProviderID:           trigger.ProviderID,
		ModelID:              trigger.ModelID,
		VariantID:            trigger.VariantID,
		Skills:               trigger.Skills,
		TimeoutSec:           trigger.TimeoutSec,
		TeamID:               trigger.TeamID,
		TriggerName:          trigger.Name,
		TriggerType:          trigger.TriggerType,
		SessionMode:          trigger.SessionMode,
		PauseReason:          trigger.PauseReason,
		TTLHours:             trigger.TTLHours,
		Isolation:            trigger.Isolation,
	}
	req := webhook.BuildReviewTaskRequest(rc)
	task, err := s.SubmitTask(ctx, webhookTestRequestToService(req))
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("submit test review task: %w", err)
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:    "trigger_test_run",
		SourceType:   "api",
		SourceID:     in.Name,
		TargetType:   "task",
		TargetID:     task.ID,
		Repo:         in.Repo,
		GitHubEvent:  "pr_review",
		GitHubAction: in.Event,
		Detail:       fmt.Sprintf("manual test run of pr_review trigger %q (%s/%s on %s#%d)", in.Name, "pr_review", in.Event, in.Repo, in.PRNumber),
	})
	return TestTriggerOutput{
		TriggerName: trigger.Name,
		TriggerType: trigger.TriggerType,
		TaskIDs:     []string{task.ID},
	}, nil
}

// testIssueTrigger resolves the authoritative issue metadata from GitHub and
// submits a task for the selected issue trigger when the simulated event and
// labels match, exactly like the issues webhook path.
func (s *Service) testIssueTrigger(ctx context.Context, in TestTriggerInput) (TestTriggerOutput, error) {
	if in.Repo == "" {
		return TestTriggerOutput{}, fmt.Errorf("repo is required to test issue trigger %q", in.Name)
	}
	if in.IssueNumber <= 0 {
		return TestTriggerOutput{}, fmt.Errorf("issue_number is required to test issue trigger %q", in.Name)
	}
	if !containsTestEvent(issueTestEvents, in.Event) {
		return TestTriggerOutput{}, fmt.Errorf("event %q is not supported for issue test runs (supported: %s)", in.Event, strings.Join(issueTestEvents, ", "))
	}

	gh, err := s.github.ClientForRepo(ctx, in.Repo)
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("resolve GitHub installation for %s: %w", in.Repo, err)
	}
	issue, err := gh.GetIssueDetails(ctx, in.Repo, in.IssueNumber)
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("fetch issue %s#%d: %w", in.Repo, in.IssueNumber, err)
	}

	triggers, err := s.ListEnabledIssueTriggersByRepo(ctx, in.Repo)
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("list issue triggers: %w", err)
	}
	trigger, ok := findReviewTrigger(triggers, in.Name)
	if !ok {
		return TestTriggerOutput{}, fmt.Errorf("trigger %q is disabled or does not watch repo %q", in.Name, in.Repo)
	}
	if trigger.Event != "" && trigger.Event != in.Event {
		return TestTriggerOutput{}, fmt.Errorf("trigger %q does not respond to %q events (configured event: %q)", in.Name, in.Event, trigger.Event)
	}
	// Simulated labels win over the issue's actual labels so label-matching
	// triggers can be tested against hypothetical label sets.
	issueLabels := issue.Labels
	if len(in.Labels) > 0 {
		issueLabels = in.Labels
	}
	if !webhook.TriggerMatchesLabels(trigger.MatchLabels, issueLabels) {
		return TestTriggerOutput{}, fmt.Errorf("trigger %q requires one of labels %v; issue %s#%d has %v", in.Name, trigger.MatchLabels, in.Repo, in.IssueNumber, issueLabels)
	}

	prompt := trigger.Prompt
	if prompt == "" {
		prompt = fmt.Sprintf("A GitHub issue was %s in %s.\n\nTitle: %s\nURL: %s\n\nBody:\n%s",
			in.Event, in.Repo, issue.Title, issue.HTMLURL, issue.Body)
	}
	req := webhook.BuildIssueTaskRequest(trigger, in.Repo, gh.InstallationID, prompt, map[string]string{
		"GITHUB_REPO":  in.Repo,
		"ISSUE_NUMBER": fmt.Sprintf("%d", in.IssueNumber),
		"ISSUE_TITLE":  issue.Title,
		"ISSUE_URL":    issue.HTMLURL,
		"ISSUE_BODY":   issue.Body,
		"ISSUE_ACTION": in.Event,
	})
	task, err := s.SubmitTask(ctx, webhookTestRequestToService(req))
	if err != nil {
		return TestTriggerOutput{}, fmt.Errorf("submit test issue task: %w", err)
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:    "trigger_test_run",
		SourceType:   "api",
		SourceID:     in.Name,
		TargetType:   "task",
		TargetID:     task.ID,
		Repo:         in.Repo,
		GitHubEvent:  "issue",
		GitHubAction: in.Event,
		Detail:       fmt.Sprintf("manual test run of issue trigger %q (%s/%s on %s#%d)", in.Name, "issue", in.Event, in.Repo, in.IssueNumber),
	})
	return TestTriggerOutput{
		TriggerName: trigger.Name,
		TriggerType: trigger.TriggerType,
		TaskIDs:     []string{task.ID},
	}, nil
}

// findReviewTrigger returns the enabled trigger with the given name.
func findReviewTrigger(triggers []webhook.ReviewTrigger, name string) (webhook.ReviewTrigger, bool) {
	for _, t := range triggers {
		if t.Name == name {
			return t, true
		}
	}
	return webhook.ReviewTrigger{}, false
}

func containsTestEvent(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// webhookTestRequestToService converts the webhook package's task request
// (produced by the shared BuildReviewTaskRequest/BuildIssueTaskRequest
// builders) into the service-side request, stamping the manual test-run
// submission source so test-originated tasks are auditable.
func webhookTestRequestToService(req webhook.SubmitTaskRequest) SubmitTaskRequest {
	return SubmitTaskRequest{
		TeamID:               req.TeamID,
		Prompt:               req.Prompt,
		GitURL:               req.GitURL,
		GitRef:               req.GitRef,
		GitHubRepo:           req.GitHubRepo,
		GitHubInstallationID: req.GitHubInstallationID,
		AgentImage:           req.AgentImage,
		Agent:                req.Agent,
		ProviderID:           req.ProviderID,
		ModelID:              req.ModelID,
		VariantID:            req.VariantID,
		Skills:               req.Skills,
		Env:                  req.Env,
		TimeoutSec:           req.TimeoutSec,
		TriggerName:          req.TriggerName,
		TriggerType:          req.TriggerType,
		SubmissionSource:     testRunSubmissionSource,
		SessionMode:          req.SessionMode,
		PauseReason:          req.PauseReason,
		TTLHours:             req.TTLHours,
		Isolation:            req.Isolation,
	}
}
