package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/definitions"
)

const testMCPProfileYAML = `name: context
transport: http
url: https://mcp.example.com/mcp
headers:
  X-Tenant: engineering
auth:
  type: bearer
  token_env: EXAMPLE_MCP_TOKEN
`

func seedGlobalMCPProfile(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	if err := repository.New(db).UpsertDefinition(context.Background(), repository.UpsertDefinitionParams{
		ID:             "def_mcp_context",
		SourceID:       defaultDefinitionSourceID,
		DefinitionType: definitions.DefinitionTypeMCPProfile,
		Name:           "context",
		Scope:          definitions.DefinitionScopeGlobal,
		Path:           "mcp-profiles/context.yaml",
		SourceCommit:   "test-commit",
		ContentHash:    strings.Repeat("a", 64),
		Content:        testMCPProfileYAML,
		Active:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed MCP profile: %v", err)
	}
}

func TestTeamTokenCannotSubmitTaskWithMCPProfiles(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	_, err := svc.SubmitTask(ctxWithTeam(context.Background(), "team_1"), SubmitTaskRequest{
		Prompt:      "use context",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"context"},
	})
	if err == nil || !strings.Contains(err.Error(), "mcp_profiles require admin access") {
		t.Fatalf("expected admin access error, got %v", err)
	}
}

func TestAdminCannotAssignMCPProfilesToTeamTask(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	_, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		TeamID:      "team_1",
		Prompt:      "use context",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"context"},
	})
	if err == nil || !strings.Contains(err.Error(), "global admin-owned task") {
		t.Fatalf("expected global task error, got %v", err)
	}
}

func TestAdminTaskStoresNamesAndClaimResolvesProfileWithoutToken(t *testing.T) {
	t.Setenv("EXAMPLE_MCP_TOKEN", "server-secret-must-not-be-resolved")
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedGlobalMCPProfile(t, tdb.DB)

	record, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		Prompt:      "use context",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{" context ", "context"},
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if len(record.MCPProfiles) != 1 || record.MCPProfiles[0] != "context" {
		t.Fatalf("unexpected task MCP profiles: %#v", record.MCPProfiles)
	}

	q := repository.New(tdb.DB)
	row, err := q.GetTaskByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if row.TeamID.Valid {
		t.Fatalf("profile-bearing task must be global, got team %q", row.TeamID.String)
	}
	if strings.Contains(string(row.McpProfiles), "server-secret") || string(row.McpProfiles) != `["context"]` {
		t.Fatalf("task must store only profile names, got %s", row.McpProfiles)
	}

	rpc := NewRunnerRPCService(q, tdb.DB)
	claimed, err := rpc.ClaimTask(context.Background(), connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_1",
	}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	profiles := claimed.Msg.Task.GetMcpProfiles()
	if len(profiles) != 1 {
		t.Fatalf("expected one resolved profile, got %#v", profiles)
	}
	profile := profiles[0]
	if profile.GetName() != "context" || profile.GetUrl() != "https://mcp.example.com/mcp" || profile.GetBearerTokenEnv() != "EXAMPLE_MCP_TOKEN" {
		t.Fatalf("unexpected resolved profile: %#v", profile)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if strings.Contains(string(encoded), "server-secret") {
		t.Fatalf("server must not resolve runner token values: %s", encoded)
	}
}

func TestSubmitTaskRejectsMissingMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	_, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		Prompt:      "use missing profile",
		AgentImage:  "runner:latest",
		MCPProfiles: []string{"missing"},
	})
	if err == nil || !strings.Contains(err.Error(), "active global MCP profiles not found: missing") {
		t.Fatalf("expected missing profile error, got %v", err)
	}
}

func TestSubmitTaskRejectsTooManyMCPProfiles(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	profiles := make([]string, maxTaskMCPProfiles+1)
	for i := range profiles {
		profiles[i] = fmt.Sprintf("profile-%d", i)
	}
	_, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		Prompt:      "use too many profiles",
		AgentImage:  "runner:latest",
		MCPProfiles: profiles,
	})
	if err == nil || !strings.Contains(err.Error(), "at most 16 MCP profiles") {
		t.Fatalf("expected profile count error, got %v", err)
	}
}

func TestClaimTaskFailsClosedForTeamOwnedLegacyMCPProfiles(t *testing.T) {
	_, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	seedGlobalMCPProfile(t, tdb.DB)
	insertPendingTask(t, q, "task_team_profile", "use context", "runner:latest")
	if _, err := tdb.DB.ExecContext(context.Background(),
		`UPDATE chetter_tasks SET team_id = ?, mcp_profiles = ? WHERE id = ?`,
		"team_1", `["context"]`, "task_team_profile",
	); err != nil {
		t.Fatalf("prepare legacy task: %v", err)
	}

	rpc := NewRunnerRPCService(q, tdb.DB)
	claimed, err := rpc.ClaimTask(context.Background(), connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_1",
	}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	profiles := claimed.Msg.Task.GetMcpProfiles()
	if len(profiles) != 1 || profiles[0].GetName() != "context" {
		t.Fatalf("expected invalid profile stub, got %#v", profiles)
	}
	if profiles[0].GetUrl() != "" || profiles[0].GetBearerTokenEnv() != "" || len(profiles[0].GetHeaders()) != 0 {
		t.Fatalf("team-owned task resolved privileged profile: %#v", profiles[0])
	}
}

func TestNonAdminCannotReadMCPProfileDefinitions(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedGlobalMCPProfile(t, tdb.DB)
	teamCtx := ctxWithTeam(context.Background(), "team_1")

	_, listed, err := svc.listDefinitionsTool(teamCtx, nil, ListDefinitionsInput{})
	if err != nil {
		t.Fatalf("list definitions: %v", err)
	}
	for _, def := range listed.Definitions {
		if def.DefinitionType == definitions.DefinitionTypeMCPProfile {
			t.Fatalf("non-admin list exposed MCP profile: %#v", def)
		}
	}
	if _, _, err := svc.getDefinitionTool(teamCtx, nil, GetDefinitionInput{
		DefinitionType: definitions.DefinitionTypeMCPProfile,
		Name:           "context",
	}); err == nil || !strings.Contains(err.Error(), "admin access required") {
		t.Fatalf("expected non-admin get rejection, got %v", err)
	}

	_, got, err := svc.getDefinitionTool(ctxWithAdmin(context.Background()), nil, GetDefinitionInput{
		DefinitionType: definitions.DefinitionTypeMCPProfile,
		Name:           "context",
	})
	if err != nil {
		t.Fatalf("admin get definition: %v", err)
	}
	if got.Definition.Content != testMCPProfileYAML {
		t.Fatalf("admin did not receive MCP profile content: %#v", got.Definition)
	}
}
