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
