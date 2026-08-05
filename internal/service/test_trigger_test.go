package service

import (
	"testing"

	"github.com/flatout-works/chetter/internal/webhook"
)

func TestFindReviewTrigger(t *testing.T) {
	triggers := []webhook.ReviewTrigger{
		{Name: "alpha", TriggerType: "pr_review"},
		{Name: "beta", TriggerType: "issue"},
	}
	got, ok := findReviewTrigger(triggers, "beta")
	if !ok || got.Name != "beta" {
		t.Fatalf("findReviewTrigger(beta) = %+v, %v; want beta, true", got, ok)
	}
	if _, ok := findReviewTrigger(triggers, "missing"); ok {
		t.Fatal("findReviewTrigger(missing) should not match")
	}
}

func TestWebhookTestRequestToServiceStampsTestSource(t *testing.T) {
	req := webhookTestRequestToService(webhook.SubmitTaskRequest{
		TeamID:               "team_1",
		Prompt:               "review",
		GitURL:               "https://github.com/acme/one.git",
		GitRef:               "feature/x",
		GitHubRepo:           "acme/one",
		GitHubInstallationID: 111,
		AgentImage:           "runner:latest",
		Agent:                "pr-reviewer",
		ProviderID:           "opencode",
		ModelID:              "minimax-m3",
		VariantID:            "high",
		Skills:               []string{"review"},
		Env:                  map[string]string{"PR_NUMBER": "42"},
		TimeoutSec:           3600,
		TriggerName:          "deep-review",
		TriggerType:          "pr_review",
		SessionMode:          "none",
		PauseReason:          "",
		TTLHours:             0,
		Isolation:            "required",
	})
	if req.SubmissionSource != testRunSubmissionSource {
		t.Fatalf("submission source = %q, want %q", req.SubmissionSource, testRunSubmissionSource)
	}
	if req.TriggerName != "deep-review" || req.GitHubRepo != "acme/one" || req.GitHubInstallationID != 111 {
		t.Fatalf("field mapping mismatch: %+v", req)
	}
	if req.Isolation != "required" || req.SessionMode != "none" {
		t.Fatalf("runtime fields not mapped: %+v", req)
	}
}

func TestTriggerTestEvents(t *testing.T) {
	for _, ev := range prReviewTestEvents {
		if !containsTestEvent(prReviewTestEvents, ev) {
			t.Fatalf("prReviewTestEvents missing %q", ev)
		}
	}
	for _, ev := range issueTestEvents {
		if !containsTestEvent(issueTestEvents, ev) {
			t.Fatalf("issueTestEvents missing %q", ev)
		}
	}
	// Derived webhook reasons are not user-selectable test events.
	if containsTestEvent(prReviewTestEvents, "fork") || containsTestEvent(prReviewTestEvents, "comment") {
		t.Fatal("pr_review test events must not include derived fork/comment reasons")
	}
}
