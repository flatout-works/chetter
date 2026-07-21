package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

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

	if _, err := svc.SubmitTask(ctxWithTeam(ctx, teamA), SubmitTaskRequest{
		Prompt:       "resumable task using endpoint",
		AgentImage:   "runner:latest",
		SessionMode:  "resumable",
		McpEndpoints: []string{"team-only"},
	}); err == nil || !strings.Contains(err.Error(), "mcp_endpoints cannot be attached to resumable tasks") {
		t.Fatalf("expected resumable endpoint rejection, got %v", err)
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
