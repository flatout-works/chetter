package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/repository"
)

// Containers are disposable on harness resume; the bind-mounted workspace is
// not. Keep this distinction across retained states, old labels and owners.
func TestRPCPruneContainersRetainsWorkspacesIndependently(t *testing.T) {
	svc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_retention", "x", "runner:latest")
	now := time.Now().UTC()
	path := "/data/task_retention/exec_task_retention/workspace"
	setAttempt := func(status string) {
		t.Helper()
		if _, err := tdb.DB.ExecContext(ctx, sqlQuery(tdb.Dialect(), "UPDATE execution_attempts SET status = ? WHERE id = ?"), status, "exec_task_retention"); err != nil {
			t.Fatal(err)
		}
	}
	verdict := func(container bool, workspace, runner, execution string, wantSafe bool) {
		t.Helper()
		resp, err := svc.PruneWorkspaces(ctx, connect.NewRequest(&runnerv1.PruneWorkspacesRequest{
			RunnerId: runner, ContainerScope: container,
			Candidates: []*runnerv1.WorkspaceCandidate{{TaskId: "task_retention", ExecutionId: execution, WorkspacePath: workspace}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(resp.Msg.SafeToDelete) == 1; got != wantSafe {
			t.Fatalf("container=%v path=%q runner=%s execution=%s safe=%v want=%v", container, workspace, runner, execution, got, wantSafe)
		}
	}
	for _, status := range []string{"running", "resuming", "paused", "recoverable", "paused_waiting_review"} {
		t.Run(status, func(t *testing.T) {
			if _, err := q.PauseAgentSessionByTaskID(ctx, repository.PauseAgentSessionByTaskIDParams{
				Status: status, PinnedRunnerID: nullString("runner_owner"), WorkspacePath: nullString(path),
				PausedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, TaskID: "task_retention",
			}); err != nil {
				t.Fatal(err)
			}
			setAttempt("succeeded")
			verdict(false, path, "runner_owner", "exec_task_retention", false)
			verdict(true, path, "runner_reaper", "exec_task_retention", true)
			verdict(true, "", "runner_reaper", "exec_task_retention", true)
		})
	}
	// A live attempt is protected across owners, even if its session is paused
	// and the container predates workspace labels. A different orphan is safe.
	setAttempt("pending")
	markPendingExecutionAttemptClaimed(t, q, "task_retention", "runner_owner", now, now.Add(time.Minute))
	verdict(true, path, "runner_reaper", "exec_task_retention", false)
	verdict(true, "", "runner_reaper", "exec_task_retention", false)
	verdict(true, "", "runner_reaper", "exec_orphan", true)
	setAttempt("succeeded")
	if err := q.InsertAgentSessionCheckpoint(ctx, repository.InsertAgentSessionCheckpointParams{
		ID: "chk_retention", AgentSessionID: "sess_task_retention", RunnerID: "runner_owner",
		CheckpointPath: "/data/checkpoint", WorkspacePath: path, ContainerName: nullString("chetter-task-task_retention-exec_task_retention"),
		Status: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Ready checkpoints protect both resources, regardless of the requesting
	// runner. Legacy candidates conservatively match ready checkpoints task-wide.
	verdict(false, path, "runner_owner", "exec_task_retention", false)
	verdict(true, path, "runner_reaper", "exec_task_retention", false)
	verdict(true, "", "runner_reaper", "exec_task_retention", false)
	verdict(true, path+"-other", "runner_reaper", "exec_orphan", true)
	if _, err := tdb.DB.ExecContext(ctx, sqlQuery(tdb.Dialect(), "UPDATE agent_session_checkpoints SET status = ? WHERE id = ?"), "expired", "chk_retention"); err != nil {
		t.Fatal(err)
	}
	verdict(true, path, "runner_reaper", "exec_task_retention", true)
	verdict(true, "", "runner_reaper", "exec_task_retention", true)
	// Expiring the checkpoint must not make a retained workspace disposable.
	verdict(false, path, "runner_owner", "exec_task_retention", false)
	// Artifact GC clears the path but deliberately leaves status ready. Such
	// rows must not protect legacy containers or terminal-session workspaces.
	if _, err := tdb.DB.ExecContext(ctx, sqlQuery(tdb.Dialect(), "UPDATE agent_sessions SET status = ?, updated_at = ? WHERE id = ?"), "expired", now.Add(-time.Hour), "sess_task_retention"); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.DB.ExecContext(ctx, sqlQuery(tdb.Dialect(), "UPDATE agent_session_checkpoints SET status = ?, checkpoint_path = ? WHERE id = ?"), "ready", "/data/checkpoint", "chk_retention"); err != nil {
		t.Fatal(err)
	}
	verdict(true, "", "runner_reaper", "exec_task_retention", false)
	verdict(false, path, "runner_owner", "exec_task_retention", false)
	if n, err := q.ClearExpiredSessionCheckpoints(ctx, 60); err != nil || n != 1 {
		t.Fatalf("checkpoint GC: n=%d err=%v", n, err)
	}
	verdict(true, "", "runner_reaper", "exec_task_retention", true)
	verdict(true, path, "runner_reaper", "exec_task_retention", true)
	verdict(false, path, "runner_owner", "exec_task_retention", true)
	// A completely unknown attempt/task is safe in container scope.
	resp, err := svc.PruneWorkspaces(ctx, connect.NewRequest(&runnerv1.PruneWorkspacesRequest{
		RunnerId: "runner_reaper", ContainerScope: true,
		Candidates: []*runnerv1.WorkspaceCandidate{{TaskId: "task_orphan", ExecutionId: "exec_orphan"}},
	}))
	if err != nil || len(resp.Msg.SafeToDelete) != 1 {
		t.Fatalf("orphan verdict: resp=%v err=%v", resp, err)
	}
}
