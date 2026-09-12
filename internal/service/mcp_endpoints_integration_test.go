package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
)

func TestSubmitTaskMcpEndpointScopeAndPolicy(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	teamA, _ := seedTeam(t, tdb.DB, "engineering", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "platform", "bob")

	seedMcpEndpoint(t, tdb.DB, tdb.Dialect(), "shared", "global", "")
	seedMcpEndpoint(t, tdb.DB, tdb.Dialect(), "shared", "team", teamA)
	seedMcpEndpoint(t, tdb.DB, tdb.Dialect(), "team-only", "team", teamA)

	globalTask, err := svc.SubmitTask(ctxWithAdmin(ctx), SubmitTaskRequest{
		Prompt:       "use shared endpoint",
		AgentImage:   "runner:latest",
		McpEndpoints: []string{"shared"},
	})
	if err != nil {
		t.Fatalf("global SubmitTask: %v", err)
	}
	if len(globalTask.McpEndpoints) != 1 || globalTask.McpEndpoints[0] != "shared" {
		t.Fatalf("global task endpoints = %#v", globalTask.McpEndpoints)
	}

	teamTask, err := svc.SubmitTask(ctxWithTeam(ctx, teamA), SubmitTaskRequest{
		Prompt:       "use team endpoints",
		AgentImage:   "runner:latest",
		McpEndpoints: []string{"shared", "team-only"},
	})
	if err != nil {
		t.Fatalf("team SubmitTask: %v", err)
	}
	if len(teamTask.McpEndpoints) != 2 {
		t.Fatalf("team task endpoints = %#v", teamTask.McpEndpoints)
	}

	resolved, err := loadMcpEndpoints(ctx, tdb.DB, tdb.Dialect(), []string{"shared"}, teamA)
	if err != nil {
		t.Fatalf("resolve team endpoint override: %v", err)
	}
	if len(resolved) != 1 || !strings.HasSuffix(resolved[0].URL, "/team/shared") {
		t.Fatalf("team endpoint should override global definition: %#v", resolved)
	}

	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamB), SubmitTaskRequest{
		Prompt:       "use another team's endpoint",
		AgentImage:   "runner:latest",
		McpEndpoints: []string{"team-only"},
	}); err == nil || !strings.Contains(err.Error(), "active MCP endpoints not found: team-only") {
		t.Fatalf("expected cross-team endpoint rejection, got %v", err)
	}

	if _, err := svc.SubmitTask(ctxWithAdmin(ctx), SubmitTaskRequest{
		Prompt:       "global task using team endpoint",
		AgentImage:   "runner:latest",
		McpEndpoints: []string{"team-only"},
	}); err == nil || !strings.Contains(err.Error(), "active MCP endpoints not found: team-only") {
		t.Fatalf("expected global task endpoint rejection, got %v", err)
	}

	resumableTask, err := svc.SubmitTask(ctxWithTeam(ctx, teamA), SubmitTaskRequest{
		Prompt:       "resumable task using endpoint",
		AgentImage:   "runner:latest",
		SessionMode:  "resumable",
		McpEndpoints: []string{"team-only"},
	})
	if err != nil {
		t.Fatalf("resumable task with endpoint: %v", err)
	}
	if len(resumableTask.McpEndpoints) != 1 || resumableTask.McpEndpoints[0] != "team-only" {
		t.Fatalf("resumable task endpoints = %#v, want [team-only]", resumableTask.McpEndpoints)
	}
}

// driveResumableTaskToPaused submits a resumable task carrying the given MCP
// endpoints, claims it through the runner RPC, reports a terminal success, and
// returns the first claim's task payload plus the resulting paused session.
func driveResumableTaskToPaused(t *testing.T, svc *Service, rpc *RunnerRPCService, q data.Repository, ctx context.Context, endpoints []string) (*runnerv1.Task, repository.AgentSession) {
	t.Helper()
	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:       "resumable task with endpoints",
		AgentImage:   "runner:latest",
		SessionMode:  "resumable",
		McpEndpoints: endpoints,
	})
	if err != nil {
		t.Fatalf("submit resumable task: %v", err)
	}
	registerIsolationCapableRunner(t, q, "runner_1")
	claimResp, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_1", WaitSeconds: 1,
	}))
	if err != nil {
		t.Fatalf("claim resumable task: %v", err)
	}
	if claimResp.Msg.Task == nil || claimResp.Msg.Task.TaskId != rec.ID {
		t.Fatalf("claim returned wrong task: %+v", claimResp.Msg.Task)
	}
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:            rec.ID,
			ExecutionId:       claimResp.Msg.Task.ExecutionId,
			ClaimId:           claimResp.Msg.Task.ClaimId,
			AgentSessionId:    claimResp.Msg.Task.AgentSessionId,
			UserPromptId:      claimResp.Msg.Task.UserPromptId,
			Status:            "done",
			Summary:           "paused for review",
			EndedAt:           time.Now().UTC().Format(time.RFC3339Nano),
			OpencodeSessionId: "oc_sid_endpoints",
			WorkspacePath:     "/var/lib/runner/" + rec.ID + "/workspace",
		}},
	})); err != nil {
		t.Fatalf("report terminal event: %v", err)
	}
	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get user prompt: %v", err)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get paused session: %v", err)
	}
	if session.Status != "paused" {
		t.Fatalf("session status = %s, want paused", session.Status)
	}
	return claimResp.Msg.Task, session
}

func TestResumableTaskWithMcpEndpoints(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(data.New(tdb.DB, tdb.Dialect()), tdb.DB, tdb.Dialect())

	seedMcpEndpoint(t, tdb.DB, tdb.Dialect(), "context", "global", "")

	firstTask, session := driveResumableTaskToPaused(t, svc, rpc, q, ctx, []string{"context"})
	if len(firstTask.McpEndpoints) != 1 || !strings.HasSuffix(firstTask.McpEndpoints[0].Url, "/global/context") {
		t.Fatalf("first claim endpoints = %#v, want resolved global/context", firstTask.McpEndpoints)
	}

	resumeOut, err := svc.ResumeAgentSession(ctx, session.ID, "address feedback", 600)
	if err != nil {
		t.Fatalf("resume session: %v", err)
	}
	resumeClaim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_1", WaitSeconds: 0,
	}))
	if err != nil {
		t.Fatalf("claim resumed task: %v", err)
	}
	if resumeClaim.Msg.Task == nil || resumeClaim.Msg.Task.TaskId != resumeOut.Task.ID {
		t.Fatalf("claim returned wrong resume task: %+v", resumeClaim.Msg.Task)
	}
	if len(resumeClaim.Msg.Task.McpEndpoints) != 1 || !strings.HasSuffix(resumeClaim.Msg.Task.McpEndpoints[0].Url, "/global/context") {
		t.Fatalf("resumed claim endpoints = %#v, want resolved global/context", resumeClaim.Msg.Task.McpEndpoints)
	}
}

func TestResumeAgentSessionFailsWhenMcpEndpointRemoved(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(data.New(tdb.DB, tdb.Dialect()), tdb.DB, tdb.Dialect())

	seedMcpEndpoint(t, tdb.DB, tdb.Dialect(), "context", "global", "")

	_, session := driveResumableTaskToPaused(t, svc, rpc, q, ctx, []string{"context"})

	if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		`UPDATE definitions SET active = false WHERE definition_type = 'mcp_endpoint' AND name = 'context'`,
		`UPDATE definitions SET active = false WHERE definition_type = 'mcp_endpoint' AND name = 'context'`)); err != nil {
		t.Fatalf("deactivate endpoint: %v", err)
	}

	_, err := svc.ResumeAgentSession(ctx, session.ID, "address feedback", 600)
	if err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected resume to fail naming the removed endpoint, got %v", err)
	}
}

func seedMcpEndpoint(t *testing.T, db *sql.DB, dialect store.Dialect, name, scope, teamID string) {
	t.Helper()
	now := time.Now().UTC()
	content := fmt.Sprintf("name: %s\nurl: https://mcp.example.com/%s/%s\n", name, scope, name)
	path := scope + "/mcp-endpoints/" + name + ".yaml"
	query := testQuery(dialect,
		`INSERT INTO definitions (id, source_id, definition_type, name, scope, team_id, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES (?, ?, 'mcp_endpoint', ?, ?, ?, ?, ?, ?, ?, true, ?, ?)`,
		`INSERT INTO definitions (id, source_id, definition_type, name, scope, team_id, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES ($1, $2, 'mcp_endpoint', $3, $4, $5, $6, $7, $8, $9, true, $10, $11)`,
	)
	var owner any
	if teamID != "" {
		owner = teamID
	}
	if _, err := db.Exec(query, "def_mcp_"+scope+"_"+name, defaultDefinitionSourceID, name, scope, owner, path, "test", strings.Repeat("a", 64), content, now, now); err != nil {
		t.Fatalf("seed MCP endpoint %s/%s: %v", scope, name, err)
	}
}
