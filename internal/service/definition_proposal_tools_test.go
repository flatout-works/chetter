package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
