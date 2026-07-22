package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/repository"
)

func TestIsHeartbeatSummary(t *testing.T) {
	tests := []struct {
		name    string
		summary string
		want    bool
	}{
		{"exact heartbeat", "opencode: server.heartbeat", true},
		{"heartbeat with json payload", `opencode: server.heartbeat {"task_id":"task_123"}`, true},
		{"non-heartbeat event", `opencode: session.status {"status":"busy"}`, false},
		{"empty string", "", false},
		{"message.part.updated", `opencode: message.part.updated {"part":{}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHeartbeatSummary(tc.summary); got != tc.want {
				t.Errorf("isHeartbeatSummary(%q) = %v, want %v", tc.summary, got, tc.want)
			}
		})
	}
}

func TestShouldStoreHeartbeatThrottle(t *testing.T) {
	svc := NewRunnerRPCService(nil, nil)

	// First call should return true (store)
	if !svc.shouldStoreHeartbeat("task_throttle") {
		t.Error("first call should return true")
	}

	// Immediate second call should return false (throttled)
	if svc.shouldStoreHeartbeat("task_throttle") {
		t.Error("immediate second call should return false")
	}

	// Different task should return true (independent throttle)
	if !svc.shouldStoreHeartbeat("task_other") {
		t.Error("different task should return true")
	}
}

func TestHeartbeatThrottlingStoresFirstButNotSecond(t *testing.T) {
	svc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_hb1", "x", "runner:latest")
	now := time.Now().UTC()
	if _, err := q.MarkTaskRunning(ctx, repository.MarkTaskRunningParams{
		UpdatedAt: now,
		ID:        "task_hb1",
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	markPendingExecutionAttemptClaimed(t, q, "task_hb1", "runner_hb", now, now.Add(time.Minute))

	// First heartbeat event — should be stored
	_, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_hb",
		Events: []*runnerv1.TaskEvent{{
			TaskId:      "task_hb1",
			ExecutionId: "exec_task_hb1",
			Status:      "running",
			Summary:     "opencode: server.heartbeat",
		}},
	}))
	if err != nil {
		t.Fatalf("first heartbeat report: %v", err)
	}

	// Second heartbeat event immediately after — should be skipped (throttled)
	_, err = svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_hb",
		Events: []*runnerv1.TaskEvent{{
			TaskId:      "task_hb1",
			ExecutionId: "exec_task_hb1",
			Status:      "running",
			Summary:     "opencode: server.heartbeat",
		}},
	}))
	if err != nil {
		t.Fatalf("second heartbeat report: %v", err)
	}

	// Count running event rows — should be exactly 1 (throttled)
	rows, err := tdb.DB.QueryContext(ctx, testQuery(tdb.Dialect(),
		`SELECT COUNT(*) FROM chetter_task_events WHERE task_id = ? AND status = 'running'`,
		`SELECT COUNT(*) FROM chetter_task_events WHERE task_id = $1 AND status = 'running'`),
		"task_hb1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no rows")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 heartbeat event row (throttled), got %d", count)
	}

	// Verify lease was still renewed on both calls despite throttling the event row
	attempt, err := q.GetExecutionAttemptByID(ctx, "exec_task_hb1")
	if err != nil {
		t.Fatalf("get execution attempt: %v", err)
	}
	if !attempt.LeaseExpiresAt.Valid {
		t.Error("lease_expires_at should be valid")
	}
	if attempt.LeaseExpiresAt.Time.Before(now.Add(50 * time.Second)) {
		t.Error("lease should have been renewed to at least now+60s")
	}
}

func TestNonHeartbeatEventsAlwaysStored(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_nonhb", "x", "runner:latest")
	now := time.Now().UTC()
	if _, err := q.MarkTaskRunning(ctx, repository.MarkTaskRunningParams{
		UpdatedAt: now,
		ID:        "task_nonhb",
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	markPendingExecutionAttemptClaimed(t, q, "task_nonhb", "runner_nh", now, now.Add(time.Minute))

	// Send two non-heartbeat running events rapidly — both should be stored
	for i := 0; i < 2; i++ {
		_, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
			RunnerId: "runner_nh",
			Events: []*runnerv1.TaskEvent{{
				TaskId:      "task_nonhb",
				ExecutionId: "exec_task_nonhb",
				Status:      "running",
				Summary:     `opencode: session.status {"status":"busy"}`,
			}},
		}))
		if err != nil {
			t.Fatalf("report event %d: %v", i, err)
		}
	}

	events, err := q.ListTaskEvents(ctx, repository.ListTaskEventsParams{
		TaskID: "task_nonhb",
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 non-heartbeat event rows, got %d", len(events))
	}
}
