package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/repository"
)

func TestGitHubRepoFromURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"flatout-works/chetter":                        "flatout-works/chetter",
		"https://github.com/flatout-works/chetter":     "flatout-works/chetter",
		"https://github.com/flatout-works/chetter.git": "flatout-works/chetter",
		"git@github.com:flatout-works/chetter.git":     "flatout-works/chetter",
	}
	for input, want := range cases {
		got, err := githubRepoFromURL(input)
		if err != nil {
			t.Fatalf("githubRepoFromURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("githubRepoFromURL(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := githubRepoFromURL("https://example.com/not/github"); err == nil {
		t.Fatal("expected non-GitHub URL to fail")
	}
}

func TestDefinitionProposalListAndGetTools(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := svc.repo.UpsertDefinitionSource(ctx, repository.UpsertDefinitionSourceParams{
		ID: defaultDefinitionSourceID, Name: defaultDefinitionSourceName, Scope: definitionScopeGlobal,
		RepoUrl: "https://github.com/flatout-works/chetter", Branch: "main", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert definition source: %v", err)
	}
	files, _ := json.Marshal([]DefinitionProposalFile{{Path: "global/agents/task-improver.md"}})
	if err := svc.repo.InsertDefinitionChangeProposal(ctx, repository.InsertDefinitionChangeProposalParams{
		ID:         "dprop_test",
		SourceID:   defaultDefinitionSourceID,
		TaskID:     nullString("task_123"),
		Repo:       "flatout-works/chetter",
		Branch:     "automation/test",
		BaseBranch: "main",
		PrNumber:   123,
		PrUrl:      "https://github.com/flatout-works/chetter/pull/123",
		Title:      "Improve definitions",
		Body:       "Body",
		Files:      files,
		Status:     definitionProposalStatusOpen,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("insert proposal: %v", err)
	}

	_, listOut, err := svc.listDefinitionProposalsTool(ctx, nil, ListDefinitionProposalsInput{SourceID: defaultDefinitionSourceID})
	if err != nil {
		t.Fatalf("list definition proposals: %v", err)
	}
	if len(listOut.Proposals) != 1 || listOut.Proposals[0].ID != "dprop_test" || len(listOut.Proposals[0].Files) != 1 {
		t.Fatalf("unexpected list output: %#v", listOut)
	}

	_, getOut, err := svc.getDefinitionProposalTool(ctx, nil, GetDefinitionProposalInput{Repo: "flatout-works/chetter", PRNumber: 123})
	if err != nil {
		t.Fatalf("get definition proposal: %v", err)
	}
	if getOut.Proposal.PRURL == "" || getOut.Proposal.TaskID != "task_123" {
		t.Fatalf("unexpected get output: %#v", getOut)
	}
}

func TestDefinitionSourceAuthorization(t *testing.T) {
	global := repository.DefinitionSource{Name: "global", Scope: definitionScopeGlobal}
	team := repository.DefinitionSource{Name: "team", Scope: definitionScopeTeam, TeamID: nullString("team-a")}

	teamCtx := auth.WithScope(context.Background(), auth.Scope{TeamID: "team-a"})
	if err := authorizeDefinitionSourceWrite(teamCtx, global); err == nil {
		t.Fatal("team token can write the global definition source")
	}
	if err := authorizeDefinitionSourceWrite(auth.WithScope(context.Background(), auth.Scope{Admin: true}), global); err != nil {
		t.Fatalf("admin cannot write the global definition source: %v", err)
	}
	if err := authorizeDefinitionSourceWrite(teamCtx, team); err != nil {
		t.Fatalf("owning team cannot write its definition source: %v", err)
	}
	otherTeamCtx := auth.WithScope(context.Background(), auth.Scope{TeamID: "team-b"})
	if err := authorizeDefinitionSourceWrite(otherTeamCtx, team); err == nil {
		t.Fatal("non-owning team can write a team definition source")
	}
}

func TestDefinitionProposalScopeFiltersReads(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	teamA, _ := seedTeam(t, tdb.DB, "proposal-a", "alice")
	teamB, _ := seedTeam(t, tdb.DB, "proposal-b", "bob")

	for _, source := range []repository.UpsertDefinitionSourceParams{
		{ID: "source_a", Name: "source-a", Scope: definitionScopeTeam, TeamID: nullString(teamA), RepoUrl: "https://github.com/org/a", Branch: "main", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "source_b", Name: "source-b", Scope: definitionScopeTeam, TeamID: nullString(teamB), RepoUrl: "https://github.com/org/b", Branch: "main", Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := svc.repo.UpsertDefinitionSource(ctx, source); err != nil {
			t.Fatalf("insert definition source %s: %v", source.ID, err)
		}
	}
	insertProposal := func(id, sourceID, repo string, number int32) {
		t.Helper()
		if err := svc.repo.InsertDefinitionChangeProposal(ctx, repository.InsertDefinitionChangeProposalParams{
			ID: id, SourceID: sourceID, Repo: repo, Branch: "automation/test", BaseBranch: "main",
			PrNumber: number, PrUrl: "https://github.com/" + repo + "/pull/" + fmt.Sprint(number),
			Title: id, Body: "body", Files: json.RawMessage(`[]`), Status: definitionProposalStatusOpen,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert proposal %s: %v", id, err)
		}
	}
	insertProposal("proposal_a", "source_a", "org/a", 1)
	insertProposal("proposal_b", "source_b", "org/b", 2)

	teamAContext := auth.WithScope(ctx, auth.Scope{TeamID: teamA})
	_, listed, err := svc.listDefinitionProposalsTool(teamAContext, nil, ListDefinitionProposalsInput{})
	if err != nil {
		t.Fatalf("list team-scoped proposals: %v", err)
	}
	if len(listed.Proposals) != 1 || listed.Proposals[0].ID != "proposal_a" {
		t.Fatalf("team A saw unexpected proposals: %#v", listed.Proposals)
	}
	if _, _, err := svc.getDefinitionProposalTool(teamAContext, nil, GetDefinitionProposalInput{ProposalID: "proposal_b"}); err == nil {
		t.Fatal("team A can read team B proposal")
	}
	if _, out, err := svc.getDefinitionProposalTool(teamAContext, nil, GetDefinitionProposalInput{ProposalID: "proposal_a"}); err != nil {
		t.Fatalf("team A cannot read own proposal: %v", err)
	} else if out.Proposal.ID != "proposal_a" {
		t.Fatalf("team A got proposal %q, want proposal_a", out.Proposal.ID)
	}
}
