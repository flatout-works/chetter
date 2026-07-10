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

func TestSubmitTaskMCPProfilePolicy(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	tooMany := make([]string, maxTaskMCPProfiles+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("profile-%d", i)
	}
	tests := []struct {
		name string
		ctx  context.Context
		req  SubmitTaskRequest
		want string
	}{
		{
			name: "admin access",
			ctx:  ctxWithTeam(context.Background(), "team_1"),
			req:  SubmitTaskRequest{MCPProfiles: []string{"context"}},
			want: "mcp_profiles require admin access",
		},
		{
			name: "global task",
			ctx:  ctxWithAdmin(context.Background()),
			req:  SubmitTaskRequest{TeamID: "team_1", MCPProfiles: []string{"context"}},
			want: "global admin-owned task",
		},
		{
			name: "non-resumable task",
			ctx:  ctxWithAdmin(context.Background()),
			req:  SubmitTaskRequest{SessionMode: "resumable", MCPProfiles: []string{"context"}},
			want: "cannot be attached to resumable tasks",
		},
		{
			name: "existing profile",
			ctx:  ctxWithAdmin(context.Background()),
			req:  SubmitTaskRequest{MCPProfiles: []string{"missing"}},
			want: "active global MCP profiles not found: missing",
		},
		{
			name: "profile limit",
			ctx:  ctxWithAdmin(context.Background()),
			req:  SubmitTaskRequest{MCPProfiles: tooMany},
			want: "at most 16 MCP profiles",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req.Prompt = "use context"
			tt.req.AgentImage = "runner:latest"
			_, err := svc.SubmitTask(tt.ctx, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestAdminTaskStoresProfileNamesAndClaimResolvesWithoutToken(t *testing.T) {
	t.Setenv("EXAMPLE_MCP_TOKEN", "server-secret-must-not-be-resolved")
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedGlobalMCPProfile(t, tdb.DB)

	record, err := svc.SubmitTask(ctxWithAdmin(context.Background()), SubmitTaskRequest{
		Prompt:      "use context",
		GitURL:      "https://github.com/flatout-works/chetter",
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
	if row.TeamID.Valid || string(row.McpProfiles) != `["context"]` {
		t.Fatalf("task must store only profile names, got team=%v profiles=%s", row.TeamID, row.McpProfiles)
	}
	listed, err := svc.ListTasks(ctxWithAdmin(context.Background()), "", 20, 0, "", nil, []string{"flatout-works/chetter"})
	if err != nil {
		t.Fatalf("ListTasks with repo filter: %v", err)
	}
	if len(listed) != 1 || len(listed[0].MCPProfiles) != 1 || listed[0].MCPProfiles[0] != "context" {
		t.Fatalf("repo-filtered task lost MCP profiles: %#v", listed)
	}

	rpc := NewRunnerRPCService(q, tdb.DB)
	claimed, err := rpc.ClaimTask(context.Background(), connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1"}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	profiles := claimed.Msg.Task.GetMcpProfiles()
	if len(profiles) != 1 {
		t.Fatalf("expected one resolved profile, got %#v", profiles)
	}
	profile := profiles[0]
	if profile.GetName() != "context" || profile.GetUrl() != "https://mcp.example.com/mcp" || profile.GetBearerTokenEnv() != "EXAMPLE_MCP_TOKEN" {
		t.Fatalf("unexpected claimed profile: %#v", profile)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal claimed profile: %v", err)
	}
	if strings.Contains(string(encoded), "server-secret") {
		t.Fatalf("runner RPC contains a token value: %s", encoded)
	}
}

func TestRecoverTaskPreservesMCPProfiles(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	seedGlobalMCPProfile(t, tdb.DB)
	ctx := ctxWithAdmin(context.Background())

	original, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      "use context",
		AgentImage:  "runner:latest",
		Harness:     "pi",
		MCPProfiles: []string{"context"},
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx,
		`UPDATE chetter_tasks SET status = 'error', session_export = ? WHERE id = ?`,
		"previous session", original.ID,
	); err != nil {
		t.Fatalf("prepare recoverable task: %v", err)
	}

	recovered, err := svc.RecoverTask(ctx, original.ID)
	if err != nil {
		t.Fatalf("RecoverTask: %v", err)
	}
	if len(recovered.MCPProfiles) != 1 || recovered.MCPProfiles[0] != "context" {
		t.Fatalf("recovery dropped MCP profiles: %#v", recovered.MCPProfiles)
	}
	row, err := repository.New(tdb.DB).GetTaskByID(ctx, recovered.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if string(row.McpProfiles) != `["context"]` {
		t.Fatalf("recovery stored wrong MCP profiles: %s", row.McpProfiles)
	}
	claimed, err := NewRunnerRPCService(repository.New(tdb.DB), tdb.DB).ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1"}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	profiles := claimed.Msg.Task.GetMcpProfiles()
	if len(profiles) != 1 || profiles[0].GetName() != "context" {
		t.Fatalf("recovered task claim lost MCP profiles: %#v", profiles)
	}
}
