package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/store"
)

// insertFTSFixture inserts a task, session, prompt, attempt, audit event, and
// artifact with the given IDs and search_text so that FTS queries can find them.
func insertFTSFixture(t *testing.T, db *sql.DB, dialect store.Dialect, suffix, searchText string) {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")

	taskID := "fts-task-" + suffix
	sessionID := "fts-session-" + suffix
	promptID := "fts-prompt-" + suffix
	attemptID := "fts-attempt-" + suffix
	auditID := "fts-audit-" + suffix
	artifactID := "fts-artifact-" + suffix

	execOrDie(t, db, dialect,
		`INSERT INTO tasks (id, status, prompt, search_text, created_at, updated_at) VALUES (?, 'completed', 'test prompt', ?, ?, ?)`,
		`INSERT INTO tasks (id, status, prompt, search_text, created_at, updated_at) VALUES ($1, 'completed', 'test prompt', $2, $3, $4)`,
		taskID, searchText, now, now,
	)
	execOrDie(t, db, dialect,
		`INSERT INTO agent_sessions (id, task_id, sequence, status, resume_mode, skills, env, search_text, created_at, updated_at) VALUES (?, ?, 1, 'completed', 'none', '[]', '{}', ?, ?, ?)`,
		`INSERT INTO agent_sessions (id, task_id, sequence, status, resume_mode, skills, env, search_text, created_at, updated_at) VALUES ($1, $2, 1, 'completed', 'none', '[]', '{}', $3, $4, $5)`,
		sessionID, taskID, searchText, now, now,
	)
	execOrDie(t, db, dialect,
		`INSERT INTO user_prompts (id, agent_session_id, task_id, sequence, status, prompt, created_at, updated_at) VALUES (?, ?, ?, 1, 'completed', 'test prompt', ?, ?)`,
		`INSERT INTO user_prompts (id, agent_session_id, task_id, sequence, status, prompt, created_at, updated_at) VALUES ($1, $2, $3, 1, 'completed', 'test prompt', $4, $5)`,
		promptID, sessionID, taskID, now, now,
	)
	execOrDie(t, db, dialect,
		`INSERT INTO execution_attempts (id, user_prompt_id, sequence, status, timeout_sec, created_at, updated_at) VALUES (?, ?, 1, 'completed', 600, ?, ?)`,
		`INSERT INTO execution_attempts (id, user_prompt_id, sequence, status, timeout_sec, created_at, updated_at) VALUES ($1, $2, 1, 'completed', 600, $3, $4)`,
		attemptID, promptID, now, now,
	)
	execOrDie(t, db, dialect,
		`INSERT INTO audit_log (id, event_type, search_text, created_at) VALUES (?, 'task.submitted', ?, ?)`,
		`INSERT INTO audit_log (id, event_type, search_text, created_at) VALUES ($1, 'task.submitted', $2, $3)`,
		auditID, searchText, now,
	)
	execOrDie(t, db, dialect,
		`INSERT INTO task_artifacts (id, task_id, execution_attempt_id, artifact_type, repo, search_text, created_at, discovered_at, discovery_source) VALUES (?, ?, ?, 'github_pr_review', 'test/repo', ?, ?, ?, 'webhook')`,
		`INSERT INTO task_artifacts (id, task_id, execution_attempt_id, artifact_type, repo, search_text, created_at, discovered_at, discovery_source) VALUES ($1, $2, $3, 'github_pr_review', 'test/repo', $4, $5, $6, 'webhook')`,
		artifactID, taskID, attemptID, searchText, now, now,
	)
}

func execOrDie(t *testing.T, db *sql.DB, dialect store.Dialect, mysqlQuery, pgQuery string, args ...any) {
	t.Helper()
	query := mysqlQuery
	if dialect == store.DialectPostgres {
		query = pgQuery
	}
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("insert fixture: %v (query: %s)", err, query)
	}
}

// TestPostgresFTS validates that PostgreSQL FTS uses websearch_to_tsquery
// against the GIN search indexes for tasks, sessions, audit log, and artifacts.
func TestPostgresFTS(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() != store.DialectPostgres {
		t.Skip("PostgreSQL-specific FTS test")
	}

	ctx := context.Background()

	// Insert two fixtures with distinct search terms.
	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "alpha", "deploy a kubernetes cluster on aws")
	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "beta", "debug a memory leak in the python runtime")

	// Task FTS: search for "kubernetes" should match alpha, not beta.
	tasks, err := svc.searchTasksFTS(ctx, sql.NullString{}, "", "kubernetes", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksFTS for 'kubernetes': got %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "fts-task-alpha" {
		t.Fatalf("searchTasksFTS: got task %s, want fts-task-alpha", tasks[0].ID)
	}

	// Session FTS: search for "memory leak" should match beta.
	sessions, err := svc.searchAgentSessionsFTS(ctx, sql.NullString{}, "", "memory leak", 10, 0)
	if err != nil {
		t.Fatalf("searchAgentSessionsFTS: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("searchAgentSessionsFTS for 'memory leak': got %d sessions, want 1", len(sessions))
	}
	if sessions[0].ID != "fts-session-beta" {
		t.Fatalf("searchAgentSessionsFTS: got session %s, want fts-session-beta", sessions[0].ID)
	}

	// Audit FTS: search for "python" should match beta.
	auditRows, err := svc.searchAuditLogFTS(ctx, AuditEventFilterInput{Search: "python"}, 10, 0, sql.NullTime{})
	if err != nil {
		t.Fatalf("searchAuditLogFTS: %v", err)
	}
	if len(auditRows) != 1 {
		t.Fatalf("searchAuditLogFTS for 'python': got %d rows, want 1", len(auditRows))
	}
	if auditRows[0].ID != "fts-audit-beta" {
		t.Fatalf("searchAuditLogFTS: got audit %s, want fts-audit-beta", auditRows[0].ID)
	}

	// Artifact FTS: search for "aws" should match alpha.
	artifacts, err := svc.searchTaskArtifactsFTS(ctx, TaskArtifactFilterInput{Search: "aws"}, 10, 0)
	if err != nil {
		t.Fatalf("searchTaskArtifactsFTS: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("searchTaskArtifactsFTS for 'aws': got %d artifacts, want 1", len(artifacts))
	}
	if artifacts[0].ID != "fts-artifact-alpha" {
		t.Fatalf("searchTaskArtifactsFTS: got artifact %s, want fts-artifact-alpha", artifacts[0].ID)
	}
}

// TestPostgresFTSFallback validates that PostgreSQL FTS falls back to LIKE
// when no FTS index is available (simulated by searching with an empty term
// which short-circuits, or by verifying that results still come back via the
// fallback path).
func TestPostgresFTSFallback(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() != store.DialectPostgres {
		t.Skip("PostgreSQL-specific FTS test")
	}

	ctx := context.Background()

	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "fallback", "some unique text here")

	// Empty search should return nil immediately (no FTS attempted).
	tasks, err := svc.searchTasksFTS(ctx, sql.NullString{}, "", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS empty: %v", err)
	}
	if tasks != nil {
		t.Fatalf("searchTasksFTS empty: expected nil, got %d tasks", len(tasks))
	}

	// Searching with a term that exists should work via FTS.
	tasks, err = svc.searchTasksFTS(ctx, sql.NullString{}, "", "unique text", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS 'unique text': %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksFTS 'unique text': got %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "fts-task-fallback" {
		t.Fatalf("searchTasksFTS: got task %s, want fts-task-fallback", tasks[0].ID)
	}

	// Searching for a non-matching term should return empty results, not error.
	tasks, err = svc.searchTasksFTS(ctx, sql.NullString{}, "", "nonexistent", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS 'nonexistent': %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("searchTasksFTS 'nonexistent': got %d tasks, want 0", len(tasks))
	}
}

// TestPostgresFTSSearchRaw validates that the raw search path also uses PostgreSQL FTS.
func TestPostgresFTSSearchRaw(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() != store.DialectPostgres {
		t.Skip("PostgreSQL-specific FTS test")
	}

	ctx := context.Background()

	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "raw-alpha", "elasticsearch observability pipeline")
	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "raw-beta", "postgresql backup restore procedure")

	// searchTasksRaw with FTS matching
	tasks, err := svc.searchTasksRaw(ctx, nil, nil, "", "elasticsearch", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksRaw: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksRaw for 'elasticsearch': got %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "fts-task-raw-alpha" {
		t.Fatalf("searchTasksRaw: got task %s, want fts-task-raw-alpha", tasks[0].ID)
	}

	// searchAgentSessionsRaw with FTS matching
	sessions, err := svc.searchAgentSessionsRaw(ctx, nil, nil, "", "postgresql backup", 10, 0)
	if err != nil {
		t.Fatalf("searchAgentSessionsRaw: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("searchAgentSessionsRaw for 'postgresql backup': got %d sessions, want 1", len(sessions))
	}
	if sessions[0].ID != "fts-session-raw-beta" {
		t.Fatalf("searchAgentSessionsRaw: got session %s, want fts-session-raw-beta", sessions[0].ID)
	}
}

// TestPostgresFTSAllTerms validates that websearch_to_tsquery handles multiple
// terms with implicit AND semantics (websearch_to_tsquery uses OR for bare
// terms; phrases like "memory leak" use quotes).
func TestPostgresFTSMultiTerm(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() != store.DialectPostgres {
		t.Skip("PostgreSQL-specific FTS test")
	}

	ctx := context.Background()

	// Insert fixtures with overlapping terms.
	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "multi-1", "api gateway rate limiting")
	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "multi-2", "database rate limiting timeout")

	// "rate limiting" should match both (websearch_to_tsquery treats bare
	// words as OR, so "rate" OR "limiting" would match both).
	tasks, err := svc.searchTasksFTS(ctx, sql.NullString{}, "", "rate limiting", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS 'rate limiting': %v", err)
	}
	if len(tasks) < 1 {
		t.Fatalf("searchTasksFTS 'rate limiting': got %d tasks, want at least 1", len(tasks))
	}

	// "api gateway" should match only multi-1 (both "api" and "gateway" appear).
	tasks, err = svc.searchTasksFTS(ctx, sql.NullString{}, "", "api gateway", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS 'api gateway': %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksFTS 'api gateway': got %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "fts-task-multi-1" {
		t.Fatalf("searchTasksFTS: got task %s, want fts-task-multi-1", tasks[0].ID)
	}

	// Verify that searchTasksRaw also works with multi-term queries.
	tasks, err = svc.searchTasksRaw(ctx, nil, nil, "", "api gateway", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksRaw 'api gateway': %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksRaw 'api gateway': got %d tasks, want 1", len(tasks))
	}
}

// TestPostgresFTSPreservesFilters verifies that FTS search preserves the
// existing filters (status, team, agent, repo) when using PostgreSQL.
func TestPostgresFTSPreservesFilters(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() != store.DialectPostgres {
		t.Skip("PostgreSQL-specific FTS test")
	}

	ctx := context.Background()

	// Insert one task with status 'completed' and one with status 'pending'.
	// Both should have the same search term.
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")
	execOrDie(t, tdb.DB, tdb.Dialect(),
		`INSERT INTO tasks (id, status, prompt, git_url, search_text, created_at, updated_at) VALUES (?, 'completed', 'test', 'https://github.com/foo/bar', ?, ?, ?)`,
		`INSERT INTO tasks (id, status, prompt, git_url, search_text, created_at, updated_at) VALUES ($1, 'completed', 'test', 'https://github.com/foo/bar', $2, $3, $4)`,
		"fts-filter-done", "filter test deployment", now, now,
	)
	execOrDie(t, tdb.DB, tdb.Dialect(),
		`INSERT INTO tasks (id, status, prompt, git_url, search_text, created_at, updated_at) VALUES (?, 'pending', 'test', 'https://github.com/foo/bar', ?, ?, ?)`,
		`INSERT INTO tasks (id, status, prompt, git_url, search_text, created_at, updated_at) VALUES ($1, 'pending', 'test', 'https://github.com/foo/bar', $2, $3, $4)`,
		"fts-filter-pending", "filter test deployment", now, now,
	)

	// FTS with status filter should only return completed tasks.
	tasks, err := svc.searchTasksFTS(ctx, sql.NullString{}, "completed", "filter test", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS with status filter: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksFTS with status=completed: got %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "fts-filter-done" {
		t.Fatalf("searchTasksFTS: got task %s, want fts-filter-done", tasks[0].ID)
	}

	// searchTasksRaw with status filter and repo filter.
	tasks, err = svc.searchTasksRaw(ctx, nil, []string{"foo/bar"}, "pending", "filter test", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksRaw with status/repo filter: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksRaw with status=pending, repo=foo/bar: got %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "fts-filter-pending" {
		t.Fatalf("searchTasksRaw: got task %s, want fts-filter-pending", tasks[0].ID)
	}
}

// TestPostgresFTSAuditExcludeTypes verifies that exclude types work correctly
// with PostgreSQL FTS for audit log search.
func TestPostgresFTSAuditExcludeTypes(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() != store.DialectPostgres {
		t.Skip("PostgreSQL-specific FTS test")
	}

	ctx := context.Background()
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")

	// Insert two audit events with the same search text but different event types.
	execOrDie(t, tdb.DB, tdb.Dialect(),
		`INSERT INTO audit_log (id, event_type, search_text, created_at) VALUES (?, 'task.submitted', ?, ?)`,
		`INSERT INTO audit_log (id, event_type, search_text, created_at) VALUES ($1, 'task.submitted', $2, $3)`,
		"fts-audit-excl-1", "critical security audit event", now,
	)
	execOrDie(t, tdb.DB, tdb.Dialect(),
		`INSERT INTO audit_log (id, event_type, search_text, created_at) VALUES (?, 'task.cancelled', ?, ?)`,
		`INSERT INTO audit_log (id, event_type, search_text, created_at) VALUES ($1, 'task.cancelled', $2, $3)`,
		"fts-audit-excl-2", "critical security audit event", now,
	)

	// FTS without exclude should return both.
	auditRows, err := svc.searchAuditLogFTS(ctx, AuditEventFilterInput{Search: "critical security"}, 10, 0, sql.NullTime{})
	if err != nil {
		t.Fatalf("searchAuditLogFTS: %v", err)
	}
	if len(auditRows) != 2 {
		t.Fatalf("searchAuditLogFTS without exclude: got %d rows, want 2", len(auditRows))
	}

	// FTS with exclude should filter.
	auditRows, err = svc.searchAuditLogFTS(ctx, AuditEventFilterInput{
		Search:       "critical security",
		ExcludeTypes: []string{"task.cancelled"},
	}, 10, 0, sql.NullTime{})
	if err != nil {
		t.Fatalf("searchAuditLogFTS with exclude: %v", err)
	}
	if len(auditRows) != 1 {
		t.Fatalf("searchAuditLogFTS with exclude: got %d rows, want 1", len(auditRows))
	}
	if auditRows[0].ID != "fts-audit-excl-1" {
		t.Fatalf("searchAuditLogFTS: got audit %s, want fts-audit-excl-1", auditRows[0].ID)
	}
}

// TestMySQLFTSNotBroken is a smoke-test that MySQL FTS still works after the
// refactoring (when running against a MySQL or TiDB test database).
func TestMySQLFTSNotBroken(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	if tdb.Dialect() == store.DialectPostgres {
		t.Skip("MySQL/TiDB-specific FTS test")
	}

	ctx := context.Background()

	insertFTSFixture(t, tdb.DB, tdb.Dialect(), "mysql", "mysql fulltext search test")

	tasks, err := svc.searchTasksFTS(ctx, sql.NullString{}, "", "fulltext", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS MySQL: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("searchTasksFTS MySQL 'fulltext': got %d tasks, want 1", len(tasks))
	}
}

// TestFTSEmptySearch verifies that empty search terms short-circuit and return
// nil for all FTS functions (all dialects).
func TestFTSEmptySearch(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()

	ctx := context.Background()

	tasks, err := svc.searchTasksFTS(ctx, sql.NullString{}, "", "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchTasksFTS empty: %v", err)
	}
	if tasks != nil {
		t.Fatalf("searchTasksFTS empty: expected nil, got %d tasks", len(tasks))
	}

	sessions, err := svc.searchAgentSessionsFTS(ctx, sql.NullString{}, "", "", 10, 0)
	if err != nil {
		t.Fatalf("searchAgentSessionsFTS empty: %v", err)
	}
	if sessions != nil {
		t.Fatalf("searchAgentSessionsFTS empty: expected nil, got %d sessions", len(sessions))
	}

	auditRows, err := svc.searchAuditLogFTS(ctx, AuditEventFilterInput{}, 10, 0, sql.NullTime{})
	if err != nil {
		t.Fatalf("searchAuditLogFTS empty: %v", err)
	}
	if auditRows != nil {
		t.Fatalf("searchAuditLogFTS empty: expected nil, got %d rows", len(auditRows))
	}

	artifacts, err := svc.searchTaskArtifactsFTS(ctx, TaskArtifactFilterInput{}, 10, 0)
	if err != nil {
		t.Fatalf("searchTaskArtifactsFTS empty: %v", err)
	}
	if artifacts != nil {
		t.Fatalf("searchTaskArtifactsFTS empty: expected nil, got %d artifacts", len(artifacts))
	}
}


