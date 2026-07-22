package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/definitions"
)

func TestSyncDefinitionsMaterializesRegistry(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createDefinitionsRepo(t)
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	record, err := svc.SyncDefinitions(context.Background())
	if err != nil {
		t.Fatalf("sync definitions: %v", err)
	}
	if record.Name != "definitions" {
		t.Fatalf("expected synced model catalog record, got %#v", record)
	}

	source, err := svc.repo.GetDefinitionSource(context.Background(), defaultDefinitionSourceID)
	if err != nil {
		t.Fatalf("get definition source: %v", err)
	}
	if source.RepoUrl != repoDir || source.Scope != definitionScopeGlobal || !source.LastSyncAt.Valid {
		t.Fatalf("unexpected source row: %#v", source)
	}

	defs, err := svc.repo.ListDefinitions(context.Background(), repository.ListDefinitionsParams{
		Column1:        "",
		DefinitionType: "",
		Column3:        "",
		SourceID:       "",
	})
	if err != nil {
		t.Fatalf("list definitions: %v", err)
	}
	if len(defs) != 4 {
		t.Fatalf("expected 4 definitions, got %d: %#v", len(defs), defs)
	}
	for _, def := range defs {
		if def.SourceCommit == "" || def.ContentHash == "" || !def.Active {
			t.Fatalf("definition missing materialized metadata: %#v", def)
		}
	}

	runs, err := svc.repo.ListDefinitionSyncRuns(context.Background(), repository.ListDefinitionSyncRunsParams{
		Column1:  defaultDefinitionSourceID,
		SourceID: defaultDefinitionSourceID,
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("list sync runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != definitionSyncStatusSuccess || runs[0].DefinitionsCount != 4 {
		t.Fatalf("unexpected sync runs: %#v", runs)
	}

	_, sourcesOut, err := svc.listDefinitionSourcesTool(context.Background(), nil, ListDefinitionSourcesInput{})
	if err != nil {
		t.Fatalf("list definition sources tool: %v", err)
	}
	if len(sourcesOut.Sources) != 1 || sourcesOut.Sources[0].ID != defaultDefinitionSourceID || sourcesOut.Sources[0].LastSyncAt == nil {
		t.Fatalf("unexpected source tool output: %#v", sourcesOut)
	}

	_, defsOut, err := svc.listDefinitionsTool(context.Background(), nil, ListDefinitionsInput{DefinitionType: definitions.DefinitionTypeAgent})
	if err != nil {
		t.Fatalf("list definitions tool: %v", err)
	}
	if len(defsOut.Definitions) != 1 || defsOut.Definitions[0].Name != "pr-reviewer" || defsOut.Definitions[0].Content == "" {
		t.Fatalf("unexpected definitions tool output: %#v", defsOut)
	}

	_, defOut, err := svc.getDefinitionTool(context.Background(), nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeSkill, Name: "chetter"})
	if err != nil {
		t.Fatalf("get definition tool: %v", err)
	}
	if defOut.Definition.Path != "global/skills/chetter/SKILL.md" {
		t.Fatalf("unexpected definition output: %#v", defOut)
	}

	_, _, err = svc.syncDefinitionSourceTool(context.Background(), nil, SyncDefinitionSourceInput{})
	if err == nil {
		t.Fatal("expected non-admin sync definition source to fail")
	}
}

func TestSyncDefinitionsScopedLayout(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	now := time.Now().UTC()
	if err := svc.repo.CreateTeam(context.Background(), repository.CreateTeamParams{
		ID:        "team_eng",
		Name:      "engineering",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	repoDir := createScopedDefinitionsRepo(t)
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("sync definitions: %v", err)
	}

	defs, err := svc.repo.ListDefinitions(context.Background(), repository.ListDefinitionsParams{
		Column1:        "",
		DefinitionType: "",
		Column3:        "",
		SourceID:       "",
	})
	if err != nil {
		t.Fatalf("list definitions: %v", err)
	}
	byPath := map[string]repository.Definition{}
	for _, def := range defs {
		byPath[def.Path] = def
	}
	globalDef := byPath["global/agents/global-reviewer.md"]
	if globalDef.Scope != definitionScopeGlobal || globalDef.TeamID.Valid || globalDef.Repo.Valid {
		t.Fatalf("unexpected global definition: %#v", globalDef)
	}
	teamDef := byPath["groups/engineering/triggers/team-nightly.yaml"]
	if teamDef.Scope != definitionScopeTeam || !teamDef.TeamID.Valid || teamDef.TeamID.String != "team_eng" {
		t.Fatalf("unexpected team definition: %#v", teamDef)
	}
	repoDef := byPath["repos/acme/app/agents/repo-reviewer.md"]
	if repoDef.Scope != definitionScopeRepo || !repoDef.Repo.Valid || repoDef.Repo.String != "acme/app" || repoDef.TeamID.Valid {
		t.Fatalf("unexpected repo definition: %#v", repoDef)
	}
	trigger, err := svc.repo.GetTriggerByName(context.Background(), "team-nightly")
	if err != nil {
		t.Fatalf("get synced trigger: %v", err)
	}
	if !trigger.TeamID.Valid || trigger.TeamID.String != "team_eng" {
		t.Fatalf("expected group-scoped trigger to be team-owned, got %#v", trigger)
	}
}

func createDefinitionsRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	writeRepoFile(t, dir, "model-catalog.yaml", `version: 1
default_provider: test
default_model: test-model
providers:
  test:
    name: Test
    kind: openai_compatible
    models:
      - id: test-model
`)
	writeRepoFile(t, dir, "global/agents/pr-reviewer.md", "---\nidentity: primary-bot\n---\n# PR reviewer\n")
	writeRepoFile(t, dir, "global/skills/chetter/SKILL.md", "# Chetter skill\n")
	writeRepoFile(t, dir, "global/triggers/nightly.yaml", "name: nightly\n")
	writeRepoFile(t, dir, "global/task-templates/improve.md", "Improve this\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial definitions")
	return dir
}

func createScopedDefinitionsRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	writeRepoFile(t, dir, "model-catalog.yaml", `version: 1
default_provider: test
default_model: test-model
providers:
  test:
    name: Test
    kind: openai_compatible
    models:
      - id: test-model
`)
	writeRepoFile(t, dir, "global/agents/global-reviewer.md", "---\nidentity: primary-bot\n---\n# Global reviewer\n")
	writeRepoFile(t, dir, "groups/engineering/triggers/team-nightly.yaml", "name: team-nightly\n")
	writeRepoFile(t, dir, "repos/acme/app/agents/repo-reviewer.md", "---\nidentity: primary-bot\n---\n# Repo reviewer\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "scoped definitions")
	return dir
}

func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}
