package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/testdb"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

func newRPCTestService(t *testing.T) (*RunnerRPCService, data.Repository, *testdb.TestDB, func()) {
	t.Helper()
	if svcTestDB == nil {
		t.Skip("database unavailable; skipping integration test")
	}
	tdb, cleanup := svcTestDB.NewTestDB(t)
	tdb.Truncate(t)
	q := data.New(tdb.DB, tdb.Dialect())
	return NewRunnerRPCService(q, tdb.DB, tdb.Dialect()), q, tdb, cleanup
}

func insertPendingTask(t *testing.T, q data.Repository, id, prompt, agentImage string) {
	t.Helper()
	now := time.Now().UTC()
	if err := q.InsertTask(context.Background(), repository.InsertTaskParams{
		ID:                id,
		Prompt:            prompt,
		AgentImage:        sql.NullString{String: agentImage, Valid: true},
		Skills:            json.RawMessage(`[]`),
		Env:               json.RawMessage(`{}`),
		CommitAuthorName:  sql.NullString{String: "Chetter", Valid: true},
		CommitAuthorEmail: sql.NullString{String: "chetter@chetter.flatout.works", Valid: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	sessionID := "sess_" + id
	promptID := "prompt_" + id
	if err := q.InsertAgentSession(context.Background(), repository.InsertAgentSessionParams{
		ID: sessionID, TaskID: id, Sequence: 1, Status: "running", ResumeMode: "none", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if err := q.InsertUserPrompt(context.Background(), repository.InsertUserPromptParams{
		ID: promptID, AgentSessionID: sessionID, TaskID: id, Sequence: 1, Status: "pending", Prompt: prompt, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert prompt: %v", err)
	}
	if err := q.InsertPendingExecutionAttempt(context.Background(), repository.InsertPendingExecutionAttemptParams{
		ID: "exec_" + id, UserPromptID: promptID, Sequence: 1, TimeoutSec: 600, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert pending attempt: %v", err)
	}
}

func markPendingExecutionAttemptClaimed(t *testing.T, q data.Repository, taskID, runnerID string, claimedAt, leaseExpiresAt time.Time) {
	t.Helper()
	if rows, err := q.MarkExecutionAttemptClaimed(context.Background(), repository.MarkExecutionAttemptClaimedParams{
		RunnerID:       nullString(runnerID),
		ClaimedAt:      sql.NullTime{Time: claimedAt, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: leaseExpiresAt, Valid: true},
		StartedAt:      sql.NullTime{Time: claimedAt, Valid: true},
		UpdatedAt:      claimedAt,
		ID:             "exec_" + taskID,
	}); err != nil {
		t.Fatalf("claim execution attempt for %s: %v", taskID, err)
	} else if rows != 1 {
		t.Fatalf("claim execution attempt for %s rows: %d", taskID, rows)
	}
}

func markTaskRunning(t *testing.T, q data.Repository, taskID string, updatedAt time.Time) {
	t.Helper()
	if rows, err := q.MarkTaskRunning(context.Background(), repository.MarkTaskRunningParams{UpdatedAt: updatedAt, ID: taskID}); err != nil {
		t.Fatalf("mark task %s running: %v", taskID, err)
	} else if rows != 1 {
		t.Fatalf("mark task %s running rows: %d", taskID, rows)
	}
}

func TestRPCClaimTaskMarksPendingTaskRunning(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_1", "do work", "runner:latest")

	resp, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId:     "runner_1",
		WaitSeconds:  0,
		LeaseSeconds: 60,
	}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if resp.Msg.Task == nil {
		t.Fatal("expected claimed task")
	}
	if resp.Msg.Task.TaskId != "task_1" {
		t.Fatalf("task id mismatch: %s", resp.Msg.Task.TaskId)
	}
	if resp.Msg.Task.Attempt != 1 {
		t.Fatalf("attempt should be incremented, got %d", resp.Msg.Task.Attempt)
	}
	if resp.Msg.Task.Prompt != "do work" {
		t.Fatalf("prompt mismatch: %s", resp.Msg.Task.Prompt)
	}

	// Verify DB state
	row, err := q.GetTaskByID(ctx, "task_1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if row.Status != "running" {
		t.Errorf("expected status=running, got %s", row.Status)
	}
	attempt, err := q.GetExecutionAttemptByID(ctx, resp.Msg.Task.ExecutionId)
	if err != nil {
		t.Fatalf("get execution attempt: %v", err)
	}
	if !attempt.RunnerID.Valid || attempt.RunnerID.String != "runner_1" {
		t.Errorf("expected runner_id=runner_1, got %v", attempt.RunnerID)
	}
	if !attempt.LeaseExpiresAt.Valid {
		t.Error("expected lease_expires_at set")
	}
	if !attempt.ClaimedAt.Valid {
		t.Error("expected claimed_at set")
	}
}

func TestRPCRejectsStaleExecutionEventsAfterReclaim(t *testing.T) {
	svc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_fenced", "do work", "runner:latest")

	first, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_1", WaitSeconds: 0, LeaseSeconds: 60,
	}))
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	firstExecution := first.Msg.Task.ExecutionId
	if firstExecution == "" {
		t.Fatal("first claim did not receive an execution ID")
	}

	if _, err := tdb.DB.ExecContext(ctx, testQuery(tdb.Dialect(),
		"UPDATE chetter_execution_attempts SET lease_expires_at = ? WHERE id = ?",
		"UPDATE chetter_execution_attempts SET lease_expires_at = $1 WHERE id = $2"),
		time.Now().UTC().Add(-time.Second), firstExecution); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if _, err := q.MarkExecutionAttemptLost(ctx, repository.MarkExecutionAttemptLostParams{
		Error: nullString("lease expired"), EndedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
		UpdatedAt: time.Now().UTC(), ID: firstExecution,
	}); err != nil {
		t.Fatalf("mark first attempt lost: %v", err)
	}
	if _, err := q.RequeueTaskAfterExecutionAttemptLost(ctx, repository.RequeueTaskAfterExecutionAttemptLostParams{
		UpdatedAt: time.Now().UTC(), TaskID: "task_fenced",
	}); err != nil {
		t.Fatalf("requeue task aggregate: %v", err)
	}
	firstAttempt, err := q.GetExecutionAttemptByID(ctx, firstExecution)
	if err != nil {
		t.Fatalf("get first attempt: %v", err)
	}
	if err := q.InsertPendingExecutionAttempt(ctx, repository.InsertPendingExecutionAttemptParams{
		ID: "exec_reclaimed", UserPromptID: firstAttempt.UserPromptID, Sequence: 2,
		TimeoutSec: 600, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("queue replacement attempt: %v", err)
	}

	second, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId: "runner_2", WaitSeconds: 0, LeaseSeconds: 60,
	}))
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	secondExecution := second.Msg.Task.ExecutionId
	if secondExecution == "" || secondExecution == firstExecution {
		t.Fatalf("second execution ID = %q, first = %q", secondExecution, firstExecution)
	}

	_, err = svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:      "task_fenced",
			ExecutionId: firstExecution,
			Status:      "done",
		}},
	}))
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stale report error = %v, want fenced execution failure", err)
	}

	if _, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_2",
		Events: []*runnerv1.TaskEvent{{
			TaskId:      "task_fenced",
			ExecutionId: secondExecution,
			Status:      "done",
		}},
	})); err != nil {
		t.Fatalf("current report: %v", err)
	}
}

func TestRPCClaimTaskNoPendingReturnsEmpty(t *testing.T) {
	svc, _, _, cleanup := newRPCTestService(t)
	defer cleanup()
	resp, err := svc.ClaimTask(context.Background(), connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId:    "runner_1",
		WaitSeconds: 1,
	}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if resp.Msg.Task != nil && resp.Msg.Task.TaskId != "" {
		t.Fatalf("expected empty task, got %+v", resp.Msg.Task)
	}
}

func TestRPCClaimTaskHonorsRequiredRunnerID(t *testing.T) {
	svc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_pinned", "resume work", "runner:latest")
	if _, err := tdb.DB.ExecContext(ctx, testQuery(tdb.Dialect(), "UPDATE chetter_execution_attempts SET required_runner_id = ? WHERE id = ?", "UPDATE chetter_execution_attempts SET required_runner_id = $1 WHERE id = $2"), "runner_pinned", "exec_task_pinned"); err != nil {
		t.Fatalf("pin attempt: %v", err)
	}

	resp, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId:    "runner_other",
		WaitSeconds: 1,
	}))
	if err != nil {
		t.Fatalf("ClaimTask other runner: %v", err)
	}
	if resp.Msg.Task != nil {
		t.Fatalf("expected no claim for non-pinned runner, got %+v", resp.Msg.Task)
	}

	resp, err = svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId:    "runner_pinned",
		WaitSeconds: 0,
	}))
	if err != nil {
		t.Fatalf("ClaimTask pinned runner: %v", err)
	}
	if resp.Msg.Task == nil || resp.Msg.Task.TaskId != "task_pinned" {
		t.Fatalf("expected pinned task, got %+v", resp.Msg.Task)
	}
}

func TestRPCClaimTaskRejectsEmptyRunnerID(t *testing.T) {
	svc, _, _, cleanup := newRPCTestService(t)
	defer cleanup()
	_, err := svc.ClaimTask(context.Background(), connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId:    "",
		WaitSeconds: 0,
	}))
	if err == nil {
		t.Fatal("expected error for empty runner_id")
	}
}

func TestClaimTaskSkipsRunningTasks(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Insert a task that has max_attempts exhausted
	now := time.Now().UTC()
	if err := q.InsertTask(ctx, repository.InsertTaskParams{
		ID:                "task_lease",
		Prompt:            "x",
		AgentImage:        sql.NullString{String: "runner:latest", Valid: true},
		Skills:            json.RawMessage(`[]`),
		Env:               json.RawMessage(`{}`),
		CommitAuthorName:  sql.NullString{String: "Chetter", Valid: true},
		CommitAuthorEmail: sql.NullString{String: "chetter@chetter.flatout.works", Valid: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	markTaskRunning(t, q, "task_lease", now)
	// Just verify no pending task is claimable while the task is still running.
	resp, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
		RunnerId:    "runner_1",
		WaitSeconds: 1,
	}))
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if resp.Msg.Task != nil {
		t.Fatalf("expected no claimable task, got %+v", resp.Msg.Task)
	}
}

func TestRPCHeartbeatReturnsCancelCommandForCancelledTask(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_cancel_me", "do", "runner:latest")
	now := time.Now().UTC()
	markTaskRunning(t, q, "task_cancel_me", now)
	markPendingExecutionAttemptClaimed(t, q, "task_cancel_me", "runner_1", now, now.Add(5*time.Minute))
	if _, err := q.CancelExecutionAttemptsByTask(ctx, repository.CancelExecutionAttemptsByTaskParams{
		Error: nullString("operator stop"), EndedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, TaskID: "task_cancel_me",
	}); err != nil {
		t.Fatalf("cancel execution attempt: %v", err)
	}
	if _, err := q.CancelTask(ctx, repository.CancelTaskParams{
		Error:     sql.NullString{String: "operator stop", Valid: true},
		EndedAt:   sql.NullTime{Time: time.Now().UTC(), Valid: true},
		UpdatedAt: time.Now().UTC(),
		ID:        "task_cancel_me",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:          "runner_1",
			Status:            "active",
			CurrentExecutions: []*runnerv1.RunningExecution{{TaskId: "task_cancel_me", ExecutionId: "exec_task_cancel_me"}},
		},
	}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 1 {
		t.Fatalf("expected one cancel command, got %+v", resp.Msg.Commands)
	}
	cmd := resp.Msg.Commands[0]
	if cmd.Type != "cancel" || cmd.TaskId != "task_cancel_me" || cmd.Reason != "operator stop" {
		t.Fatalf("unexpected command: %+v", cmd)
	}
}

func TestRPCHeartbeatNoTasksReturnsEmptyCommands(t *testing.T) {
	svc, _, _, cleanup := newRPCTestService(t)
	defer cleanup()
	resp, err := svc.Heartbeat(context.Background(), connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:          "runner_1",
			Status:            "active",
			CurrentExecutions: nil,
		},
	}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 0 {
		t.Fatalf("expected no commands, got %+v", resp.Msg.Commands)
	}
}

func TestRPCHeartbeatMixedTasksOnlyReturnsCancelled(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_running", "x", "runner:latest")
	insertPendingTask(t, q, "task_to_cancel", "x", "runner:latest")
	now := time.Now().UTC()
	markTaskRunning(t, q, "task_running", now)
	markPendingExecutionAttemptClaimed(t, q, "task_running", "runner_1", now, now.Add(5*time.Minute))
	if _, err := q.CancelTask(ctx, repository.CancelTaskParams{
		Error:     sql.NullString{String: "by operator", Valid: true},
		EndedAt:   sql.NullTime{Time: time.Now().UTC(), Valid: true},
		UpdatedAt: time.Now().UTC(),
		ID:        "task_to_cancel",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:          "runner_1",
			Status:            "active",
			CurrentExecutions: []*runnerv1.RunningExecution{{TaskId: "task_running", ExecutionId: "exec_task_running"}, {TaskId: "task_to_cancel"}},
		},
	}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 1 {
		t.Fatalf("expected one command, got %+v", resp.Msg.Commands)
	}
	if resp.Msg.Commands[0].TaskId != "task_to_cancel" {
		t.Fatalf("wrong task cancelled: %s", resp.Msg.Commands[0].TaskId)
	}
}

func TestRPCRegisterRunnerPersistsRow(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	startedAt := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano)

	_, err := svc.RegisterRunner(ctx, connect.NewRequest(&runnerv1.RegisterRunnerRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:       "runner_99",
			Status:         "active",
			ImageRef:       "ghcr.io/x/runner:v1",
			ImageDigest:    "sha256:abc",
			Version:        "v1.2.3",
			MaxConcurrent:  8,
			RunningTasks:   2,
			AvailableSlots: 6,
			TotalStarted:   10,
			TotalCompleted: 9,
			TotalErrors:    1,
			ExecutionMode:  "kata",
			StartedAt:      startedAt,
		},
	}))
	if err != nil {
		t.Fatalf("RegisterRunner: %v", err)
	}

	// Verify via listLiveRunners
	runners, err := q.ListLiveRunners(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("list runners: %v", err)
	}
	if len(runners) != 1 {
		t.Fatalf("expected 1 runner, got %d", len(runners))
	}
	r := runners[0]
	if r.ID != "runner_99" || r.Status != "active" {
		t.Errorf("unexpected runner row: %+v", r)
	}
	if r.MaxConcurrent != 8 || r.RunningTasks != 2 || r.AvailableSlots != 6 {
		t.Errorf("counters wrong: %+v", r)
	}
}

func TestRPCRegisterRunnerRejectsEmptyID(t *testing.T) {
	svc, _, _, cleanup := newRPCTestService(t)
	defer cleanup()
	_, err := svc.RegisterRunner(context.Background(), connect.NewRequest(&runnerv1.RegisterRunnerRequest{
		Runner: &runnerv1.RunnerInfo{RunnerId: ""},
	}))
	if err == nil {
		t.Fatal("expected error for empty runner_id")
	}
}

func TestRPCReportTaskEventsPersistsEventAndUpdatesTask(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_report", "x", "runner:latest")
	now := time.Now().UTC()
	markTaskRunning(t, q, "task_report", now)
	markPendingExecutionAttemptClaimed(t, q, "task_report", "runner_1", now, now.Add(time.Minute))

	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:      "task_report",
			ExecutionId: "exec_task_report",
			Status:      "done",
			Summary:     "completed",
			ProviderId:  "synthetic",
			ModelId:     "model-x",
			EndedAt:     endedAt,
		}},
	}))
	if err != nil {
		t.Fatalf("ReportTaskEvents: %v", err)
	}

	row, err := q.GetTaskByID(ctx, "task_report")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if row.Status != "done" {
		t.Errorf("expected status=done, got %s", row.Status)
	}
	if row.Summary.String != "completed" {
		t.Errorf("summary not persisted: %q", row.Summary.String)
	}
	session, err := q.GetAgentSessionByTaskID(ctx, "task_report")
	if err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if session.ProviderID.String != "synthetic" || session.ModelID.String != "model-x" {
		t.Errorf("session provider/model not persisted: %q/%q", session.ProviderID.String, session.ModelID.String)
	}
	if !row.EndedAt.Valid {
		t.Error("ended_at not persisted")
	}

	// Verify event row exists
	events, err := q.ListTaskEvents(ctx, repository.ListTaskEventsParams{
		TaskID: "task_report",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Status != "done" {
		t.Errorf("event status: %s", events[0].Status)
	}
}

func TestRPCReportTaskEventsRejectsEmptyTaskID(t *testing.T) {
	svc, _, _, cleanup := newRPCTestService(t)
	defer cleanup()
	_, err := svc.ReportTaskEvents(context.Background(), connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events:   []*runnerv1.TaskEvent{{TaskId: "", Status: "done"}},
	}))
	if err == nil {
		t.Fatal("expected error for empty task_id")
	}
}

func TestRPCHeartbeatRenewsLeasesForRunningTasks(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	claimAndExpire := func(id string) {
		t.Helper()
		markTaskRunning(t, q, id, now)
		markPendingExecutionAttemptClaimed(t, q, id, "runner_1", now, now.Add(-time.Minute))
	}
	insertPendingTask(t, q, "task_a", "x", "runner:latest")
	insertPendingTask(t, q, "task_b", "x", "runner:latest")
	claimAndExpire("task_a")
	claimAndExpire("task_b")

	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:          "runner_1",
			Status:            "active",
			CurrentExecutions: []*runnerv1.RunningExecution{{TaskId: "task_a", ExecutionId: "exec_task_a"}, {TaskId: "task_b", ExecutionId: "exec_task_b"}},
		},
	}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 0 {
		t.Fatalf("expected no cancel commands, got %+v", resp.Msg.Commands)
	}
	for _, id := range []string{"task_a", "task_b"} {
		row, err := q.GetExecutionAttemptByID(ctx, "exec_"+id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if !row.LeaseExpiresAt.Valid || !row.LeaseExpiresAt.Time.After(now) {
			t.Errorf("%s lease not renewed: %v", id, row.LeaseExpiresAt)
		}
	}
}

func TestRPCHeartbeatCancelsReclaimedTask(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertPendingTask(t, q, "task_reclaim", "x", "runner:latest")
	markTaskRunning(t, q, "task_reclaim", now)
	markPendingExecutionAttemptClaimed(t, q, "task_reclaim", "runner_1", now, now.Add(-time.Minute))
	if rows, err := q.MarkExecutionAttemptLost(ctx, repository.MarkExecutionAttemptLostParams{
		Error: nullString("lease reclaimed"), EndedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, ID: "exec_task_reclaim",
	}); err != nil || rows != 1 {
		t.Fatalf("mark attempt lost: rows=%d err=%v", rows, err)
	}
	if rows, err := q.RequeueTaskAfterExecutionAttemptLost(ctx, repository.RequeueTaskAfterExecutionAttemptLostParams{
		UpdatedAt: now, TaskID: "task_reclaim",
	}); err != nil || rows != 1 {
		t.Fatalf("requeue task: rows=%d err=%v", rows, err)
	}

	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:          "runner_1",
			Status:            "active",
			CurrentExecutions: []*runnerv1.RunningExecution{{TaskId: "task_reclaim", ExecutionId: "exec_task_reclaim"}},
		},
	}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 1 {
		t.Fatalf("expected one cancel command, got %+v", resp.Msg.Commands)
	}
	cmd := resp.Msg.Commands[0]
	if cmd.Type != "cancel" || cmd.TaskId != "task_reclaim" {
		t.Fatalf("unexpected command: %+v", cmd)
	}
	if !strings.Contains(cmd.Reason, "lease reclaimed") {
		t.Errorf("expected reclaim reason, got %q", cmd.Reason)
	}
}

func TestTaskToProto_ExtractsHarnessFromEnv(t *testing.T) {
	harnessJSON, _ := json.Marshal(map[string]string{
		"__chetter_harness": "pi",
		"CUSTOM_VAR":        "val",
	})
	task := repository.ChetterTask{
		ID:                "task-1",
		Prompt:            "test prompt",
		AgentImage:        sql.NullString{String: "img", Valid: true},
		Env:               harnessJSON,
		Skills:            []byte(`[]`),
		ProviderID:        sql.NullString{},
		ModelID:           sql.NullString{},
		VariantID:         sql.NullString{},
		Agent:             sql.NullString{},
		GitUrl:            sql.NullString{},
		GitRef:            sql.NullString{},
		CommitAuthorName:  sql.NullString{},
		CommitAuthorEmail: sql.NullString{},
	}
	proto := taskToProto(task, repository.ChetterExecutionAttempt{ID: "exec_test", TimeoutSec: 300}, 1, "", "")
	if proto.Harness != "pi" {
		t.Fatalf("expected harness='pi', got %q", proto.Harness)
	}
	if v, ok := proto.Env["__chetter_harness"]; ok {
		t.Fatalf("__chetter_harness should be removed from env, got %q", v)
	}
	if proto.Env["CUSTOM_VAR"] != "val" {
		t.Fatalf("CUSTOM_VAR should be preserved, got %q", proto.Env["CUSTOM_VAR"])
	}
	if proto.Env["__chetter_harness"] != "" {
		t.Fatal("__chetter_harness key should not exist in env map")
	}
}

func TestTaskToProto_NoHarnessIsEmpty(t *testing.T) {
	envJSON, _ := json.Marshal(map[string]string{"FOO": "bar"})
	task := repository.ChetterTask{
		ID:                "task-2",
		Prompt:            "test",
		Env:               envJSON,
		Skills:            []byte(`[]`),
		ProviderID:        sql.NullString{},
		ModelID:           sql.NullString{},
		VariantID:         sql.NullString{},
		Agent:             sql.NullString{},
		AgentImage:        sql.NullString{String: "img", Valid: true},
		GitUrl:            sql.NullString{},
		GitRef:            sql.NullString{},
		CommitAuthorName:  sql.NullString{},
		CommitAuthorEmail: sql.NullString{},
	}
	proto := taskToProto(task, repository.ChetterExecutionAttempt{ID: "exec_test", TimeoutSec: 300}, 1, "", "")
	if proto.Harness != "" {
		t.Fatalf("expected empty harness, got %q", proto.Harness)
	}
}

func TestResolveModelForTaskUsesHarnessMappings(t *testing.T) {
	catalog := &modelcatalog.Catalog{
		Version:         1,
		DefaultProvider: "synthetic",
		DefaultModel:    "default-model",
		Defaults: map[string]modelcatalog.HarnessDefault{
			"opencode": {Provider: "synthetic", Model: "default-model"},
		},
		Providers: map[string]modelcatalog.Provider{
			"synthetic": {
				Name:      "Synthetic",
				BaseURL:   "https://api.example.test/base",
				APIKeyEnv: "SYNTHETIC_API_KEY",
				Harnesses: map[string]modelcatalog.ProviderHarness{
					"opencode": {
						ID:         "synthetic-openai",
						Name:       "Synthetic OpenAI",
						BaseURL:    "https://api.example.test/openai",
						APIKeyEnv:  "SYNTHETIC_OPENAI_KEY",
						API:        "openai-completions",
						AuthHeader: true,
					},
				},
				Models: []modelcatalog.Model{{
					ID: "default-model",
					Harnesses: map[string]modelcatalog.ModelHarness{
						"opencode": {ID: "mapped-model"},
					},
				}},
			},
		},
	}
	got := resolveModelForTask(catalog, &runnerv1.Task{Harness: "opencode"})
	if got.ProviderID != "synthetic-openai" || got.ModelID != "mapped-model" {
		t.Fatalf("unexpected resolved model: %+v", got)
	}
	if got.ProviderName != "Synthetic OpenAI" || got.ProviderBaseURL != "https://api.example.test/openai" || got.ProviderAPIKeyEnv != "SYNTHETIC_OPENAI_KEY" || got.ProviderAPI != "openai-completions" || !got.ProviderAuthHeader {
		t.Fatalf("unexpected provider metadata: %+v", got)
	}
	if got.ProviderKind != "" || got.AwsProfile != "" || got.AwsRegion != "" {
		t.Fatalf("unexpected Bedrock fields on non-Bedrock provider: kind=%q profile=%q region=%q", got.ProviderKind, got.AwsProfile, got.AwsRegion)
	}
}

func TestResolveModelForTask_BedrockProvider(t *testing.T) {
	catalog := &modelcatalog.Catalog{
		Version:         1,
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5.4",
		Defaults: map[string]modelcatalog.HarnessDefault{
			"codex": {Provider: "aws-bedrock", Model: "bedrock-model"},
		},
		Providers: map[string]modelcatalog.Provider{
			"openai": {
				Name:      "OpenAI",
				Kind:      "native",
				BaseURL:   "https://api.openai.com/v1",
				APIKeyEnv: "OPENAI_API_KEY",
				Models:    []modelcatalog.Model{{ID: "gpt-5.4"}},
			},
			"aws-bedrock": {
				Name:       "Amazon Bedrock",
				Kind:       "aws_bedrock",
				BaseURL:    "https://bedrock-runtime.us-east-1.amazonaws.com",
				AwsProfile: "my-profile",
				AwsRegion:  "us-east-1",
				Models:     []modelcatalog.Model{{ID: "bedrock-model"}},
			},
		},
	}
	got := resolveModelForTask(catalog, &runnerv1.Task{Harness: "codex"})
	if got.ProviderID != "aws-bedrock" || got.ModelID != "bedrock-model" {
		t.Fatalf("unexpected resolved model: %+v", got)
	}
	if got.ProviderKind != "aws_bedrock" {
		t.Fatalf("expected ProviderKind=aws_bedrock, got %q", got.ProviderKind)
	}
	if got.AwsProfile != "my-profile" {
		t.Fatalf("expected AwsProfile=my-profile, got %q", got.AwsProfile)
	}
	if got.AwsRegion != "us-east-1" {
		t.Fatalf("expected AwsRegion=us-east-1, got %q", got.AwsRegion)
	}
}

func TestResolveModelForTask_BedrockWithHarnessOverride(t *testing.T) {
	catalog := &modelcatalog.Catalog{
		Version:         1,
		DefaultProvider: "aws-bedrock",
		DefaultModel:    "bedrock-model",
		Defaults: map[string]modelcatalog.HarnessDefault{
			"codex": {Provider: "aws-bedrock", Model: "bedrock-model"},
		},
		Providers: map[string]modelcatalog.Provider{
			"aws-bedrock": {
				Name:       "Amazon Bedrock",
				Kind:       "aws_bedrock",
				BaseURL:    "https://bedrock-runtime.us-east-1.amazonaws.com",
				AwsProfile: "global-profile",
				AwsRegion:  "global-region",
				Models:     []modelcatalog.Model{{ID: "bedrock-model"}},
				Harnesses: map[string]modelcatalog.ProviderHarness{
					"codex": {
						AwsProfile: "codex-profile",
						AwsRegion:  "codex-region",
					},
				},
			},
		},
	}
	got := resolveModelForTask(catalog, &runnerv1.Task{Harness: "codex"})
	if got.AwsProfile != "codex-profile" {
		t.Fatalf("expected harness override AwsProfile=codex-profile, got %q", got.AwsProfile)
	}
	if got.AwsRegion != "codex-region" {
		t.Fatalf("expected harness override AwsRegion=codex-region, got %q", got.AwsRegion)
	}
}

func TestDefaultCatalogIncludesBedrockProvider(t *testing.T) {
	catalog := modelcatalog.Default()
	p, ok := catalog.Providers["aws-bedrock"]
	if !ok {
		t.Fatal("expected aws-bedrock provider in default catalog")
	}
	if p.Kind != "aws_bedrock" {
		t.Fatalf("expected kind=aws_bedrock, got %q", p.Kind)
	}
	if p.Name != "Amazon Bedrock" {
		t.Fatalf("expected name=Amazon Bedrock, got %q", p.Name)
	}
	if len(p.Models) == 0 {
		t.Fatal("expected at least one model")
	}

	provider, model := catalog.DefaultForHarness("codex", "openai", "gpt-5.4")
	if provider != "openai" || model != "gpt-5.4" {
		t.Fatalf("codex default should be openai/gpt-5.4 (Bedrock is opt-in), got %s/%s", provider, model)
	}
}

func TestResolveModelForTaskNoHarnessMappingOmitsAPI(t *testing.T) {
	catalog := &modelcatalog.Catalog{
		Version:         1,
		DefaultProvider: "litellm",
		DefaultModel:    "coding-model",
		Defaults: map[string]modelcatalog.HarnessDefault{
			"opencode": {Provider: "litellm", Model: "coding-model"},
		},
		Providers: map[string]modelcatalog.Provider{
			"litellm": {
				Name:      "LiteLLM",
				BaseURL:   "https://litellm.example.com/v1",
				APIKeyEnv: "LITELLM_API_KEY",
				Models:    []modelcatalog.Model{{ID: "coding-model"}},
			},
		},
	}
	got := resolveModelForTask(catalog, &runnerv1.Task{Harness: "opencode"})
	if got.ProviderAPI != "" {
		t.Fatalf("ProviderAPI should be empty without harness mapping, got %q", got.ProviderAPI)
	}
	if got.ProviderAuthHeader {
		t.Fatal("ProviderAuthHeader should be false without harness mapping")
	}
	if got.ProviderBaseURL != "https://litellm.example.com/v1" {
		t.Fatalf("ProviderBaseURL should fall back to provider level, got %q", got.ProviderBaseURL)
	}
}

func TestResolveModelForTaskDisabledHarnessFallsBackToDefault(t *testing.T) {
	catalog := &modelcatalog.Catalog{
		Version:         1,
		DefaultProvider: "litellm",
		DefaultModel:    "coding-model",
		Defaults: map[string]modelcatalog.HarnessDefault{
			"opencode":  {Provider: "litellm", Model: "coding-model"},
			"codewhale": {Provider: "anthropic", Model: "claude-sonnet-4-5"},
		},
		Providers: map[string]modelcatalog.Provider{
			"litellm": {
				Name:      "LiteLLM",
				BaseURL:   "https://litellm.example.com/v1",
				APIKeyEnv: "LITELLM_API_KEY",
				Harnesses: map[string]modelcatalog.ProviderHarness{
					"codewhale": {
						Disabled: true,
						API:      "openai-completions",
					},
				},
				Models: []modelcatalog.Model{{ID: "coding-model"}},
			},
			"anthropic": {
				Name:      "Anthropic",
				BaseURL:   "https://api.anthropic.com",
				APIKeyEnv: "ANTHROPIC_API_KEY",
				Models:    []modelcatalog.Model{{ID: "claude-sonnet-4-5"}},
			},
		},
	}
	got := resolveModelForTask(catalog, &runnerv1.Task{
		Harness:    "codewhale",
		ProviderId: "litellm",
		ModelId:    "coding-model",
	})
	if got.ProviderID != "anthropic" {
		t.Fatalf("disabled harness should fall back to default provider, got %q", got.ProviderID)
	}
	if got.ModelID != "claude-sonnet-4-5" {
		t.Fatalf("disabled harness should fall back to default model, got %q", got.ModelID)
	}
	if got.ProviderAPI != "" {
		t.Fatalf("disabled harness fallback should not carry API, got %q", got.ProviderAPI)
	}
}

func TestResolveModelForTaskDisabledHarnessCircularGuard(t *testing.T) {
	catalog := &modelcatalog.Catalog{
		Version:         1,
		DefaultProvider: "litellm",
		DefaultModel:    "coding-model",
		Defaults: map[string]modelcatalog.HarnessDefault{
			"codewhale": {Provider: "litellm", Model: "coding-model"},
		},
		Providers: map[string]modelcatalog.Provider{
			"litellm": {
				Name:      "LiteLLM",
				BaseURL:   "https://litellm.example.com/v1",
				APIKeyEnv: "LITELLM_API_KEY",
				Harnesses: map[string]modelcatalog.ProviderHarness{
					"codewhale": {Disabled: true},
				},
				Models: []modelcatalog.Model{{ID: "coding-model"}},
			},
		},
	}
	got := resolveModelForTask(catalog, &runnerv1.Task{
		Harness:    "codewhale",
		ProviderId: "litellm",
		ModelId:    "coding-model",
	})
	if got.ProviderID == "" {
		t.Fatal("circular disabled fallback should return a non-empty provider ID")
	}
}
