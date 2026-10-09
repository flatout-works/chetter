package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/pkg/definitions"
)

// createDatabaseDraft creates a trigger the way chetter_create_trigger does:
// with no SourceID, which is what makes it a database draft that definitions
// sync must not silently overwrite. It returns the stored row id.
func createDatabaseDraft(t *testing.T, svc *Service, name, prompt string) string {
	t.Helper()
	rec, err := svc.CreateTrigger(context.Background(), store.TriggerInput{
		Name:        name,
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "0 3 * * *",
		Prompt:      prompt,
		AgentImage:  "runner:latest",
		TimeoutSec:  300,
	})
	if err != nil {
		t.Fatalf("create draft trigger %q: %v", name, err)
	}
	return rec.ID
}

// TestSyncDefinitionsRefusesAdoptionOfDatabaseTrigger is the H1 guard: a
// definition whose name matches a database-created draft must fail the sync
// rather than silently overwriting the draft and re-attributing its history.
func TestSyncDefinitionsRefusesAdoptionOfDatabaseTrigger(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	draftID := createDatabaseDraft(t, svc, "nightly-reindex", "the developer's prompt")

	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/nightly-reindex.yaml", content: cronTriggerYAML("nightly-reindex", "0 0 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	_, err := svc.SyncDefinitions(ctx)
	if err == nil {
		t.Fatal("sync should refuse to adopt a database-created trigger without adopt: true")
	}
	if !strings.Contains(err.Error(), "adopt: true") {
		t.Errorf("error should name the opt-in, got: %v", err)
	}

	// The draft must be untouched: same id, same prompt, still unmanaged.
	after, err := svc.repo.GetTriggerByID(ctx, draftID)
	if err != nil {
		t.Fatalf("draft trigger disappeared after refused sync: %v", err)
	}
	if after.SourceID.Valid {
		t.Errorf("draft was re-attributed to source %q", after.SourceID.String)
	}
	if after.Prompt != "the developer's prompt" {
		t.Errorf("draft prompt was overwritten: %q", after.Prompt)
	}
}

// TestSyncDefinitionsAdoptsDatabaseTriggerWithOptIn verifies the deliberate
// path: `adopt: true` takes ownership while preserving the row id and run
// history, which is what makes promotion gapless.
func TestSyncDefinitionsAdoptsDatabaseTriggerWithOptIn(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	draftID := createDatabaseDraft(t, svc, "nightly-reindex", "the developer's prompt")

	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/nightly-reindex.yaml", content: cronTriggerYAMLWithAdopt("nightly-reindex", "0 0 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("sync with adopt: true should succeed: %v", err)
	}

	adopted, err := svc.repo.GetTriggerByID(ctx, draftID)
	if err != nil {
		t.Fatalf("adopted trigger should keep its row id: %v", err)
	}
	if !adopted.SourceID.Valid || adopted.SourceID.String != defaultDefinitionSourceID {
		t.Errorf("adopted trigger source_id = %v, want %q", adopted.SourceID, defaultDefinitionSourceID)
	}
	if adopted.CronExpr != "0 0 * * *" {
		t.Errorf("adopted trigger should take the definition's cron: %q", adopted.CronExpr)
	}
}

// TestSyncDefinitionsCollisionRefusalIsPerTrigger verifies a second, unrelated
// definition still syncs when one collides... by asserting the reverse: the
// whole sync fails, so no partial state is written. This documents the
// fail-closed choice.
func TestSyncDefinitionsCollisionFailsClosed(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	createDatabaseDraft(t, svc, "collides", "prompt")

	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/collides.yaml", content: cronTriggerYAML("collides", "0 0 * * *", true)},
		{path: "global/triggers/unrelated.yaml", content: cronTriggerYAML("unrelated", "0 5 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(ctx); err == nil {
		t.Fatal("expected the sync to fail")
	}
	// The refusal happens before the transaction, so the unrelated trigger must
	// not have been materialized. This is the fail-closed choice: a collision
	// refuses the whole sync rather than writing partial state.
	if _, err := svc.repo.GetTriggerByName(ctx, "unrelated"); err == nil {
		t.Error("no trigger should be materialized when the sync is refused")
	}
}

// TestSyncDefinitionsAdoptionGuardIgnoresManagedRows verifies the guard only
// fires for database-created rows: a definition re-syncing over a row the same
// source already owns must not be treated as a collision.
func TestSyncDefinitionsAdoptionGuardIgnoresManagedRows(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/managed.yaml", content: cronTriggerYAML("managed", "0 0 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// The second sync re-upserts the same managed row; no collision is involved.
	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("resync of a managed trigger should not be refused: %v", err)
	}
}

// --- Phase 0: ownership visibility -----------------------------------------
func TestTriggerToolRecordExposesOwnership(t *testing.T) {
	managed := triggerToolRecord(store.TriggerRecord{
		Name:          "managed",
		SourceID:      "defs_default",
		SourcePath:    "global/triggers/managed.yaml",
		SourceRepoURL: "https://example.invalid/config.git",
	})
	if !managed.Managed {
		t.Error("a trigger with a source_id must report managed=true")
	}
	if managed.Source != sourceKindConfig {
		t.Errorf("source = %q, want %q", managed.Source, sourceKindConfig)
	}
	if managed.SourcePath != "global/triggers/managed.yaml" {
		t.Errorf("source_path not exposed: %q", managed.SourcePath)
	}

	draft := triggerToolRecord(store.TriggerRecord{Name: "draft"})
	if draft.Managed {
		t.Error("a trigger without a source_id must report managed=false")
	}
	if draft.Source != sourceKindDatabase {
		t.Errorf("source = %q, want %q", draft.Source, sourceKindDatabase)
	}
	if draft.SourcePath != "" {
		t.Errorf("a draft has no definition path, got %q", draft.SourcePath)
	}
}

func TestFilterTriggersBySource(t *testing.T) {
	records := []store.TriggerRecord{
		{Name: "draft-a"},
		{Name: "managed-a", SourceID: "defs_default"},
		{Name: "managed-b", SourceID: "defs_other"},
	}
	cases := []struct {
		filter  string
		want    []string
		wantErr bool
	}{
		{filter: "", want: []string{"draft-a", "managed-a", "managed-b"}},
		{filter: "database", want: []string{"draft-a"}},
		{filter: "config", want: []string{"managed-a", "managed-b"}},
		{filter: "defs_other", want: []string{"managed-b"}},
		{filter: "  database  ", want: []string{"draft-a"}},
		{filter: "nonsense", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.filter, func(t *testing.T) {
			got, err := filterTriggersBySource(records, tc.filter)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error for an unknown source filter")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, len(got))
			for i, r := range got {
				names[i] = r.Name
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("filter %q selected %v, want %v", tc.filter, names, tc.want)
			}
		})
	}
}
