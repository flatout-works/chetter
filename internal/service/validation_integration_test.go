package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/validation"
)

// TestSubmitTaskValidation_RejectsUnsupportedHarness exercises the central
// task validation shared by the MCP, ConnectRPC/web UI, and webhook ingress
// paths (all funnel through Service.SubmitTask). An unknown harness must be
// rejected and not persisted rather than silently falling back to OpenCode.
func TestSubmitTaskValidation_RejectsUnsupportedHarness(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	_, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:     "harness check",
		AgentImage: "runner:latest",
		Harness:    "bogus-harness",
	})
	if err == nil {
		t.Fatal("expected error for unsupported harness")
	}
	if !strings.Contains(err.Error(), "harness") {
		t.Fatalf("error should identify the harness field: %v", err)
	}
	// Invalid requests are not persisted.
	tasks, err := svc.ListTasks(ctx, "", 100, 0, "", "", nil, nil)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("invalid task must not be persisted; got %d tasks", len(tasks))
	}
	_ = tdb // keep tdb reference for clarity; truncated by harness
}

func TestSubmitTaskValidation_RejectsUnsupportedSessionMode(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{
		Prompt:      "mode check",
		AgentImage:  "runner:latest",
		SessionMode: "persistent",
	})
	if err == nil || !strings.Contains(err.Error(), "session_mode") {
		t.Fatalf("expected session_mode error, got %v", err)
	}
}

func TestSubmitTaskValidation_RejectsNegativeTimeout(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.SubmitTask(context.Background(), SubmitTaskRequest{
		Prompt:     "timeout check",
		AgentImage: "runner:latest",
		TimeoutSec: -10,
	})
	if err == nil || !strings.Contains(err.Error(), "timeout_sec") {
		t.Fatalf("expected timeout_sec error, got %v", err)
	}
}

// TestSubmitTaskValidation_AcceptsSupportedHarnesses confirms every
// supported harness value (and the empty default) is accepted and persisted.
func TestSubmitTaskValidation_AcceptsSupportedHarnesses(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	for _, h := range append([]string{""}, validation.SupportedHarnesses...) {
		rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
			Prompt:     "harness " + h,
			AgentImage: "runner:latest",
			Harness:    h,
		})
		if err != nil {
			t.Errorf("harness %q should be accepted: %v", h, err)
			continue
		}
		// The stored harness echoes the requested value (empty stays empty and
		// is defaulted by the runner, not the server).
		row, err := data.New(tdb.DB, tdb.Dialect()).GetAgentSessionByTaskID(ctx, rec.ID)
		if err != nil {
			t.Errorf("GetAgentSessionByTaskID(%s): %v", rec.ID, err)
			continue
		}
		if row.Harness.String != h {
			t.Errorf("harness %q: stored %q", h, row.Harness.String)
		}
	}
}

// TestCreateTriggerValidation_RejectsUnsupportedHarness covers the trigger
// create ingress path (MCP and web UI both funnel through Service.CreateTrigger).
func TestCreateTriggerValidation_RejectsUnsupportedHarness(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	_, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:        "bad-harness",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "x",
		AgentImage:  "runner:latest",
		Harness:     "bogus",
		TimeoutSec:  60,
	})
	if err == nil || !strings.Contains(err.Error(), "harness") {
		t.Fatalf("expected harness error, got %v", err)
	}
}

func TestCreateTriggerValidation_RejectsBadRepoSyntax(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	for _, tt := range []string{store.TriggerTypePRReview, store.TriggerTypeIssue} {
		cfg, _ := json.Marshal(map[string]any{"repo": "https://github.com/o/r"})
		_, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
			Name:          "bad-repo-" + tt,
			TriggerType:   tt,
			TriggerConfig: string(cfg),
			AgentImage:    "runner:latest",
			TimeoutSec:    60,
		})
		if err == nil || !strings.Contains(err.Error(), "repo") {
			t.Errorf("%s: expected repo syntax error, got %v", tt, err)
		}
	}
}

func TestCreateTriggerValidation_AcceptsCanonicalRepo(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	cfg, _ := json.Marshal(map[string]any{"repo": "flatout-works/chetter"})
	if _, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:          "good-repo",
		TriggerType:   store.TriggerTypePRReview,
		TriggerConfig: string(cfg),
		AgentImage:    "runner:latest",
		TimeoutSec:    60,
	}); err != nil {
		t.Fatalf("canonical repo should be accepted: %v", err)
	}
}

func TestUpdateTriggerValidation_RejectsUnsupportedHarness(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	// Seed a valid cron trigger.
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:        "updatable",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "x",
		AgentImage:  "runner:latest",
		TimeoutSec:  60,
	}); err != nil {
		t.Fatalf("seed trigger: %v", err)
	}

	if _, err := svc.UpdateTrigger(ctx, "updatable", store.TriggerInput{
		Name:        "updatable",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "x",
		AgentImage:  "runner:latest",
		Harness:     "bogus",
		TimeoutSec:  60,
	}, true); err == nil || !strings.Contains(err.Error(), "harness") {
		t.Fatalf("expected harness error on update, got %v", err)
	}
}

func TestUpdateTriggerValidation_RejectsBadRepoSyntax(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	cfg, _ := json.Marshal(map[string]any{"repo": "flatout-works/chetter"})
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          "pr-trigger",
		TriggerType:   store.TriggerTypePRReview,
		TriggerConfig: string(cfg),
		AgentImage:    "runner:latest",
		TimeoutSec:    60,
	}); err != nil {
		t.Fatalf("seed trigger: %v", err)
	}

	badCfg, _ := json.Marshal(map[string]any{"repo": "not a repo"})
	if _, err := svc.UpdateTrigger(ctx, "pr-trigger", store.TriggerInput{
		Name:          "pr-trigger",
		TriggerType:   store.TriggerTypePRReview,
		TriggerConfig: string(badCfg),
		AgentImage:    "runner:latest",
		TimeoutSec:    60,
	}, true); err == nil || !strings.Contains(err.Error(), "repo") {
		t.Fatalf("expected repo syntax error on update, got %v", err)
	}
}
