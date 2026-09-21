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
		n, err := s.PruneOldRows(ctx, "task_events", ttl)
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
	if _, err := s.PruneOldRows(ctx, "tasks", time.Hour); err == nil {
		t.Fatal("expected error for unknown table, got nil")
	}
	if _, err := s.PruneOldRows(ctx, "runners; DROP TABLE x", time.Hour); err == nil {
		t.Fatal("expected error for injected table name, got nil")
	}
}

// TestRetentionTablesAllowlist verifies the allowlist matches the four tables
// named in issue #112 (sessions live in agent_sessions).
func TestRetentionTablesAllowlist(t *testing.T) {
	want := []string{
		"task_events",
		"audit_log",
		"task_artifacts",
		"agent_sessions",
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

// TestPruneTerminalDeliveriesZeroTTLIsDisabled verifies that a zero or negative
// TTL is a no-op that returns before any database access. See issue #253.
func TestPruneTerminalDeliveriesZeroTTLIsDisabled(t *testing.T) {
	s := &Store{} // nil db; the disabled path must return before any DB access
	ctx := context.Background()
	for _, ttl := range []time.Duration{0, -1 * time.Second, -24 * time.Hour} {
		n, err := s.PruneTerminalDeliveries(ctx, "callback_deliveries", ttl)
		if err != nil {
			t.Fatalf("ttl=%v: expected nil error, got %v", ttl, err)
		}
		if n != 0 {
			t.Fatalf("ttl=%v: expected 0 rows pruned, got %d", ttl, n)
		}
	}
}

// TestPruneTerminalDeliveriesRejectsUnknownTable verifies the allowlist guard:
// only the known delivery tables may be pruned, and the guard runs before any
// database access. See issue #253.
func TestPruneTerminalDeliveriesRejectsUnknownTable(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	for _, table := range []string{"tasks", "task_events", "runners; DROP TABLE x"} {
		if _, err := s.PruneTerminalDeliveries(ctx, table, time.Hour); err == nil {
			t.Fatalf("expected error for unknown delivery table %q, got nil", table)
		}
	}
}

// TestDeliveryRetentionTablesAllowlist verifies the delivery allowlist covers
// the three delivery tables and that each only lists terminal statuses, so
// retention can never delete an in-flight or retryable delivery. See issue
// #253.
func TestDeliveryRetentionTablesAllowlist(t *testing.T) {
	want := map[string][]string{
		"callback_deliveries": {"completed", "dead_letter"},
		"inbound_deliveries":  {"succeeded", "failed_permanent", "dead_letter"},
		"webhook_deliveries":  {"completed", "dead_letter"},
	}
	if len(deliveryRetentionTables) != len(want) {
		t.Fatalf("expected %d delivery retention tables, got %d", len(want), len(deliveryRetentionTables))
	}
	// Non-terminal statuses must never appear in any allowlist.
	nonTerminal := map[string]bool{
		"pending": true, "in_flight": true, "processing": true,
		"received": true, "failed": true, "retry_wait": true,
	}
	for table, statuses := range want {
		got, ok := deliveryRetentionTables[table]
		if !ok {
			t.Errorf("expected %q in deliveryRetentionTables allowlist", table)
			continue
		}
		if len(got) != len(statuses) {
			t.Errorf("%s: expected statuses %v, got %v", table, statuses, got)
		}
		for _, status := range got {
			if nonTerminal[status] {
				t.Errorf("%s: non-terminal status %q must not be prunable", table, status)
			}
		}
	}
}
