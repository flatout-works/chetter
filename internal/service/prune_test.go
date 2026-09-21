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
// per issue #112 criterion 3. See issue #253 for the delivery jobs.
func TestPruneJobsFromConfig(t *testing.T) {
	s := &Service{cfg: config.Config{
		EventsRetentionDays:   30,
		AuditRetentionDays:    90,
		ArtifactRetentionDays: 180,
		DeliveryRetentionDays: 60,
	}}
	jobs := s.pruneJobs()
	if len(jobs) != 7 {
		t.Fatalf("expected 7 prune jobs, got %d", len(jobs))
	}
	want := []pruneJob{
		{table: "task_events", ttl: 30 * 24 * time.Hour},
		{table: "audit_log", ttl: 90 * 24 * time.Hour},
		{table: "task_artifacts", ttl: 180 * 24 * time.Hour},
		{table: "agent_sessions", ttl: 180 * 24 * time.Hour},
		{table: "callback_deliveries", ttl: 60 * 24 * time.Hour, terminalOnly: true},
		{table: "inbound_deliveries", ttl: 60 * 24 * time.Hour, terminalOnly: true},
		{table: "webhook_deliveries", ttl: 60 * 24 * time.Hour, terminalOnly: true},
	}
	for i, w := range want {
		if jobs[i] != w {
			t.Errorf("job %d: want %+v, got %+v", i, w, jobs[i])
		}
	}
}

// TestPruneJobsDeliveryOnly verifies that enabling only the delivery TTL
// produces exactly the three delivery tables, each terminal-only. See issue
// #253.
func TestPruneJobsDeliveryOnly(t *testing.T) {
	s := &Service{cfg: config.Config{DeliveryRetentionDays: 14}}
	jobs := s.pruneJobs()
	if len(jobs) != 3 {
		t.Fatalf("expected 3 prune jobs, got %d", len(jobs))
	}
	for i, job := range jobs {
		if !job.terminalOnly {
			t.Errorf("job %d (%s): expected terminalOnly=true", i, job.table)
		}
		if job.ttl != 14*24*time.Hour {
			t.Errorf("job %d (%s): expected ttl 14d, got %v", i, job.table, job.ttl)
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
	if jobs[0].table != "audit_log" || jobs[0].ttl != 90*24*time.Hour {
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
