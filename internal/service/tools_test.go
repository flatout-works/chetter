package service

import (
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterTools(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	RegisterTools(server, nil)
}

func TestTaskToolRecordKeepsStableShape(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	record := taskToolRecord(store.TaskRecord{
		ID:                "task_1",
		TeamID:            "team_123",
		Status:            "done",
		Prompt:            "prompt",
		GitURL:            "https://example.com/repo.git",
		GitRef:            "main",
		AgentImage:        "image",
		Agent:             "changelog-maintainer",
		ProviderID:        "synthetic",
		ModelID:           "model",
		VariantID:         "variant",
		OpenCodeSessionID: "session",
		RunnerImageDigest: "digest",
		CommitAuthorName:  "Chetter",
		CommitAuthorEmail: "chetter@chetter.flatout.works",
		Skills:            []string{"go"},
		Env:               map[string]string{"SAFE": "value"},
		TimeoutSec:        300,
		Summary:           "summary",
		CreatedAt:         now,
		UpdatedAt:         now,
	})

	if record.ID != "task_1" || record.Status != "done" || record.TimeoutSec != 300 || record.TeamID != "team_123" {
		t.Fatalf("unexpected task record: %+v", record)
	}
	if record.AgentImage != "image" || record.Agent != "changelog-maintainer" || len(record.Skills) != 1 || record.Env["SAFE"] != "value" {
		t.Fatalf("expected core task fields to be preserved: %+v", record)
	}
	if record.ProviderID != "synthetic" || record.ModelID != "model" || record.VariantID != "variant" {
		t.Fatalf("expected model fields to be preserved: %+v", record)
	}
}

func TestTriggerToolRecordKeepsStableShape(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	next := now.Add(time.Hour)
	record := triggerToolRecord(store.ScheduleRecord{
		ID:            "trig_1",
		TeamID:        "team_123",
		Name:          "hourly",
		TriggerType:   store.TriggerTypeCron,
		TriggerConfig: "{}",
		CronExpr:      "@hourly",
		Prompt:        "prompt",
		GitURL:        "https://example.com/repo.git",
		GitRef:        "main",
		AgentImage:    "image",
		Agent:         "docs-maintainer",
		ProviderID:    "synthetic",
		ModelID:       "model",
		VariantID:     "variant",
		Harness:       "opencode",
		Skills:        []string{"docs"},
		TimeoutSec:    300,
		Enabled:       true,
		CreatedAt:     now,
		UpdatedAt:     now,
		NextRunAt:     &next,
	})

	if record.ID != "trig_1" || record.Name != "hourly" || record.TriggerType != store.TriggerTypeCron || !record.Enabled {
		t.Fatalf("unexpected trigger record: %+v", record)
	}
	if record.ProviderID != "synthetic" || record.ModelID != "model" || record.VariantID != "variant" || record.Harness != "opencode" {
		t.Fatalf("expected model and harness fields to be preserved: %+v", record)
	}
	if len(record.Skills) != 1 || record.NextRunAt == nil || !record.NextRunAt.Equal(next) {
		t.Fatalf("expected skills and schedule timestamps to be preserved: %+v", record)
	}
}

func TestExpandChetterPromptVars(t *testing.T) {
	t.Parallel()
	prompt := "Task $CHETTER_TASK_ID by ${CHETTER_AGENT_NAME} on $CHETTER_RUNNER_IMAGE ($CHETTER_RUNNER_IMAGE_DIGEST); keep $ISSUE_NUMBER"
	got := expandChetterPromptVars(prompt, map[string]string{
		"CHETTER_AGENT_NAME":   "issue-writer",
		"CHETTER_TASK_ID":      "task_123",
		"CHETTER_RUNNER_IMAGE": "runner:latest",
	})
	want := "Task task_123 by issue-writer on runner:latest (unknown); keep $ISSUE_NUMBER"
	if got != want {
		t.Fatalf("expandChetterPromptVars() = %q, want %q", got, want)
	}
}
