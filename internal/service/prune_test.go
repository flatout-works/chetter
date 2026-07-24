package service

import (
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/config"
)

// TestPruneJobsDisabledByDefault verifies that with all retention TTLs at their
// zero default no prune jobs are produced, so the reaper prunes nothing. This
// keeps existing deployments unaffected until an operator opts in. See issue
// #112 criteria 6 and 7.
func TestPruneJobsDisabledByDefault(t *testing.T) {
	s := &Service{cfg: config.Config{}}
	if jobs := s.pruneJobs(); len(jobs) != 0 {
		t.Fatalf("expected no prune jobs by default, got %d: %v", len(jobs), jobs)
	}
}

// TestPruneJobsFromConfig verifies that enabled TTLs produce the expected tables
// and durations. The artifact TTL governs both task artifacts and agent sessions
// per issue #112 criterion 3.
func TestPruneJobsFromConfig(t *testing.T) {
	s := &Service{cfg: config.Config{
		EventsRetentionDays:   30,
		AuditRetentionDays:    90,
		ArtifactRetentionDays: 180,
	}}
	jobs := s.pruneJobs()
	if len(jobs) != 4 {
		t.Fatalf("expected 4 prune jobs, got %d", len(jobs))
	}
	want := []pruneJob{
		{"chetter_task_events", 30 * 24 * time.Hour},
		{"chetter_audit_log", 90 * 24 * time.Hour},
		{"chetter_task_artifacts", 180 * 24 * time.Hour},
		{"chetter_agent_sessions", 180 * 24 * time.Hour},
	}
	for i, w := range want {
		if jobs[i].table != w.table || jobs[i].ttl != w.ttl {
			t.Errorf("job %d: want table=%q ttl=%v, got table=%q ttl=%v", i, w.table, w.ttl, jobs[i].table, jobs[i].ttl)
		}
	}
}

// TestPruneJobsPartialConfig verifies that enabling only some TTLs prunes only
// the corresponding tables (opt-in is per table). See issue #112 criterion 6.
func TestPruneJobsPartialConfig(t *testing.T) {
	s := &Service{cfg: config.Config{AuditRetentionDays: 90}}
	jobs := s.pruneJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 prune job, got %d", len(jobs))
	}
	if jobs[0].table != "chetter_audit_log" || jobs[0].ttl != 90*24*time.Hour {
		t.Errorf("unexpected job: %+v", jobs[0])
	}
}

// TestPruneRetainedRowsNoopWhenDisabled verifies that the reaper step is a safe
// no-op when pruning is disabled: with an empty prune job list it returns before
// touching the store (here nil), so existing deployments are unaffected. See
// issue #112 criterion 7.
func TestPruneRetainedRowsNoopWhenDisabled(t *testing.T) {
	s := &Service{cfg: config.Config{}} // nil store; must not be reached
	s.pruneRetainedRows()               // must not panic
}
