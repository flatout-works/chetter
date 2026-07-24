package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/testdb"
)

// TestPruneOldRowsDeletesByAge verifies that PruneOldRows deletes only rows
// older than the TTL and leaves newer rows in place. See issue #112 criteria 1
// and 5.
func TestPruneOldRowsDeletesByAge(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	insertEvent := func(id string, createdAt time.Time) {
		if err := svc.repo.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
			ID:        id,
			TaskID:    "task_prune_age",
			Subject:   "test",
			Status:    "ok",
			EventType: "task.progress",
			Payload:   json.RawMessage(`{}`),
			CreatedAt: createdAt,
		}); err != nil {
			t.Fatalf("InsertTaskEvent %s: %v", id, err)
		}
	}
	insertEvent("evt_old_1", old)
	insertEvent("evt_old_2", old)
	insertEvent("evt_recent_1", recent)
	insertEvent("evt_recent_2", recent)

	// TTL of 24h: the 48h-old rows are pruned; the 1h-old rows stay.
	n, err := svc.store.PruneOldRows(ctx, "chetter_task_events", 24*time.Hour)
	if err != nil {
		t.Fatalf("PruneOldRows: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows pruned, got %d", n)
	}
	if got := countRows(t, tdb, "chetter_task_events"); got != 2 {
		t.Fatalf("expected 2 task_events remaining, got %d", got)
	}
}

// TestPruneOldRowsZeroTTLDeletesNothing verifies the opt-out semantics: a TTL
// of 0 prunes nothing. See issue #112 criteria 6 and 7.
func TestPruneOldRowsZeroTTLDeletesNothing(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.repo.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
		ID:        "evt_zero",
		TaskID:    "task_prune_zero",
		Subject:   "x",
		Status:    "ok",
		EventType: "task.progress",
		Payload:   json.RawMessage(`{}`),
		CreatedAt: time.Now().UTC().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertTaskEvent: %v", err)
	}
	n, err := svc.store.PruneOldRows(ctx, "chetter_task_events", 0)
	if err != nil {
		t.Fatalf("PruneOldRows: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows pruned for zero TTL, got %d", n)
	}
	if got := countRows(t, tdb, "chetter_task_events"); got != 1 {
		t.Fatalf("expected 1 row remaining, got %d", got)
	}
}

// TestPruneRetainedRowsIntegration drives the reaper step end to end: with
// retention enabled via config, the step prunes old rows and leaves recent rows.
// See issue #112 criteria 1, 4, and 8.
func TestPruneRetainedRowsIntegration(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertEvent := func(id string, createdAt time.Time) {
		if err := svc.repo.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
			ID: id, TaskID: "task_prune_step", Subject: "x", Status: "ok",
			EventType: "task.progress", Payload: json.RawMessage(`{}`), CreatedAt: createdAt,
		}); err != nil {
			t.Fatalf("InsertTaskEvent %s: %v", id, err)
		}
	}
	insertEvent("evt_step_old", now.Add(-48*time.Hour))
	insertEvent("evt_step_recent", now.Add(-1*time.Hour))

	// 1-day TTL -> prunes the 48h-old row, keeps the 1h-old row.
	svc.cfg.EventsRetentionDays = 1
	svc.pruneRetainedRows()

	if got := countRows(t, tdb, "chetter_task_events"); got != 1 {
		t.Fatalf("expected 1 task_event remaining after prune step, got %d", got)
	}
}

// countRows returns the row count of table. The table argument is a hardcoded
// constant from this file, so interpolating it is safe.
func countRows(t *testing.T, tdb *testdb.TestDB, table string) int {
	t.Helper()
	var n int
	if err := tdb.DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
