package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/testdb"
)

// pendingAdmissionService returns a test service with the global pending-task
// admission limit configured (issue #50). The limit is applied on the live
// Service (same package), matching how operators configure
// CHETTER_MAX_PENDING_TASKS.
func pendingAdmissionService(t *testing.T, maxPending int) (*Service, *testdb.TestDB, func()) {
	t.Helper()
	svc, tdb, cleanup := newServiceForTest(t)
	svc.cfg.MaxPendingTasks = maxPending
	return svc, tdb, cleanup
}

func TestPendingTaskAdmissionRejectsAtLimit(t *testing.T) {
	svc, tdb, cleanup := pendingAdmissionService(t, 2)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: fmt.Sprintf("admitted %d", i), AgentImage: "runner:latest"}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	_, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "overflow", AgentImage: "runner:latest"})
	var capErr *PendingTaskCapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected PendingTaskCapacityError, got %v", err)
	}
	if capErr.Limit != 2 || capErr.Current != 2 {
		t.Fatalf("capacity error = %+v, want limit 2 current 2", capErr)
	}

	// The rejected task must not be stored: pending count stays at the limit.
	n, err := svc.repo.CountPendingTasks(ctx)
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if n != 2 {
		t.Fatalf("pending tasks = %d, want 2", n)
	}

	// The rejection is recorded in the audit log (auditAsync is synchronous).
	var auditCount int
	err = tdb.DB.QueryRow(testQuery(tdb.Dialect(),
		`SELECT COUNT(*) FROM audit_log WHERE event_type = 'task_admission_rejected'`,
		`SELECT COUNT(*) FROM audit_log WHERE event_type = 'task_admission_rejected'`,
	)).Scan(&auditCount)
	if err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("task_admission_rejected audit events = %d, want 1", auditCount)
	}
}

func TestPendingTaskAdmissionConcurrentIsStrict(t *testing.T) {
	svc, _, cleanup := pendingAdmissionService(t, 5)
	defer cleanup()
	ctx := context.Background()

	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: fmt.Sprintf("concurrent %d", i), AgentImage: "runner:latest"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	var admitted, rejected int
	var capErr *PendingTaskCapacityError
	for err := range errs {
		switch {
		case err == nil:
			admitted++
		case errors.As(err, &capErr):
			rejected++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if admitted != 5 {
		t.Fatalf("admitted = %d, want exactly 5", admitted)
	}
	if rejected != workers-5 {
		t.Fatalf("rejected = %d, want %d", rejected, workers-5)
	}
	n, err := svc.repo.CountPendingTasks(ctx)
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if n != 5 {
		t.Fatalf("final pending tasks = %d, want 5 (no overshoot)", n)
	}
}

func TestPendingTaskAdmissionReleasedWhenTaskLeavesPending(t *testing.T) {
	svc, _, cleanup := pendingAdmissionService(t, 1)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "first", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit first: %v", err)
	}
	if _, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "second", AgentImage: "runner:latest"}); err == nil {
		t.Fatal("expected capacity error while queue is full")
	}

	// Cancelling the pending task frees the slot; the same submission succeeds.
	if _, err := svc.CancelTask(ctx, rec.ID, "test cancel"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "retried", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit after capacity freed: %v", err)
	}
}

func TestPendingTaskAdmissionDisabledByDefault(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t) // MaxPendingTasks stays 0
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if _, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: fmt.Sprintf("unlimited %d", i), AgentImage: "runner:latest"}); err != nil {
			t.Fatalf("submit %d with limit disabled: %v", i, err)
		}
	}
}

func TestPendingTaskAdmissionGatesRerunTask(t *testing.T) {
	svc, tdb, cleanup := pendingAdmissionService(t, 1)
	defer cleanup()
	ctx := context.Background()

	orig, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "rerun source", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source: %v", err)
	}
	// Terminate the source task so RerunTask accepts it, freeing its slot.
	markTaskTerminal(t, tdb, orig.ID, "error")

	if _, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "fills queue", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit filler: %v", err)
	}

	_, err = svc.RerunTask(ctx, orig.ID)
	var rerunCapErr *PendingTaskCapacityError
	if !errors.As(err, &rerunCapErr) {
		t.Fatalf("RerunTask: expected PendingTaskCapacityError, got %v", err)
	}
}

func TestPendingTaskAdmissionGatesRecoverTask(t *testing.T) {
	svc, tdb, cleanup := pendingAdmissionService(t, 1)
	defer cleanup()
	ctx := context.Background()

	orig, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "recover source", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source: %v", err)
	}
	// RecoverTask requires a session export and a terminal task.
	markTaskTerminal(t, tdb, orig.ID, "error")
	setSessionExport(t, tdb, orig.ID)

	if _, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "fills queue", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit filler: %v", err)
	}

	_, err = svc.RecoverTask(ctx, orig.ID, "")
	var recoverCapErr *PendingTaskCapacityError
	if !errors.As(err, &recoverCapErr) {
		t.Fatalf("RecoverTask: expected PendingTaskCapacityError, got %v", err)
	}
}

func TestPendingTaskAdmissionGatesResumeAgentSession(t *testing.T) {
	svc, tdb, cleanup := pendingAdmissionService(t, 1)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "resumable source", AgentImage: "runner:latest", SessionMode: "resumable"})
	if err != nil {
		t.Fatalf("submit source: %v", err)
	}
	session, err := svc.repo.GetAgentSessionByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	// Set up the paused session the way a completed harness run does:
	// terminal task + paused session pinned to a live runner with a workspace.
	registerIsolationCapableRunner(t, data.New(tdb.DB, tdb.Dialect()), "runner_gvisor")
	markTaskTerminal(t, tdb, rec.ID, "done")
	pauseSession(t, tdb, session.ID, "runner_gvisor")

	if _, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "fills queue", AgentImage: "runner:latest"}); err != nil {
		t.Fatalf("submit filler: %v", err)
	}

	_, err = svc.ResumeAgentSession(ctx, session.ID, "continue", 0)
	var resumeCapErr *PendingTaskCapacityError
	if !errors.As(err, &resumeCapErr) {
		t.Fatalf("ResumeAgentSession: expected PendingTaskCapacityError, got %v", err)
	}
}

// --- helpers ---------------------------------------------------------------

func markTaskTerminal(t *testing.T, tdb *testdb.TestDB, taskID, status string) {
	t.Helper()
	now := time.Now().UTC()
	_, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		`UPDATE tasks SET status = ?, ended_at = ?, updated_at = ? WHERE id = ?`,
		`UPDATE tasks SET status = $1, ended_at = $2, updated_at = $3 WHERE id = $4`,
	), status, now, now, taskID)
	if err != nil {
		t.Fatalf("mark task %s terminal: %v", taskID, err)
	}
}

func setSessionExport(t *testing.T, tdb *testdb.TestDB, taskID string) {
	t.Helper()
	_, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		`UPDATE user_prompts SET session_export = ? WHERE task_id = ?`,
		`UPDATE user_prompts SET session_export = $1 WHERE task_id = $2`,
	), "export: transcript", taskID)
	if err != nil {
		t.Fatalf("set session export: %v", err)
	}
}

func pauseSession(t *testing.T, tdb *testdb.TestDB, sessionID, runnerID string) {
	t.Helper()
	now := time.Now().UTC()
	_, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		`UPDATE agent_sessions SET status = 'paused', pinned_runner_id = ?, workspace_path = '/workspace/pinned', harness_session_id = 'harness-1', paused_at = ?, updated_at = ? WHERE id = ?`,
		`UPDATE agent_sessions SET status = 'paused', pinned_runner_id = $1, workspace_path = '/workspace/pinned', harness_session_id = 'harness-1', paused_at = $2, updated_at = $3 WHERE id = $4`,
	), runnerID, now, now, sessionID)
	if err != nil {
		t.Fatalf("pause session %s: %v", sessionID, err)
	}
}
