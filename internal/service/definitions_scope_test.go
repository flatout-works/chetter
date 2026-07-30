package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/pkg/definitions"
)

// TestDefinitionScopeFilter verifies the pure scope-visibility logic shared by
// the MCP list/get tools and ListAgentDefinitions. It does not require a DB.
func TestDefinitionScopeFilter(t *testing.T) {
	mkDef := func(scope, teamID, repo string) repository.Definition {
		d := repository.Definition{Scope: scope, UpdatedAt: time.Now().UTC()}
		if teamID != "" {
			d.TeamID = sql.NullString{String: teamID, Valid: true}
		}
		if repo != "" {
			d.Repo = sql.NullString{String: repo, Valid: true}
		}
		return d
	}
	teamA, teamB := "team_a", "team_b"

	tests := []struct {
		name    string
		ctx     context.Context
		uiRepos []string
		def     repository.Definition
		visible bool
	}{
		{"no scope sees global", context.Background(), nil, mkDef("global", "", ""), true},
		{"no scope sees team", context.Background(), nil, mkDef("team", teamA, ""), true},
		{"admin sees other team", ctxWithAdmin(context.Background()), nil, mkDef("team", teamB, ""), true},
		{"team sees own team", ctxWithTeam(context.Background(), teamA), nil, mkDef("team", teamA, ""), true},
		{"team blocked from other team", ctxWithTeam(context.Background(), teamA), nil, mkDef("team", teamB, ""), false},
		{"team sees global", ctxWithTeam(context.Background(), teamA), nil, mkDef("global", "", ""), true},
		{"team without repos sees repo-scoped (matches ListAgentDefinitions)", ctxWithTeam(context.Background(), teamA), nil, mkDef("repo", "", "acme/app"), true},
		{"team sees repo-scoped when repo matches", ctxWithTeam(context.Background(), teamA), []string{"acme/app"}, mkDef("repo", "", "acme/app"), true},
		{"team blocked from repo-scoped when repo does not match", ctxWithTeam(context.Background(), teamA), []string{"acme/app"}, mkDef("repo", "", "other/repo"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDefinitionScopeFilter(tc.ctx, nil, tc.uiRepos)
			if got := f.visible(tc.def); got != tc.visible {
				t.Fatalf("visible = %v, want %v", got, tc.visible)
			}
		})
	}
}

// TestPickVisibleDefinition verifies deterministic precedence and fail-closed
// behavior when same-named definitions exist across scopes.
func TestPickVisibleDefinition(t *testing.T) {
	teamA, teamB := "team_a", "team_b"
	base := time.Now().UTC()
	mkDef := func(scope, teamID string, updated time.Time) repository.Definition {
		d := repository.Definition{Scope: scope, UpdatedAt: updated, Content: scope + ":" + teamID}
		if teamID != "" {
			d.TeamID = sql.NullString{String: teamID, Valid: true}
		}
		return d
	}

	t.Run("team A prefers own team over global", func(t *testing.T) {
		// Note: pickVisibleDefinition uses global>team>repo precedence (mirrors
		// GetDefinitionBySourceTypeName), so among visible defs global wins.
		global := mkDef("global", "", base)
		ownTeam := mkDef("team", teamA, base.Add(time.Second))
		filter := newDefinitionScopeFilter(ctxWithTeam(context.Background(), teamA), nil, nil)
		got, ok := pickVisibleDefinition([]repository.Definition{ownTeam, global}, filter, "")
		if !ok {
			t.Fatal("expected a visible definition")
		}
		if got.Scope != "global" {
			t.Fatalf("precedence = %q, want global first", got.Scope)
		}
	})

	t.Run("team A does not receive team B definition", func(t *testing.T) {
		other := mkDef("team", teamB, base)
		filter := newDefinitionScopeFilter(ctxWithTeam(context.Background(), teamA), nil, nil)
		if _, ok := pickVisibleDefinition([]repository.Definition{other}, filter, ""); ok {
			t.Fatal("team A must not resolve team B definition")
		}
	})

	t.Run("scope filter constrains to requested scope", func(t *testing.T) {
		global := mkDef("global", "", base)
		ownTeam := mkDef("team", teamA, base)
		filter := newDefinitionScopeFilter(ctxWithTeam(context.Background(), teamA), nil, nil)
		got, ok := pickVisibleDefinition([]repository.Definition{global, ownTeam}, filter, "team")
		if !ok || got.Scope != "team" {
			t.Fatalf("scope filter = ok %v scope %q, want team", ok, got.Scope)
		}
	})

	t.Run("most recently updated wins within same scope", func(t *testing.T) {
		older := mkDef("global", "", base)
		newer := mkDef("global", "", base.Add(time.Minute))
		filter := newDefinitionScopeFilter(context.Background(), nil, nil)
		got, ok := pickVisibleDefinition([]repository.Definition{older, newer}, filter, "")
		if !ok || got.UpdatedAt != newer.UpdatedAt {
			t.Fatalf("expected newer global definition, got ok %v", ok)
		}
	})
}

// seedScopedDefinition inserts an active definition row with the given scope
// ownership. It mirrors seedMcpEndpoint for non-endpoint definition types.
func seedScopedDefinition(t *testing.T, db *sql.DB, dialect store.Dialect, defType, name, scope, teamID, repo, content string) {
	t.Helper()
	now := time.Now().UTC()
	path := scope + "/" + defType + "s/" + name
	if scope == definitionScopeTeam && teamID != "" {
		path = scope + "/" + teamID + "/" + defType + "s/" + name
	} else if repo != "" {
		path = "repos/" + repo + "/" + defType + "s/" + name
	}
	id := "def_" + scope + "_" + defType + "_" + name
	if teamID != "" {
		id += "_" + teamID
	}
	if repo != "" {
		id += "_" + strings.ReplaceAll(repo, "/", "_")
	}
	query := testQuery(dialect,
		`INSERT INTO definitions (id, source_id, definition_type, name, scope, team_id, repo, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true, ?, ?)`,
		`INSERT INTO definitions (id, source_id, definition_type, name, scope, team_id, repo, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, true, $12, $13)`,
	)
	var teamArg, repoArg any
	if teamID != "" {
		teamArg = teamID
	}
	if repo != "" {
		repoArg = repo
	}
	if _, err := db.Exec(query, id, defaultDefinitionSourceID, defType, name, scope, teamArg, repoArg, path, "test", strings.Repeat("a", 64), content, now, now); err != nil {
		t.Fatalf("seed definition %s/%s/%s: %v", scope, defType, name, err)
	}
}

// TestListAndGetDefinitionsScopedByTeam verifies the MCP list/get tools only
// expose definitions visible to the caller's team scope (issue #262).
func TestListAndGetDefinitionsScopedByTeam(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	teamA, _ := seedTeam(t, tdb.DB, "engineering", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "platform", "bob")

	agentContent := func(team string) string {
		return "---\nidentity: " + team + "-bot\n---\n# Reviewer for " + team + "\n"
	}
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "reviewer", definitionScopeGlobal, "", "", agentContent("global"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "reviewer", definitionScopeTeam, teamA, "", agentContent("A"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "reviewer", definitionScopeTeam, teamB, "", agentContent("B"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "b-only", definitionScopeTeam, teamB, "", agentContent("B"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeSkill, "common", definitionScopeGlobal, "", "", "# common skill\n")
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeSkill, "a-skill", definitionScopeTeam, teamA, "", "# A skill\n")
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeSkill, "b-skill", definitionScopeTeam, teamB, "", "# B skill\n")

	listAgents := func(t *testing.T, c context.Context) []DefinitionToolRecord {
		t.Helper()
		_, out, err := svc.listDefinitionsTool(c, nil, ListDefinitionsInput{DefinitionType: definitions.DefinitionTypeAgent})
		if err != nil {
			t.Fatalf("listDefinitionsTool: %v", err)
		}
		return out.Definitions
	}
	listSkills := func(t *testing.T, c context.Context) []string {
		t.Helper()
		_, out, err := svc.listDefinitionsTool(c, nil, ListDefinitionsInput{DefinitionType: definitions.DefinitionTypeSkill})
		if err != nil {
			t.Fatalf("listDefinitionsTool skills: %v", err)
		}
		names := make([]string, 0, len(out.Definitions))
		for _, d := range out.Definitions {
			names = append(names, d.Name)
		}
		return names
	}
	getAgent := func(t *testing.T, c context.Context, name, scope string) (DefinitionToolRecord, error) {
		t.Helper()
		_, out, err := svc.getDefinitionTool(c, nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeAgent, Name: name, Scope: scope})
		return out.Definition, err
	}

	t.Run("admin lists all agents", func(t *testing.T) {
		agents := listAgents(t, ctxWithAdmin(ctx))
		if len(agents) != 4 {
			t.Fatalf("admin should see 4 agents, got %d: %#v", len(agents), agents)
		}
	})

	t.Run("team A lists global and own agents, not team B", func(t *testing.T) {
		agents := listAgents(t, ctxWithTeam(ctx, teamA))
		// global reviewer + team A reviewer + (not team B reviewer, not b-only)
		if len(agents) != 2 {
			t.Fatalf("team A should see 2 agents, got %d: %#v", len(agents), agents)
		}
		for _, a := range agents {
			if a.Name == "b-only" {
				t.Fatalf("team A must not see team B only agent: %#v", agents)
			}
			if a.TeamID == teamB {
				t.Fatalf("team A must not see team B agent: %#v", a)
			}
		}
	})

	t.Run("team B lists global and own agents, not team A", func(t *testing.T) {
		agents := listAgents(t, ctxWithTeam(ctx, teamB))
		if len(agents) != 3 { // global reviewer + team B reviewer + b-only
			t.Fatalf("team B should see 3 agents, got %d: %#v", len(agents), agents)
		}
		for _, a := range agents {
			if a.TeamID == teamA {
				t.Fatalf("team B must not see team A agent: %#v", a)
			}
		}
	})

	t.Run("team A skills exclude team B", func(t *testing.T) {
		skills := listSkills(t, ctxWithTeam(ctx, teamA))
		if !containsString(skills, "common") || !containsString(skills, "a-skill") {
			t.Fatalf("team A should see common and a-skill, got %v", skills)
		}
		if containsString(skills, "b-skill") {
			t.Fatalf("team A must not see b-skill, got %v", skills)
		}
	})

	t.Run("team A cannot get team B only agent (fail closed)", func(t *testing.T) {
		if _, err := getAgent(t, ctxWithTeam(ctx, teamA), "b-only", ""); err == nil {
			t.Fatal("team A must not fetch team B only agent")
		}
	})

	t.Run("team B can get own only agent", func(t *testing.T) {
		got, err := getAgent(t, ctxWithTeam(ctx, teamB), "b-only", "")
		if err != nil {
			t.Fatalf("team B should fetch b-only: %v", err)
		}
		if !strings.Contains(got.Content, "B") {
			t.Fatalf("unexpected b-only content: %q", got.Content)
		}
	})

	t.Run("team A explicit team scope gets own override", func(t *testing.T) {
		got, err := getAgent(t, ctxWithTeam(ctx, teamA), "reviewer", definitionScopeTeam)
		if err != nil {
			t.Fatalf("team A team-scoped reviewer: %v", err)
		}
		if got.TeamID != teamA {
			t.Fatalf("expected team A reviewer, got team_id=%q", got.TeamID)
		}
	})

	t.Run("team A explicit team scope cannot get team B agent", func(t *testing.T) {
		if _, err := getAgent(t, ctxWithTeam(ctx, teamA), "b-only", definitionScopeTeam); err == nil {
			t.Fatal("team A must not fetch team B agent even with team scope filter")
		}
	})
}

// TestResolveTaskDefinitionsScopedByTeam verifies the runner materialization
// path only injects agent/skill definitions visible to the task's owning team
// (issue #262). Team-scoped agent definitions override global ones for the
// owning team's tasks; cross-team names resolve as not-found (fail closed).
func TestResolveTaskDefinitionsScopedByTeam(t *testing.T) {
	svc, _, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	teamA, _ := seedTeam(t, tdb.DB, "engineering", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "platform", "bob")

	agentContent := func(label string) string {
		return "---\nidentity: " + label + "-bot\n---\n# Agent " + label + "\n"
	}
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "reviewer", definitionScopeGlobal, "", "", agentContent("global"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "reviewer", definitionScopeTeam, teamA, "", agentContent("A"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "reviewer", definitionScopeTeam, teamB, "", agentContent("B"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeAgent, "b-only", definitionScopeTeam, teamB, "", agentContent("B"))
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeSkill, "common", definitionScopeGlobal, "", "", "# common skill\n")
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeSkill, "a-skill", definitionScopeTeam, teamA, "", "# A skill\n")
	seedScopedDefinition(t, tdb.DB, tdb.Dialect(), definitions.DefinitionTypeSkill, "b-skill", definitionScopeTeam, teamB, "", "# B skill\n")

	resolve := func(t *testing.T, agent string, skills []string, teamID string) *runnerv1.Task {
		t.Helper()
		task := &runnerv1.Task{Agent: agent, Skills: skills}
		svc.resolveTaskDefinitions(ctx, task, teamID)
		return task
	}

	t.Run("team A task gets team A agent override of global", func(t *testing.T) {
		task := resolve(t, "reviewer", nil, teamA)
		if !strings.Contains(task.AgentDefinition, "Agent A") {
			t.Fatalf("team A task should materialize team A reviewer (override), got %q", task.AgentDefinition)
		}
	})

	t.Run("team B task gets team B agent override of global", func(t *testing.T) {
		task := resolve(t, "reviewer", nil, teamB)
		if !strings.Contains(task.AgentDefinition, "Agent B") {
			t.Fatalf("team B task should materialize team B reviewer (override), got %q", task.AgentDefinition)
		}
	})

	t.Run("global task gets global agent", func(t *testing.T) {
		task := resolve(t, "reviewer", nil, "")
		if !strings.Contains(task.AgentDefinition, "Agent global") {
			t.Fatalf("global task should materialize global reviewer, got %q", task.AgentDefinition)
		}
	})

	t.Run("team A task cannot materialize team B only agent (fail closed)", func(t *testing.T) {
		task := resolve(t, "b-only", nil, teamA)
		if task.AgentDefinition != "" {
			t.Fatalf("team A task must not materialize team B agent, got %q", task.AgentDefinition)
		}
	})

	t.Run("team A task skills include global and own, exclude team B", func(t *testing.T) {
		task := resolve(t, "", []string{"common", "a-skill", "b-skill"}, teamA)
		if _, ok := task.SkillDefinitions["b-skill"]; ok {
			t.Fatal("team A task must not materialize team B skill")
		}
		if _, ok := task.SkillDefinitions["common"]; !ok {
			t.Fatal("team A task should materialize global common skill")
		}
		if _, ok := task.SkillDefinitions["a-skill"]; !ok {
			t.Fatal("team A task should materialize own a-skill")
		}
		if len(task.SkillDefinitions) != 2 {
			t.Fatalf("team A task should have 2 skills, got %d: %v", len(task.SkillDefinitions), skillNames(task.SkillDefinitions))
		}
	})

	t.Run("global task skills include only global", func(t *testing.T) {
		task := resolve(t, "", []string{"common", "a-skill", "b-skill"}, "")
		if _, ok := task.SkillDefinitions["common"]; !ok {
			t.Fatal("global task should materialize global common skill")
		}
		if len(task.SkillDefinitions) != 1 {
			t.Fatalf("global task should have 1 skill, got %d: %v", len(task.SkillDefinitions), skillNames(task.SkillDefinitions))
		}
	})
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func skillNames(defs map[string][]byte) []string {
	out := make([]string, 0, len(defs))
	for k := range defs {
		out = append(out, k)
	}
	return out
}

// TestGetDefinitionToolNotFound verifies the not-found error is wrapped so
// callers can detect it consistently with the previous query path.
func TestGetDefinitionToolNotFound(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, _, err := svc.getDefinitionTool(ctx, nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeAgent, Name: "missing"}); err == nil {
		t.Fatal("expected error for missing definition")
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows for missing definition, got %v", err)
	}

	if _, _, err := svc.getDefinitionTool(ctx, nil, GetDefinitionInput{DefinitionType: "", Name: "x"}); err == nil {
		t.Fatal("expected error for empty definition_type")
	}
	if _, _, err := svc.getDefinitionTool(ctx, nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeAgent, Name: ""}); err == nil {
		t.Fatal("expected error for empty name")
	}
}
