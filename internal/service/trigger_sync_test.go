package service

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/definitions"
	cron "github.com/robfig/cron/v3"
)

// TestTriggerIDDeterministic verifies synced trigger identity is a stable
// function of (source ID, name): the same key always maps to the same ID, and
// renaming mints a different ID (delete old + create new).
func TestTriggerIDDeterministic(t *testing.T) {
	id1 := triggerID(defaultDefinitionSourceID, "nightly")
	id2 := triggerID(defaultDefinitionSourceID, "nightly")
	if id1 != id2 {
		t.Fatalf("triggerID not deterministic: %q vs %q", id1, id2)
	}
	if !strings.HasPrefix(id1, "trig_") || len(id1) != len("trig_")+32 {
		t.Fatalf("triggerID has unexpected shape: %q", id1)
	}
	if triggerID(defaultDefinitionSourceID, "daily") == id1 {
		t.Fatal("different trigger names must produce different IDs")
	}
	if triggerID("defs_other", "nightly") == id1 {
		t.Fatal("different source IDs must produce different IDs")
	}
}

// TestSyncedTriggersBeforeFiltersBySource verifies only triggers owned by the
// default definition source are tracked for cron reconciliation; API-managed
// and other-source triggers are left alone.
func TestSyncedTriggersBeforeFiltersBySource(t *testing.T) {
	now := time.Now().UTC()
	existing := []repository.ChetterTrigger{
		{ID: "trig_a", Name: "a", SourceID: sql.NullString{String: defaultDefinitionSourceID, Valid: true}, CreatedAt: now},
		{ID: "trig_b", Name: "b", SourceID: sql.NullString{String: defaultDefinitionSourceID, Valid: true}, CreatedAt: now},
		{ID: "trig_api", Name: "api", SourceID: sql.NullString{}, CreatedAt: now},
		{ID: "trig_other", Name: "other", SourceID: sql.NullString{String: "defs_other", Valid: true}, CreatedAt: now},
	}
	byName, ids := syncedTriggersBefore(existing)
	if len(ids) != 2 {
		t.Fatalf("expected 2 managed-before IDs, got %d: %v", len(ids), ids)
	}
	if _, ok := ids["trig_a"]; !ok {
		t.Fatalf("expected trig_a in managed-before IDs, got %v", ids)
	}
	if _, ok := ids["trig_b"]; !ok {
		t.Fatalf("expected trig_b in managed-before IDs, got %v", ids)
	}
	if _, ok := ids["trig_api"]; ok {
		t.Fatalf("API trigger must be excluded, got %v", ids)
	}
	if _, ok := ids["trig_other"]; ok {
		t.Fatalf("other-source trigger must be excluded, got %v", ids)
	}
	if byName["a"].ID != "trig_a" || byName["b"].ID != "trig_b" {
		t.Fatalf("unexpected byName map: %v", byName)
	}
}

// TestDesiredSyncedTriggerIDsReusesStoredID verifies existing triggers keep
// their stored ID (preserved by the name-keyed upsert), new triggers use the
// deterministic ID, and only enabled cron triggers land in the cron subset.
func TestDesiredSyncedTriggerIDsReusesStoredID(t *testing.T) {
	now := time.Now().UTC()
	managedBefore := map[string]repository.ChetterTrigger{
		"keep":    {ID: "trig_existing", Name: "keep", SourceID: sql.NullString{String: defaultDefinitionSourceID, Valid: true}, CreatedAt: now},
		"disable": {ID: "trig_disable", Name: "disable", SourceID: sql.NullString{String: defaultDefinitionSourceID, Valid: true}, CreatedAt: now},
	}
	entries := []triggerSyncEntry{
		{def: definitions.TriggerDef{Name: "keep", TriggerType: "cron", Enabled: true, CronExpr: "0 0 * * *"},
			params: repository.UpsertTriggerParams{ID: triggerID(defaultDefinitionSourceID, "keep")}},
		{def: definitions.TriggerDef{Name: "disable", TriggerType: "cron", Enabled: false, CronExpr: "0 0 * * *"},
			params: repository.UpsertTriggerParams{ID: triggerID(defaultDefinitionSourceID, "disable")}},
		{def: definitions.TriggerDef{Name: "fresh", TriggerType: "cron", Enabled: true, CronExpr: "0 0 * * *"},
			params: repository.UpsertTriggerParams{ID: triggerID(defaultDefinitionSourceID, "fresh")}},
		{def: definitions.TriggerDef{Name: "review", TriggerType: "pr_review", Enabled: true},
			params: repository.UpsertTriggerParams{ID: triggerID(defaultDefinitionSourceID, "review")}},
	}
	desired, cronEnabled := desiredSyncedTriggerIDs(entries, managedBefore)

	// Existing triggers reuse their stored ID; new triggers use the deterministic ID.
	if _, ok := desired["trig_existing"]; !ok {
		t.Errorf("keep: existing stored ID should be desired, got %v", desired)
	}
	if _, ok := desired["trig_disable"]; !ok {
		t.Errorf("disable: existing stored ID should be desired, got %v", desired)
	}
	if _, ok := desired[triggerID(defaultDefinitionSourceID, "fresh")]; !ok {
		t.Errorf("fresh: deterministic ID should be desired, got %v", desired)
	}
	if _, ok := desired[triggerID(defaultDefinitionSourceID, "review")]; !ok {
		t.Errorf("review: deterministic ID should be desired, got %v", desired)
	}
	// The deterministic ID for "keep" must NOT be treated as desired: the stored
	// row keeps its existing ID via the name-keyed upsert.
	if _, ok := desired[triggerID(defaultDefinitionSourceID, "keep")]; ok {
		t.Errorf("keep: deterministic ID should not be desired when a stored ID exists, got %v", desired)
	}

	// Only enabled cron triggers are in the cron subset.
	if len(cronEnabled) != 2 {
		t.Fatalf("expected 2 enabled cron trigger IDs, got %d: %v", len(cronEnabled), cronEnabled)
	}
	if _, ok := cronEnabled["trig_existing"]; !ok {
		t.Errorf("keep (enabled cron) should be in cron subset, got %v", cronEnabled)
	}
	if _, ok := cronEnabled[triggerID(defaultDefinitionSourceID, "fresh")]; !ok {
		t.Errorf("fresh (enabled cron) should be in cron subset, got %v", cronEnabled)
	}
	if _, ok := cronEnabled["trig_disable"]; ok {
		t.Errorf("disable (disabled cron) should not be in cron subset, got %v", cronEnabled)
	}
	if _, ok := cronEnabled[triggerID(defaultDefinitionSourceID, "review")]; ok {
		t.Errorf("review (pr_review) should not be in cron subset, got %v", cronEnabled)
	}
}

// TestReconcileSyncedCronEntries verifies the in-memory cron registry is
// reconciled against the desired set: deleted/renamed and disabled/non-cron
// triggers drop their schedules, enabled cron triggers keep theirs, and
// API-managed triggers are never touched. Reconcile never adds entries —
// activation does.
func TestReconcileSyncedCronEntries(t *testing.T) {
	c := cron.New(cron.WithParser(defaultCronParser), cron.WithLocation(time.UTC))
	svc := &Service{cron: c, cronEntries: make(map[string]cron.EntryID)}

	addEntry := func(id, expr string) cron.EntryID {
		t.Helper()
		eid, err := c.AddFunc(expr, func() {})
		if err != nil {
			t.Fatalf("AddFunc %s: %v", id, err)
		}
		svc.cronEntries[id] = eid
		return eid
	}
	addEntry("trig_a", "0 1 * * *")               // source-managed, will be deleted
	addEntry("trig_b", "0 2 * * *")               // source-managed, will be disabled
	addEntry("trig_c", "0 3 * * *")               // source-managed, stays enabled cron
	apiEntry := addEntry("trig_api", "0 4 * * *") // API-managed, must be untouched
	if got := len(c.Entries()); got != 4 {
		t.Fatalf("expected 4 cron entries pre-reconcile, got %d", got)
	}

	managedBefore := map[string]struct{}{"trig_a": {}, "trig_b": {}, "trig_c": {}}
	desired := map[string]struct{}{"trig_b": {}, "trig_c": {}, "trig_d": {}}
	desiredCron := map[string]struct{}{"trig_c": {}}
	svc.reconcileSyncedCronEntries(managedBefore, desired, desiredCron)

	if _, ok := svc.cronEntries["trig_a"]; ok {
		t.Error("trig_a: deleted trigger cron entry should be removed")
	}
	if _, ok := svc.cronEntries["trig_b"]; ok {
		t.Error("trig_b: disabled desired trigger cron entry should be removed")
	}
	if _, ok := svc.cronEntries["trig_c"]; !ok {
		t.Error("trig_c: enabled cron trigger entry should be kept")
	}
	if _, ok := svc.cronEntries["trig_d"]; ok {
		t.Error("trig_d: reconcile must not add new entries (activation does)")
	}
	if eid, ok := svc.cronEntries["trig_api"]; !ok || eid != apiEntry {
		t.Error("trig_api: API-managed cron entry must be left untouched")
	}
	if got := len(c.Entries()); got != 2 {
		t.Fatalf("expected 2 cron entries post-reconcile (c + api), got %d", got)
	}
}

// TestParseTriggerDefsForSyncUsesDeterministicID verifies the sync parser mints
// a deterministic ID keyed on (source, name) and stamps the source_id, instead
// of a fresh random ID per sync.
func TestParseTriggerDefsForSyncUsesDeterministicID(t *testing.T) {
	svc := &Service{}
	defs := []definitions.Definition{{
		Type:    definitions.DefinitionTypeTrigger,
		Name:    "nightly",
		Path:    "global/triggers/nightly.yaml",
		Content: cronTriggerYAML("nightly", "0 0 * * *", true),
	}}
	entries, err := svc.parseTriggerDefsForSync(defs, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("parseTriggerDefsForSync: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	want := triggerID(defaultDefinitionSourceID, "nightly")
	if entries[0].params.ID != want {
		t.Fatalf("entry ID = %q, want deterministic %q", entries[0].params.ID, want)
	}
	if !entries[0].params.SourceID.Valid || entries[0].params.SourceID.String != defaultDefinitionSourceID {
		t.Fatalf("entry source_id = %+v, want %q", entries[0].params.SourceID, defaultDefinitionSourceID)
	}
	if entries[0].params.Name != "nightly" || entries[0].params.TriggerType != "cron" || !entries[0].params.Enabled {
		t.Fatalf("unexpected entry params: %+v", entries[0].params)
	}
}
