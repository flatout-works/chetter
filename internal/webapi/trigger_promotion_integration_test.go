package webapi

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	apiv1connect "github.com/flatout-works/chetter/gen/proto/api/v1/apiv1connect"
	"github.com/flatout-works/chetter/internal/store"
)

// TestWebAPIPromoteTriggerDryRun covers the ConnectRPC surface of the promotion
// flow: a draft created through CreateTrigger renders to canonical definition
// YAML through PromoteTrigger, without opening a pull request. This is the path
// the Web UI's promote panel uses, so it must work without a GitHub App.
func TestWebAPIPromoteTriggerDryRun(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()
	ctx := context.Background()
	triggers := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)

	if _, err := triggers.CreateTrigger(ctx, connect.NewRequest(&apiv1.CreateTriggerRequest{
		Name:        "draft-promote",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "0 3 * * *",
		Prompt:      "Reindex the corpus.",
		AgentImage:  "golang",
		Agent:       "docs-maintainer",
		Harness:     "claude-code",
		TimeoutSec:  3600,
	})); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	resp, err := triggers.PromoteTrigger(ctx, connect.NewRequest(&apiv1.PromoteTriggerRequest{
		Name:   "draft-promote",
		Scope:  "global",
		DryRun: true,
	}))
	if err != nil {
		t.Fatalf("PromoteTrigger: %v", err)
	}
	out := resp.Msg
	if !out.DryRun {
		t.Error("dry_run should be reported back")
	}
	if out.Path != "global/triggers/draft-promote.yaml" {
		t.Errorf("path = %q", out.Path)
	}
	if out.PrNumber != nil || out.PrUrl != nil {
		t.Error("a dry run must not report a pull request")
	}
	for _, want := range []string{
		"# yaml-language-server: $schema=../../../",
		"name: draft-promote",
		"cron_expr: \"0 3 * * *\"",
		"timeout_sec: 3600",
		"prompt: |-",
	} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("rendered content missing %q:\n%s", want, out.Content)
		}
	}
	// A promotion must never silently opt the definition into adopting.
	if strings.Contains(out.Content, "adopt") {
		t.Errorf("promotion should not emit adopt:\n%s", out.Content)
	}
}

// TestWebAPIPromoteTriggerValidation covers the user-correctable refusals. They
// must come back as FailedPrecondition with an actionable message rather than a
// generic internal error, because the UI renders them in an alert.
func TestWebAPIPromoteTriggerValidation(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()
	ctx := context.Background()
	triggers := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)

	cases := []struct {
		name    string
		req     *apiv1.PromoteTriggerRequest
		wantSub string
	}{
		{
			name:    "missing name",
			req:     &apiv1.PromoteTriggerRequest{Scope: "global"},
			wantSub: "name is required",
		},
		{
			name:    "unknown trigger",
			req:     &apiv1.PromoteTriggerRequest{Name: "nope", Scope: "global"},
			wantSub: "nope",
		},
		{
			name:    "team scope without team",
			req:     &apiv1.PromoteTriggerRequest{Name: "anything", Scope: "team"},
			wantSub: "team_name is required",
		},
		{
			name:    "repo scope without repo",
			req:     &apiv1.PromoteTriggerRequest{Name: "anything", Scope: "repo"},
			wantSub: "target_repo is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := triggers.PromoteTrigger(ctx, connect.NewRequest(tc.req))
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
				t.Errorf("code = %v, want FailedPrecondition", got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err, tc.wantSub)
			}
		})
	}
}
