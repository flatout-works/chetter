package service

import (
	"context"
	"database/sql"
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
	if defOut.Definition.Path != "skills/chetter/SKILL.md" {
		t.Fatalf("unexpected definition output: %#v", defOut)
	}

	_, _, err = svc.syncDefinitionSourceTool(context.Background(), nil, SyncDefinitionSourceInput{})
	if err == nil {
		t.Fatal("expected non-admin sync definition source to fail")
	}
}

func TestMCPProfileDefinitionsRequireAdmin(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	q := repository.New(tdb.DB)
	insertMCPProfileDefinition(t, q, "chetter-orchestration")

	_, _, err := svc.listDefinitionsTool(context.Background(), nil, ListDefinitionsInput{DefinitionType: definitions.DefinitionTypeMCPProfile})
	if err == nil {
		t.Fatal("expected non-admin mcp_profile list to fail")
	}
	_, _, err = svc.getDefinitionTool(context.Background(), nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeMCPProfile, Name: "chetter-orchestration"})
	if err == nil {
		t.Fatal("expected non-admin mcp_profile get to fail")
	}
	_, listOut, err := svc.listDefinitionsTool(context.Background(), nil, ListDefinitionsInput{})
	if err != nil {
		t.Fatalf("unfiltered non-admin list definitions: %v", err)
	}
	for _, def := range listOut.Definitions {
		if def.DefinitionType == definitions.DefinitionTypeMCPProfile {
			t.Fatalf("non-admin unfiltered list exposed mcp_profile definition: %#v", def)
		}
	}
	_, getOut, err := svc.getDefinitionTool(ctxWithAdmin(context.Background()), nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeMCPProfile, Name: "chetter-orchestration"})
	if err != nil {
		t.Fatalf("admin get mcp_profile definition: %v", err)
	}
	if getOut.Definition.Content == "" {
		t.Fatalf("admin mcp_profile definition content is empty: %#v", getOut.Definition)
	}
}

func TestNonAdminDefinitionSourceRepoURLIsRedacted(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	q := repository.New(tdb.DB)
	now := time.Now().UTC()
	sourceURL := "https://user:secret@example.com/org/private-definitions.git?token=abc#main"
	if err := q.UpsertDefinitionSource(ctx, repository.UpsertDefinitionSourceParams{
		ID:        "source_secret",
		Name:      "secret-definitions",
		Scope:     definitionScopeGlobal,
		TeamID:    sql.NullString{},
		Repo:      sql.NullString{String: "example/private-definitions", Valid: true},
		RepoUrl:   sourceURL,
		Branch:    "main",
		Path:      ".",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert definition source: %v", err)
	}

	_, listOut, err := svc.listDefinitionSourcesTool(ctx, nil, ListDefinitionSourcesInput{})
	if err != nil {
		t.Fatalf("non-admin list definition sources: %v", err)
	}
	if len(listOut.Sources) != 1 {
		t.Fatalf("definition sources = %#v, want one source", listOut.Sources)
	}
	if got, want := listOut.Sources[0].RepoURL, "https://example.com/org/private-definitions.git"; got != want {
		t.Fatalf("non-admin list repo_url = %q, want %q", got, want)
	}

	_, getOut, err := svc.getDefinitionSourceTool(ctx, nil, GetDefinitionSourceInput{Name: "secret-definitions"})
	if err != nil {
		t.Fatalf("non-admin get definition source: %v", err)
	}
	if got, want := getOut.Source.RepoURL, "https://example.com/org/private-definitions.git"; got != want {
		t.Fatalf("non-admin get repo_url = %q, want %q", got, want)
	}

	_, adminOut, err := svc.getDefinitionSourceTool(ctxWithAdmin(ctx), nil, GetDefinitionSourceInput{Name: "secret-definitions"})
	if err != nil {
		t.Fatalf("admin get definition source: %v", err)
	}
	if adminOut.Source.RepoURL != sourceURL {
		t.Fatalf("admin repo_url = %q, want original %q", adminOut.Source.RepoURL, sourceURL)
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
	writeRepoFile(t, dir, "agents/pr-reviewer.md", "# PR reviewer\n")
	writeRepoFile(t, dir, "skills/chetter/SKILL.md", "# Chetter skill\n")
	writeRepoFile(t, dir, "triggers/nightly.yaml", "name: nightly\n")
	writeRepoFile(t, dir, "task-templates/improve.md", "Improve this\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial definitions")
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
