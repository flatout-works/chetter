package service

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/flatout-works/chetter/internal/repository"
)

func TestRecoveryTaskRequestPreservesExecutionConfiguration(t *testing.T) {
	original := repository.ChetterTask{
		TeamID:      sql.NullString{String: "team_1", Valid: true},
		GitUrl:      sql.NullString{String: "https://github.com/acme/repo", Valid: true},
		GitRef:      sql.NullString{String: "feature", Valid: true},
		AgentImage:  sql.NullString{String: "agent:latest", Valid: true},
		Agent:       sql.NullString{String: "reviewer", Valid: true},
		ProviderID:  sql.NullString{String: "synthetic", Valid: true},
		ModelID:     sql.NullString{String: "model", Valid: true},
		VariantID:   sql.NullString{String: "high", Valid: true},
		Skills:      json.RawMessage(`["go","review"]`),
		McpProfiles: json.RawMessage(`["context"]`),
		Env:         json.RawMessage(`{"__chetter_harness":"pi","SAFE":"value"}`),
		TimeoutSec:  900,
	}

	req := recoveryTaskRequest(original, "task_original", "recovery prompt")
	if req.TeamID != "team_1" || req.Prompt != "recovery prompt" || req.GitURL != "https://github.com/acme/repo" || req.GitRef != "feature" {
		t.Fatalf("recovery lost task identity or repository configuration: %#v", req)
	}
	if req.AgentImage != "agent:latest" || req.Agent != "reviewer" || req.ProviderID != "synthetic" || req.ModelID != "model" || req.VariantID != "high" || req.Harness != "pi" {
		t.Fatalf("recovery lost agent execution configuration: %#v", req)
	}
	if !reflect.DeepEqual(req.Skills, []string{"go", "review"}) || !reflect.DeepEqual(req.MCPProfiles, []string{"context"}) {
		t.Fatalf("recovery lost task dependencies: skills=%#v mcp_profiles=%#v", req.Skills, req.MCPProfiles)
	}
	if req.Env["SAFE"] != "value" || req.Env["__recover_from"] != "task_original" {
		t.Fatalf("unexpected recovery environment: %#v", req.Env)
	}
	if _, exists := req.Env["__chetter_harness"]; exists {
		t.Fatalf("internal harness key should be normalized into Harness: %#v", req.Env)
	}
	if req.TimeoutSec != 900 || req.TriggerName != "" || req.TriggerType != "" || req.SessionMode != "" {
		t.Fatalf("recovery inherited lifecycle fields or lost timeout: %#v", req)
	}
}
