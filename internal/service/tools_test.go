package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterTools(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	RegisterTools(server, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	result, err := clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	forbidden := map[string]bool{
		"chetter_create_issue":  true,
		"chetter_issue_comment": true,
		"chetter_create_pr":     true,
		"chetter_pr_review":     true,
	}
	for _, tool := range result.Tools {
		if forbidden[tool.Name] {
			t.Errorf("control-plane MCP unexpectedly exposes %s", tool.Name)
		}
	}
}

func TestDrainRunnerToolRequiresAdmin(t *testing.T) {
	svc := &Service{}
	if _, _, err := svc.drainRunnerTool(context.Background(), nil, DrainRunnerInput{RunnerID: "runner_1"}); err == nil || err.Error() != "admin access required" {
		t.Fatalf("team-scoped drain error = %v, want admin access required", err)
	}

	adminCtx := auth.WithScope(context.Background(), auth.Scope{Admin: true})
	if _, _, err := svc.drainRunnerTool(adminCtx, nil, DrainRunnerInput{RunnerID: "runner_1"}); err == nil || err.Error() != "runner RPC service not available" {
		t.Fatalf("admin drain error = %v, want runner RPC service not available", err)
	}
}

func TestTaskToolRecordKeepsStableShape(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	record := taskToolRecord(store.TaskRecord{
		ID:               "task_1",
		TeamID:           "team_123",
		Status:           "done",
		Prompt:           "prompt",
		GitURL:           "https://example.com/repo.git",
		GitRef:           "main",
		AgentImage:       "image",
		Agent:            "changelog-maintainer",
		ProviderID:       "synthetic",
		ModelID:          "model",
		VariantID:        "variant",
		TriggerName:      "nightly-docs",
		TriggerType:      store.TriggerTypeCron,
		SubmissionSource: "trigger",
		Skills:           []string{"go"},
		Env:              map[string]string{"SAFE": "value"},
		TimeoutSec:       300,
		Summary:          "summary",
		CreatedAt:        now,
		UpdatedAt:        now,
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
	if record.TriggerName != "nightly-docs" || record.TriggerType != store.TriggerTypeCron {
		t.Fatalf("expected trigger attribution to be preserved: %+v", record)
	}
	if record.SubmissionSource != "trigger" {
		t.Fatalf("expected submission source to be preserved: %+v", record)
	}
	validateGeneratedOutputSchema(t, TaskStatusOutput{Task: record})
}

func TestTaskEventOutputMatchesGeneratedSchema(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	validateGeneratedOutputSchema(t, TaskEventsOutput{Events: []TaskEventRecord{{
		ID:        "evt_1",
		TaskID:    "task_1",
		Subject:   "task.task_1",
		Status:    "running",
		EventType: "task.progress",
		Payload:   `{"task_id":"task_1","status":"running","summary":"working"}`,
		CreatedAt: now,
	}}})
	validateGeneratedOutputSchema(t, TaskLatestEventOutput{
		Event: TaskEventRecord{
			ID:        "evt_1",
			TaskID:    "task_1",
			Subject:   "task.task_1",
			Status:    "running",
			EventType: "task.progress",
			Payload:   `{"task_id":"task_1","status":"running","summary":"working"}`,
			CreatedAt: now,
		},
		AgeSec:  1,
		IsStale: false,
	})
}

func validateGeneratedOutputSchema[T any](t *testing.T, value T) {
	t.Helper()
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatalf("generate schema: %v", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if err := resolved.Validate(&decoded); err != nil {
		t.Fatalf("validate output schema: %v\njson: %s", err, data)
	}
}

func TestTriggerToolRecordKeepsStableShape(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	next := now.Add(time.Hour)
	record := triggerToolRecord(store.TriggerRecord{
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
		t.Fatalf("expected skills and trigger timestamps to be preserved: %+v", record)
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

func TestMergeTriggerConfigIncludesRuntimeFields(t *testing.T) {
	got := MergeTriggerConfig(json.RawMessage(`{"repo":"flatout-works/chetter","event":"opened"}`), "", "labeled", []string{"bug"}, "resumable", "waiting_for_pr_feedback", 48)
	want := `{"event":"labeled","match_labels":["bug"],"pause_reason":"waiting_for_pr_feedback","repo":"flatout-works/chetter","session_mode":"resumable","ttl_hours":48}`
	if got != want {
		t.Fatalf("MergeTriggerConfig() = %q, want %q", got, want)
	}
}
