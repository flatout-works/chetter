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
	n, err := svc.store.PruneOldRows(ctx, "task_events", 24*time.Hour)
	if err != nil {
		t.Fatalf("PruneOldRows: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows pruned, got %d", n)
	}
	if got := countRows(t, tdb, "task_events"); got != 2 {
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
	n, err := svc.store.PruneOldRows(ctx, "task_events", 0)
	if err != nil {
		t.Fatalf("PruneOldRows: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows pruned for zero TTL, got %d", n)
	}
	if got := countRows(t, tdb, "task_events"); got != 1 {
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

	if got := countRows(t, tdb, "task_events"); got != 1 {
		t.Fatalf("expected 1 task_event remaining after prune step, got %d", got)
	}
}

// TestPruneTerminalDeliveriesIntegration verifies delivery retention only
// deletes terminal delivery rows past the TTL: pending, in-flight, processing,
// received, failed and retry_wait rows survive regardless of age, so retention
// can never discard work that is still eligible for processing or a retry. See
// issue #253.
func TestPruneTerminalDeliveriesIntegration(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	exec := func(what, mysql, postgres string, args ...any) {
		t.Helper()
		if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(), mysql, postgres), args...); err != nil {
			t.Fatalf("insert %s: %v", what, err)
		}
	}
	insertCallback := func(id, status string, at time.Time) {
		exec("callback_deliveries",
			`INSERT INTO callback_deliveries (id, callback_id, event_id, event_type, endpoint_url, payload, status, created_at, updated_at) VALUES (?, ?, ?, 'task.completed', 'https://example.com/hook', '{}', ?, ?, ?)`,
			`INSERT INTO callback_deliveries (id, callback_id, event_id, event_type, endpoint_url, payload, status, created_at, updated_at) VALUES ($1, $2, $3, 'task.completed', 'https://example.com/hook', '{}', $4, $5, $6)`,
			id, "cb_"+id, "evt_"+id, status, at, at)
	}
	insertInbound := func(id, status string, at time.Time) {
		exec("inbound_deliveries",
			`INSERT INTO inbound_deliveries (id, endpoint_id, payload, status, created_at, updated_at) VALUES (?, ?, '{}', ?, ?, ?)`,
			`INSERT INTO inbound_deliveries (id, endpoint_id, payload, status, created_at, updated_at) VALUES ($1, $2, '{}', $3, $4, $5)`,
			id, "ep_"+id, status, at, at)
	}
	insertWebhook := func(id, status string, at time.Time) {
		exec("webhook_deliveries",
			`INSERT INTO webhook_deliveries (id, delivery_id, event_type, event_action, payload, status, created_at, updated_at) VALUES (?, ?, 'issues', 'opened', '{}', ?, ?, ?)`,
			`INSERT INTO webhook_deliveries (id, delivery_id, event_type, event_action, payload, status, created_at, updated_at) VALUES ($1, $2, 'issues', 'opened', '{}', $3, $4, $5)`,
			id, "dlv_"+id, status, at, at)
	}

	// Terminal, old -> pruned.
	insertCallback("cb_completed_old", "completed", old)
	insertCallback("cb_dead_old", "dead_letter", old)
	// Non-terminal, old -> retained.
	insertCallback("cb_pending_old", "pending", old)
	insertCallback("cb_failed_old", "failed", old)
	insertCallback("cb_inflight_old", "in_flight", old)
	// Terminal, recent -> retained.
	insertCallback("cb_completed_recent", "completed", recent)

	insertInbound("ib_succeeded_old", "succeeded", old)
	insertInbound("ib_failed_perm_old", "failed_permanent", old)
	insertInbound("ib_dead_old", "dead_letter", old)
	insertInbound("ib_retry_old", "retry_wait", old)
	insertInbound("ib_processing_old", "processing", old)
	insertInbound("ib_succeeded_recent", "succeeded", recent)

	insertWebhook("wh_completed_old", "completed", old)
	insertWebhook("wh_dead_old", "dead_letter", old)
	insertWebhook("wh_failed_old", "failed", old)
	insertWebhook("wh_processing_old", "processing", old)
	insertWebhook("wh_received_old", "received", old)
	insertWebhook("wh_completed_recent", "completed", recent)

	svc.cfg.DeliveryRetentionDays = 1
	svc.pruneRetainedRows()

	// callback_deliveries: 2 terminal-old pruned; 3 non-terminal-old + 1 recent remain.
	if got := countRows(t, tdb, "callback_deliveries"); got != 4 {
		t.Errorf("callback_deliveries: want 4 rows, got %d", got)
	}
	// inbound_deliveries: 3 terminal-old pruned; 2 non-terminal-old + 1 recent remain.
	if got := countRows(t, tdb, "inbound_deliveries"); got != 3 {
		t.Errorf("inbound_deliveries: want 3 rows, got %d", got)
	}
	// webhook_deliveries: 2 terminal-old pruned; 3 non-terminal-old + 1 recent remain.
	if got := countRows(t, tdb, "webhook_deliveries"); got != 4 {
		t.Errorf("webhook_deliveries: want 4 rows, got %d", got)
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
