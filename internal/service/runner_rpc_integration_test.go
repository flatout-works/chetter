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
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/testdb"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

func newRPCTestService(t *testing.T) (*RunnerRPCService, *repository.Queries, *testdb.TestDB, func()) {
	t.Helper()
	tdb, cleanup := svcTestDB.NewTestDB(t)
	tdb.Truncate(t)
	q := repository.New(tdb.DB)
	return NewRunnerRPCService(q, tdb.DB), q, tdb, cleanup
}

func insertPendingTask(t *testing.T, q *repository.Queries, id, prompt, agentImage string) {
	t.Helper()
	now := time.Now().UTC()
	if err := q.InsertTask(context.Background(), repository.InsertTaskParams{
		ID:                id,
		Prompt:            prompt,
		AgentImage:        sql.NullString{String: agentImage, Valid: true},
		Skills:            json.RawMessage(`[]`),
		McpProfiles:       json.RawMessage(`[]`),
		Env:               json.RawMessage(`{}`),
		TimeoutSec:        600,
		CommitAuthorName:  sql.NullString{String: "Chetter", Valid: true},
		CommitAuthorEmail: sql.NullString{String: "chetter@chetter.flatout.works", Valid: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("insert task: %v", err)
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
	if !row.RunnerID.Valid || row.RunnerID.String != "runner_1" {
		t.Errorf("expected runner_id=runner_1, got %v", row.RunnerID)
	}
	if !row.LeaseExpiresAt.Valid {
		t.Error("expected lease_expires_at set")
	}
	if !row.ClaimedAt.Valid {
		t.Error("expected claimed_at set")
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
	if _, err := tdb.DB.ExecContext(ctx, "UPDATE chetter_tasks SET required_runner_id = ? WHERE id = ?", "runner_pinned", "task_pinned"); err != nil {
		t.Fatalf("pin task: %v", err)
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
		McpProfiles:       json.RawMessage(`[]`),
		Env:               json.RawMessage(`{}`),
		TimeoutSec:        600,
		CommitAuthorName:  sql.NullString{String: "Chetter", Valid: true},
		CommitAuthorEmail: sql.NullString{String: "chetter@chetter.flatout.works", Valid: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Mark it as running with an expired lease and max attempts reached
	past := now.Add(-1 * time.Hour)
	rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: past, Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             "task_lease",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if rows != 1 {
		t.Fatalf("update rows: %d", rows)
	}
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
	if rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(5 * time.Minute), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             "task_cancel_me",
	}); err != nil {
		t.Fatalf("claim: %v", err)
	} else if rows != 1 {
		t.Fatalf("claim rows: %d", rows)
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
			RunnerId:       "runner_1",
			Status:         "active",
			CurrentTaskIds: []string{"task_cancel_me"},
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
			RunnerId:       "runner_1",
			Status:         "active",
			CurrentTaskIds: nil,
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
	if rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(5 * time.Minute), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             "task_running",
	}); err != nil {
		t.Fatalf("claim: %v", err)
	} else if rows != 1 {
		t.Fatalf("claim rows: %d", rows)
	}
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
			RunnerId:       "runner_1",
			Status:         "active",
			CurrentTaskIds: []string{"task_running", "task_to_cancel"},
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
	if rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(time.Minute), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             "task_report",
	}); err != nil {
		t.Fatalf("claim: %v", err)
	} else if rows != 1 {
		t.Fatalf("claim rows: %d", rows)
	}

	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:     "task_report",
			Status:     "done",
			Summary:    "completed",
			ProviderId: "synthetic",
			ModelId:    "model-x",
			EndedAt:    endedAt,
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
	if row.ProviderID.String != "synthetic" {
		t.Errorf("provider_id not persisted: %q", row.ProviderID.String)
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

func TestReapAndFailLeaveReclaimedTaskPending(t *testing.T) {
	tdb, cleanup := testdb.NewForTesting(t)
	defer cleanup()
	tdb.Truncate(t)
	ctx := context.Background()
	q := repository.New(tdb.DB)

	// Insert a running task with an expired lease
	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)
	if err := q.InsertTask(ctx, repository.InsertTaskParams{
		ID:                "task_expired",
		Prompt:            "x",
		AgentImage:        sql.NullString{String: "runner:latest", Valid: true},
		Skills:            json.RawMessage(`[]`),
		McpProfiles:       json.RawMessage(`[]`),
		Env:               json.RawMessage(`{}`),
		TimeoutSec:        600,
		CommitAuthorName:  sql.NullString{String: "Chetter", Valid: true},
		CommitAuthorEmail: sql.NullString{String: "chetter@chetter.flatout.works", Valid: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: past, Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             "task_expired",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if rows != 1 {
		t.Fatalf("update rows: %d", rows)
	}

	// Run the reaper
	repo := q
	expiredBefore := sql.NullTime{Time: now, Valid: true}
	if _, err := repo.ReclaimExpiredLeases(ctx, repository.ReclaimExpiredLeasesParams{
		UpdatedAt:      now,
		LeaseExpiresAt: expiredBefore,
	}); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	// Task should still be running (attempt=1 < max_attempts=3)
	row, err := q.GetTaskByID(ctx, "task_expired")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Status != "pending" {
		t.Errorf("expected status=pending after reclaim, got %s", row.Status)
	}

	// Now fail it
	if _, err := repo.FailExpiredLeases(ctx, repository.FailExpiredLeasesParams{
		EndedAt:        sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: expiredBefore,
	}); err != nil {
		t.Fatalf("fail: %v", err)
	}
	row, err = q.GetTaskByID(ctx, "task_expired")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Status != "pending" {
		t.Errorf("expected status=pending still, got %s", row.Status)
	}
}

func TestRPCHeartbeatRenewsLeasesForRunningTasks(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	claimAndExpire := func(id string) {
		t.Helper()
		if rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
			RunnerID:       sql.NullString{String: "runner_1", Valid: true},
			ClaimedAt:      sql.NullTime{Time: now, Valid: true},
			LeaseExpiresAt: sql.NullTime{Time: now.Add(-1 * time.Minute), Valid: true},
			StartedAt:      sql.NullTime{Time: now, Valid: true},
			UpdatedAt:      now,
			LastEventAt:    sql.NullTime{Time: now, Valid: true},
			ID:             id,
		}); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		} else if rows != 1 {
			t.Fatalf("claim %s rows: %d", id, rows)
		}
	}
	insertPendingTask(t, q, "task_a", "x", "runner:latest")
	insertPendingTask(t, q, "task_b", "x", "runner:latest")
	claimAndExpire("task_a")
	claimAndExpire("task_b")

	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:       "runner_1",
			Status:         "active",
			CurrentTaskIds: []string{"task_a", "task_b"},
		},
	}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 0 {
		t.Fatalf("expected no cancel commands, got %+v", resp.Msg.Commands)
	}
	for _, id := range []string{"task_a", "task_b"} {
		row, err := q.GetTaskByID(ctx, id)
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
	if rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
		RunnerID:       sql.NullString{String: "runner_1", Valid: true},
		ClaimedAt:      sql.NullTime{Time: now, Valid: true},
		LeaseExpiresAt: sql.NullTime{Time: now.Add(-1 * time.Minute), Valid: true},
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		UpdatedAt:      now,
		LastEventAt:    sql.NullTime{Time: now, Valid: true},
		ID:             "task_reclaim",
	}); err != nil {
		t.Fatalf("claim: %v", err)
	} else if rows != 1 {
		t.Fatalf("claim rows: %d", rows)
	}
	if _, err := q.ReclaimExpiredLeases(ctx, repository.ReclaimExpiredLeasesParams{
		UpdatedAt:      now,
		LeaseExpiresAt: sql.NullTime{Time: now.Add(-1 * time.Second), Valid: true},
	}); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:       "runner_1",
			Status:         "active",
			CurrentTaskIds: []string{"task_reclaim"},
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
		TimeoutSec:        300,
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
	proto := taskToProto(task, "", "")
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
		TimeoutSec:        300,
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
	proto := taskToProto(task, "", "")
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
						ID:        "synthetic-openai",
						Name:      "Synthetic OpenAI",
						BaseURL:   "https://api.example.test/openai",
						APIKeyEnv: "SYNTHETIC_OPENAI_KEY",
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
	if got.ProviderName != "Synthetic OpenAI" || got.ProviderBaseURL != "https://api.example.test/openai" || got.ProviderAPIKeyEnv != "SYNTHETIC_OPENAI_KEY" {
		t.Fatalf("unexpected provider metadata: %+v", got)
	}
}
