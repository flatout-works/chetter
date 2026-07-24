package store

import (
	"context"
	"testing"
	"time"
)

// TestPruneOldRowsZeroTTLIsDisabled verifies that a zero (or negative) TTL is a
// no-op that returns without touching the database. This is the safe default
// that keeps existing deployments unaffected until an operator opts in. See
// issue #112 criteria 6 and 7.
func TestPruneOldRowsZeroTTLIsDisabled(t *testing.T) {
	s := &Store{} // nil db; the disabled path must return before any DB access
	ctx := context.Background()

	for _, ttl := range []time.Duration{0, -1 * time.Second, -24 * time.Hour} {
		n, err := s.PruneOldRows(ctx, "chetter_task_events", ttl)
		if err != nil {
			t.Fatalf("ttl=%v: expected nil error, got %v", ttl, err)
		}
		if n != 0 {
			t.Fatalf("ttl=%v: expected 0 rows pruned, got %d", ttl, n)
		}
	}
}

// TestPruneOldRowsRejectsUnknownTable verifies the allowlist guard: only the
// known retention tables may be pruned. The guard runs before any database
// access, so it is exercised without a DB. See issue #112.
func TestPruneOldRowsRejectsUnknownTable(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	if _, err := s.PruneOldRows(ctx, "chetter_tasks", time.Hour); err == nil {
		t.Fatal("expected error for unknown table, got nil")
	}
	if _, err := s.PruneOldRows(ctx, "chetter_runners; DROP TABLE x", time.Hour); err == nil {
		t.Fatal("expected error for injected table name, got nil")
	}
}

// TestRetentionTablesAllowlist verifies the allowlist matches the four tables
// named in issue #112 (sessions live in chetter_agent_sessions).
func TestRetentionTablesAllowlist(t *testing.T) {
	want := []string{
		"chetter_task_events",
		"chetter_audit_log",
		"chetter_task_artifacts",
		"chetter_agent_sessions",
	}
	for _, table := range want {
		if !retentionTables[table] {
			t.Errorf("expected %q in retentionTables allowlist", table)
		}
	}
	if len(retentionTables) != len(want) {
		t.Errorf("expected %d retention tables, got %d", len(want), len(retentionTables))
	}
}
