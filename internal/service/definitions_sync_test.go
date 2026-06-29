package service

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
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
	if len(defs) != 5 {
		t.Fatalf("expected 5 definitions, got %d: %#v", len(defs), defs)
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
	if len(runs) != 1 || runs[0].Status != definitionSyncStatusSuccess || runs[0].DefinitionsCount != 5 {
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

	_, profileOut, err := svc.getDefinitionTool(ctxWithAdmin(context.Background()), nil, GetDefinitionInput{DefinitionType: definitions.DefinitionTypeMCPProfile, Name: "chetter-orchestration"})
	if err != nil {
		t.Fatalf("get mcp profile definition tool: %v", err)
	}
	if profileOut.Definition.Path != "mcp-profiles/chetter-orchestration.yaml" {
		t.Fatalf("unexpected mcp profile definition output: %#v", profileOut)
	}

	_, _, err = svc.syncDefinitionSourceTool(context.Background(), nil, SyncDefinitionSourceInput{})
	if err == nil {
		t.Fatal("expected non-admin sync definition source to fail")
	}
}

func TestDefinitionSourceToolsRedactCredentialRepoURLForNonAdmin(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	const rawRepoURL = "https://user:pass@github.com/acme/defs.git?access_token=secret#signature=secret"
	if err := svc.repo.UpsertDefinitionSource(ctx, repository.UpsertDefinitionSourceParams{
		ID:        "defs_secret",
		Name:      "secret-source",
		Scope:     definitionScopeGlobal,
		RepoUrl:   rawRepoURL,
		Branch:    "main",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert source: %v", err)
	}

	_, listOut, err := svc.listDefinitionSourcesTool(ctx, nil, ListDefinitionSourcesInput{})
	if err != nil {
		t.Fatalf("list definition sources: %v", err)
	}
	if len(listOut.Sources) != 1 {
		t.Fatalf("sources = %#v, want one source", listOut.Sources)
	}
	if listOut.Sources[0].RepoURL != "https://github.com/acme/defs.git" {
		t.Fatalf("non-admin list repo_url = %q, want redacted URL", listOut.Sources[0].RepoURL)
	}

	_, getOut, err := svc.getDefinitionSourceTool(ctx, nil, GetDefinitionSourceInput{SourceID: "defs_secret"})
	if err != nil {
		t.Fatalf("get definition source: %v", err)
	}
	if getOut.Source.RepoURL != "https://github.com/acme/defs.git" {
		t.Fatalf("non-admin get repo_url = %q, want redacted URL", getOut.Source.RepoURL)
	}

	_, adminOut, err := svc.getDefinitionSourceTool(ctxWithAdmin(ctx), nil, GetDefinitionSourceInput{SourceID: "defs_secret"})
	if err != nil {
		t.Fatalf("admin get definition source: %v", err)
	}
	if adminOut.Source.RepoURL != rawRepoURL {
		t.Fatalf("admin repo_url = %q, want raw URL", adminOut.Source.RepoURL)
	}
}

func TestSyncDefinitionsRejectsInvalidMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "mcp-profiles/chetter.yaml", "name: chetter\nurl: http://chetter-mcp:8080/mcp\n")
	writeRepoFile(t, repoDir, "triggers/review.yaml", "name: review\nmcp_profiles:\n  - chetter\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add invalid profile")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject invalid mcp profile")
	}
	if !strings.Contains(err.Error(), "invalid mcp profile definitions") || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("sync error = %q, want invalid reserved profile", err)
	}

	runs, runErr := svc.repo.ListDefinitionSyncRuns(context.Background(), repository.ListDefinitionSyncRunsParams{
		Column1:  defaultDefinitionSourceID,
		SourceID: defaultDefinitionSourceID,
		Limit:    5,
	})
	if runErr != nil {
		t.Fatalf("list sync runs: %v", runErr)
	}
	if len(runs) != 1 || runs[0].Status != definitionSyncStatusError || !runs[0].Error.Valid {
		t.Fatalf("unexpected sync runs: %#v", runs)
	}
	if !strings.Contains(runs[0].Error.String, "mcp-profiles/chetter.yaml") {
		t.Fatalf("sync run error = %q, want profile path", runs[0].Error.String)
	}
}

func TestSyncDefinitionsRejectsCredentialedAllowlistedMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "mcp-profiles/chetter-orchestration.yaml", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\nauth:\n  type: bearer\n  token: ${env:CHETTER_MCP_AUTH_TOKEN}\ntool_allowlist:\n  - chetter_submit_task\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add unsafe profile allowlist")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject credentialed allowlisted mcp profile")
	}
	if !strings.Contains(err.Error(), "combines tool_allowlist with credentials") {
		t.Fatalf("sync error = %q, want credentialed allowlist error", err)
	}
}

func TestSyncDefinitionsRejectsPathTokenAllowlistedMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "mcp-profiles/hosted-tools.yaml", "name: hosted-tools\nurl: https://mcp.vendor.example/mcp/tok_live_abc123secret\ntool_allowlist:\n  - safe_tool\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add path token profile")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject path-token allowlisted mcp profile")
	}
	if !strings.Contains(err.Error(), "combines tool_allowlist with credentials") {
		t.Fatalf("sync error = %q, want credentialed allowlist error", err)
	}
}

func TestSyncDefinitionsRejectsOpaquePathAllowlistedMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "mcp-profiles/hosted-tools.yaml", "name: hosted-tools\nurl: https://mcp.vendor.example/mcp/6f2d1c0a9b8e7d6c\ntool_allowlist:\n  - safe_tool\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add opaque path profile")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject opaque-path allowlisted mcp profile")
	}
	if !strings.Contains(err.Error(), "combines tool_allowlist with credentials") {
		t.Fatalf("sync error = %q, want credentialed allowlist error", err)
	}
}

func TestSyncDefinitionsAcceptsExampleConfigRepo(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	exampleDir := filepath.Join(repoRootForTest(t), "examples", "config-repo")
	repoDir := t.TempDir()
	copyTree(t, exampleDir, repoDir)
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "checkout", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test User")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "example config")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("sync example config repo: %v", err)
	}
	_, out, err := svc.getDefinitionTool(ctxWithAdmin(context.Background()), nil, GetDefinitionInput{
		DefinitionType: definitions.DefinitionTypeMCPProfile,
		Name:           "chetter-orchestration",
	})
	if err != nil {
		t.Fatalf("get example mcp profile: %v", err)
	}
	profile, err := definitions.ParseMCPProfileYAML(out.Definition.Content)
	if err != nil {
		t.Fatalf("parse example mcp profile: %v", err)
	}
	if len(profile.ToolAllowlist) != 0 {
		t.Fatalf("example credentialed profile tool_allowlist = %#v, want none", profile.ToolAllowlist)
	}
}

func TestSyncDefinitionsRejectsMissingTriggerMCPProfile(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "triggers/review.yaml", "name: review\ntrigger_type: pr_review\nrepo: flatout-works/chetter\nmcp_profiles:\n  - missing-profile\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add missing profile ref")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(context.Background())
	if err == nil {
		t.Fatal("expected sync to reject missing mcp profile reference")
	}
	if !strings.Contains(err.Error(), `trigger "review" references missing mcp profile "missing-profile"`) {
		t.Fatalf("sync error = %q, want missing profile reference", err)
	}
}

func TestSyncDefinitionsRejectsInvalidCronTrigger(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "triggers/broken-cron.yaml", "name: broken-cron\ncron_expr: not a cron\nprompt: run this\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add invalid cron trigger")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(ctx)
	if err == nil {
		t.Fatal("expected sync to reject invalid cron trigger")
	}
	if !strings.Contains(err.Error(), "triggers/broken-cron.yaml: parse cron") {
		t.Fatalf("sync error = %q, want invalid cron path", err)
	}

	runs, runErr := svc.repo.ListDefinitionSyncRuns(ctx, repository.ListDefinitionSyncRunsParams{
		Column1:  defaultDefinitionSourceID,
		SourceID: defaultDefinitionSourceID,
		Limit:    5,
	})
	if runErr != nil {
		t.Fatalf("list sync runs: %v", runErr)
	}
	if len(runs) != 1 || runs[0].Status != definitionSyncStatusError || !runs[0].Error.Valid {
		t.Fatalf("unexpected sync runs: %#v", runs)
	}
	if _, getErr := repository.New(tdb.DB).GetTriggerByName(ctx, "broken-cron"); getErr == nil {
		t.Fatal("invalid cron trigger should not be persisted")
	}
}

func TestSyncDefinitionsRejectsDuplicateTriggerNames(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "triggers/also-nightly.yaml", "name: Nightly\ncron_expr: '@daily'\nprompt: duplicate trigger\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add duplicate trigger name")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(ctx)
	if err == nil {
		t.Fatal("expected sync to reject duplicate trigger name")
	}
	if !strings.Contains(err.Error(), `duplicate trigger name "Nightly"`) {
		t.Fatalf("sync error = %q, want duplicate trigger name", err)
	}

	runs, runErr := svc.repo.ListDefinitionSyncRuns(ctx, repository.ListDefinitionSyncRunsParams{
		Column1:  defaultDefinitionSourceID,
		SourceID: defaultDefinitionSourceID,
		Limit:    5,
	})
	if runErr != nil {
		t.Fatalf("list sync runs: %v", runErr)
	}
	if len(runs) != 1 || runs[0].Status != definitionSyncStatusError || !runs[0].Error.Valid {
		t.Fatalf("unexpected sync runs: %#v", runs)
	}
	if _, getErr := repository.New(tdb.DB).GetTriggerByName(ctx, "nightly"); getErr == nil {
		t.Fatal("duplicate trigger sync should not persist either trigger")
	}
}

func TestSyncDefinitionsRejectsDynamicTriggerNameCollision(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:        "nightly",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "manual trigger",
		AgentImage:  "runner:latest",
	}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	repoDir := createDefinitionsRepo(t)
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(ctx)
	if err == nil {
		t.Fatal("expected sync to reject dynamic trigger name collision")
	}
	if !strings.Contains(err.Error(), `trigger "nightly" already exists from dynamic source`) {
		t.Fatalf("sync error = %q, want trigger source collision", err)
	}
	row, getErr := repository.New(tdb.DB).GetTriggerByName(ctx, "nightly")
	if getErr != nil {
		t.Fatalf("GetTriggerByName: %v", getErr)
	}
	if row.Prompt != "manual trigger" || row.SourceID.Valid {
		t.Fatalf("dynamic trigger was overwritten: %#v", row)
	}
}

func TestSyncDefinitionsDisablesRemovedGitTrigger(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	repoDir := createDefinitionsRepo(t)
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTriggerByName(ctx, "nightly")
	if err != nil {
		t.Fatalf("GetTriggerByName: %v", err)
	}
	if !row.Enabled {
		t.Fatal("expected synced trigger to start enabled")
	}
	if _, ok := svc.cronEntries[row.ID]; !ok {
		t.Fatal("expected synced cron trigger to be active")
	}

	if err := os.Remove(filepath.Join(repoDir, "triggers", "nightly.yaml")); err != nil {
		t.Fatalf("remove trigger file: %v", err)
	}
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "remove nightly trigger")

	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("sync after removal: %v", err)
	}
	row, err = repository.New(tdb.DB).GetTriggerByName(ctx, "nightly")
	if err != nil {
		t.Fatalf("GetTriggerByName after removal: %v", err)
	}
	if row.Enabled {
		t.Fatal("removed Git trigger should be disabled")
	}
	if _, ok := svc.cronEntries[row.ID]; ok {
		t.Fatal("removed Git trigger should not keep a cron entry")
	}
}

func TestSyncDefinitionsRejectsInvalidTriggerWithoutDisablingPreviousTrigger(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	repoDir := createDefinitionsRepo(t)
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	row, err := repository.New(tdb.DB).GetTriggerByName(ctx, "nightly")
	if err != nil {
		t.Fatalf("GetTriggerByName: %v", err)
	}
	writeRepoFile(t, repoDir, "triggers/nightly.yaml", "enabled: true\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "break nightly trigger")

	if _, err := svc.SyncDefinitions(ctx); err == nil {
		t.Fatal("expected invalid trigger definition to fail sync")
	}
	row, err = repository.New(tdb.DB).GetTriggerByName(ctx, "nightly")
	if err != nil {
		t.Fatalf("GetTriggerByName after failed sync: %v", err)
	}
	if !row.Enabled {
		t.Fatal("invalid trigger definition should not disable previous trigger")
	}
	if _, ok := svc.cronEntries[row.ID]; !ok {
		t.Fatal("invalid trigger definition should not remove previous cron entry")
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
	writeRepoFile(t, dir, "triggers/nightly.yaml", "name: nightly\ncron_expr: '@hourly'\nprompt: run nightly maintenance\n")
	writeRepoFile(t, dir, "mcp-profiles/chetter-orchestration.yaml", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\nauth:\n  type: bearer\n  token: ${env:CHETTER_MCP_AUTH_TOKEN}\n")
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

func repoRootForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func copyTree(t *testing.T, srcRoot, dstRoot string) {
	t.Helper()
	if err := filepath.WalkDir(srcRoot, func(src string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, src)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0644)
	}); err != nil {
		t.Fatalf("copy %s to %s: %v", srcRoot, dstRoot, err)
	}
}
