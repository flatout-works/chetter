package service

import (
	"context"
	"fmt"
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
	agents, err := svc.ListAgentDefinitions(context.Background(), nil, nil, "")
	if err != nil {
		t.Fatalf("list agent definitions: %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "pr-reviewer" {
		t.Fatalf("unexpected agent definitions: %#v", agents)
	}
	triggerBefore, err := svc.repo.GetTriggerByName(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("get trigger before resync: %v", err)
	}
	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("resync definitions: %v", err)
	}
	triggerAfter, err := svc.repo.GetTriggerByName(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("get trigger after resync: %v", err)
	}
	if triggerBefore.ID != triggerAfter.ID {
		t.Fatalf("trigger ID changed across definition sync: %q -> %q", triggerBefore.ID, triggerAfter.ID)
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

// triggerFile is a trigger definition file written into a definitions repo.
type triggerFile struct {
	path    string
	content string
}

// cronTriggerYAML builds a minimal enabled/disabled cron trigger definition
// with a parseable cron expression so activateTrigger can register it.
func cronTriggerYAML(name, cronExpr string, enabled bool) string {
	return fmt.Sprintf("name: %s\ncron_expr: %q\nenabled: %t\n", name, cronExpr, enabled)
}

func createTriggerDefinitionsRepo(t *testing.T, triggers []triggerFile) string {
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
	for _, tf := range triggers {
		writeRepoFile(t, dir, tf.path, tf.content)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial triggers")
	return dir
}

// rewriteTriggerRepo replaces the global trigger definitions in the repo and
// commits, so the next sync fast-forwards to the new state. Removed trigger
// files are staged for deletion via `git add -A`.
func rewriteTriggerRepo(t *testing.T, dir string, triggers []triggerFile) {
	t.Helper()
	triggerDir := filepath.Join(dir, "global", "triggers")
	if entries, err := os.ReadDir(triggerDir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(triggerDir, e.Name()))
		}
	}
	for _, tf := range triggers {
		writeRepoFile(t, dir, tf.path, tf.content)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "--allow-empty", "-m", "update triggers")
}

func cronEntryExists(svc *Service, id string) bool {
	svc.cronMu.Lock()
	defer svc.cronMu.Unlock()
	_, ok := svc.cronEntries[id]
	return ok
}

func cronEntryIDs(svc *Service) []string {
	svc.cronMu.Lock()
	defer svc.cronMu.Unlock()
	out := make([]string, 0, len(svc.cronEntries))
	for id := range svc.cronEntries {
		out = append(out, id)
	}
	return out
}

// TestSyncDefinitionsPreservesTriggerIdentityAndCronEntry verifies that re-syncing
// an unchanged trigger definition keeps a stable trigger ID and exactly one
// in-memory cron registration (no duplication, no stale entries).
func TestSyncDefinitionsPreservesTriggerIdentityAndCronEntry(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/nightly.yaml", content: cronTriggerYAML("nightly", "0 0 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	trigger, err := svc.repo.GetTriggerByName(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("get trigger: %v", err)
	}
	if want := triggerID(defaultDefinitionSourceID, "nightly"); trigger.ID != want {
		t.Fatalf("trigger ID = %q, want deterministic %q", trigger.ID, want)
	}
	if ids := cronEntryIDs(svc); len(ids) != 1 || !cronEntryExists(svc, trigger.ID) {
		t.Fatalf("expected single cron entry for %s, got %v", trigger.ID, ids)
	}

	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("resync: %v", err)
	}
	resynced, err := svc.repo.GetTriggerByName(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("get trigger after resync: %v", err)
	}
	if resynced.ID != trigger.ID {
		t.Fatalf("trigger ID changed across sync: %q -> %q", trigger.ID, resynced.ID)
	}
	if ids := cronEntryIDs(svc); len(ids) != 1 || !cronEntryExists(svc, resynced.ID) {
		t.Fatalf("expected single cron entry after resync, got %v", ids)
	}
}

// TestSyncDefinitionsCleansUpStaleCronEntries verifies that removing a trigger
// definition deletes its DB row and tears down its in-memory cron schedule on
// the next sync, while remaining and newly-added triggers keep their schedules.
func TestSyncDefinitionsCleansUpStaleCronEntries(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/alpha.yaml", content: cronTriggerYAML("alpha", "0 0 * * *", true)},
		{path: "global/triggers/beta.yaml", content: cronTriggerYAML("beta", "0 12 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	alpha, err := svc.repo.GetTriggerByName(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	beta, err := svc.repo.GetTriggerByName(context.Background(), "beta")
	if err != nil {
		t.Fatalf("get beta: %v", err)
	}
	if ids := cronEntryIDs(svc); len(ids) != 2 || !cronEntryExists(svc, alpha.ID) || !cronEntryExists(svc, beta.ID) {
		t.Fatalf("expected cron entries for alpha and beta, got %v", ids)
	}

	rewriteTriggerRepo(t, repoDir, []triggerFile{
		{path: "global/triggers/beta.yaml", content: cronTriggerYAML("beta", "0 12 * * *", true)},
		{path: "global/triggers/gamma.yaml", content: cronTriggerYAML("gamma", "0 6 * * *", true)},
	})
	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if _, err := svc.repo.GetTriggerByName(context.Background(), "alpha"); err == nil {
		t.Fatal("alpha trigger row should be deleted after sync")
	}
	gamma, err := svc.repo.GetTriggerByName(context.Background(), "gamma")
	if err != nil {
		t.Fatalf("get gamma: %v", err)
	}
	if cronEntryExists(svc, alpha.ID) {
		t.Fatalf("stale cron entry for deleted trigger alpha still present: %v", cronEntryIDs(svc))
	}
	if ids := cronEntryIDs(svc); len(ids) != 2 || !cronEntryExists(svc, beta.ID) || !cronEntryExists(svc, gamma.ID) {
		t.Fatalf("expected cron entries for beta and gamma only, got %v", ids)
	}
}

// TestSyncDefinitionsDisablesCronTriggerWithoutDeletingRow verifies that
// toggling a trigger definition to enabled:false keeps the row (updated in
// place) but removes its in-memory cron schedule so it no longer fires.
func TestSyncDefinitionsDisablesCronTriggerWithoutDeletingRow(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/nightly.yaml", content: cronTriggerYAML("nightly", "0 0 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	trigger, err := svc.repo.GetTriggerByName(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("get trigger: %v", err)
	}
	if !cronEntryExists(svc, trigger.ID) {
		t.Fatalf("enabled cron trigger should have a cron entry")
	}

	rewriteTriggerRepo(t, repoDir, []triggerFile{
		{path: "global/triggers/nightly.yaml", content: cronTriggerYAML("nightly", "0 0 * * *", false)},
	})
	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("resync: %v", err)
	}
	disabled, err := svc.repo.GetTriggerByName(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("disabled trigger row should still exist: %v", err)
	}
	if disabled.ID != trigger.ID {
		t.Fatalf("trigger ID changed when disabling: %q -> %q", trigger.ID, disabled.ID)
	}
	if disabled.Enabled {
		t.Fatalf("trigger should be disabled, got enabled=%v", disabled.Enabled)
	}
	if cronEntryExists(svc, disabled.ID) {
		t.Fatal("disabled cron trigger should not have a cron entry")
	}
}

// TestSyncDefinitionsRenameIsDeleteOldCreateNew verifies the documented rename
// semantics: renaming a trigger definition deletes the old trigger (and its
// cron schedule) and creates a new trigger with a fresh ID. Run history does
// not follow the rename.
func TestSyncDefinitionsRenameIsDeleteOldCreateNew(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/oldname.yaml", content: cronTriggerYAML("oldname", "0 0 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	old, err := svc.repo.GetTriggerByName(context.Background(), "oldname")
	if err != nil {
		t.Fatalf("get old trigger: %v", err)
	}
	if !cronEntryExists(svc, old.ID) {
		t.Fatalf("old trigger should have a cron entry")
	}

	rewriteTriggerRepo(t, repoDir, []triggerFile{
		{path: "global/triggers/newname.yaml", content: cronTriggerYAML("newname", "0 0 * * *", true)},
	})
	if _, err := svc.SyncDefinitions(context.Background()); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if _, err := svc.repo.GetTriggerByName(context.Background(), "oldname"); err == nil {
		t.Fatal("old trigger row should be deleted after rename")
	}
	renamed, err := svc.repo.GetTriggerByName(context.Background(), "newname")
	if err != nil {
		t.Fatalf("new trigger should exist after rename: %v", err)
	}
	if renamed.ID == old.ID {
		t.Fatalf("rename should mint a new trigger ID, got same %q", renamed.ID)
	}
	if want := triggerID(defaultDefinitionSourceID, "newname"); renamed.ID != want {
		t.Fatalf("renamed trigger ID = %q, want deterministic %q", renamed.ID, want)
	}
	if cronEntryExists(svc, old.ID) {
		t.Fatalf("cron entry for old trigger should be removed after rename: %v", cronEntryIDs(svc))
	}
	if !cronEntryExists(svc, renamed.ID) {
		t.Fatal("cron entry for renamed trigger should be present")
	}
}
