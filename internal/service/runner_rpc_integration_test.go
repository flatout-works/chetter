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
	"github.com/flatout-works/chetter/internal/redact"
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
		ID: id, Prompt: prompt, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	sessionID := "sess_" + id
	promptID := "prompt_" + id
	if err := q.InsertAgentSession(context.Background(), repository.InsertAgentSessionParams{
		ID: sessionID, TaskID: id, Sequence: 1, Status: "running", ResumeMode: "none",
		AgentImage: sql.NullString{String: agentImage, Valid: true}, Skills: json.RawMessage(`[]`), Env: json.RawMessage(`{}`),
		CommitAuthorName: sql.NullString{String: "Chetter", Valid: true}, CommitAuthorEmail: sql.NullString{String: "chetter@chetter.flatout.works", Valid: true},
		CreatedAt: now, UpdatedAt: now,
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

func runningExecution(taskID string) *runnerv1.RunningExecution {
	return &runnerv1.RunningExecution{
		TaskId: taskID, ExecutionId: "exec_" + taskID,
		AgentSessionId: "sess_" + taskID, UserPromptId: "prompt_" + taskID, ClaimId: "claim_" + taskID,
	}
}

func markPendingExecutionAttemptClaimed(t *testing.T, q data.Repository, taskID, runnerID string, claimedAt, leaseExpiresAt time.Time) {
	t.Helper()
	if rows, err := q.MarkExecutionAttemptClaimed(context.Background(), repository.MarkExecutionAttemptClaimedParams{
		RunnerID:       nullString(runnerID),
		ClaimID:        "claim_" + taskID,
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
	if resp.Msg.Task.ClaimId == "" {
		t.Fatal("claim_id must be generated for every execution claim")
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
	if attempt.ClaimID != resp.Msg.Task.ClaimId {
		t.Errorf("attempt claim_id = %q, response claim_id = %q", attempt.ClaimID, resp.Msg.Task.ClaimId)
	}
	if !attempt.LeaseExpiresAt.Valid {
		t.Error("expected lease_expires_at set")
	}
	if !attempt.ClaimedAt.Valid {
		t.Error("expected claimed_at set")
	}
}

// TestRPCClaimTaskWokenByNotify verifies the claimNotifier path: a ClaimTask
// long-poll that came up empty is woken immediately when a task becomes
// claimable, instead of sleeping out the safety-net claimPollInterval. The
// notify is fired the way Service.SubmitTask does after committing a task.
func TestRPCClaimTaskWokenByNotify(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()

	claimed := make(chan *connect.Response[runnerv1.ClaimTaskResponse], 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_1", WaitSeconds: 20, LeaseSeconds: 60,
		}))
		if err != nil {
			errCh <- err
			return
		}
		claimed <- resp
	}()

	// Give the long-poll time to snapshot the notification channel and enter
	// its wait. Even if it has not quite done so, the test still passes: the
	// poll after the notify observes the committed row.
	time.Sleep(500 * time.Millisecond)

	insertPendingTask(t, q, "task_wake", "do work", "runner:latest")
	svc.NotifyTaskClaimable()

	select {
	case resp := <-claimed:
		if resp.Msg.Task == nil || resp.Msg.Task.TaskId != "task_wake" {
			t.Fatalf("expected task_wake, got %+v", resp.Msg.Task)
		}
	case err := <-errCh:
		t.Fatalf("ClaimTask: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("ClaimTask long-poll was not woken by NotifyTaskClaimable within 5s (safety poll is 15s)")
	}
}

func TestRPCClaimTaskWokenByCrossReplicaDBNotification(t *testing.T) {
	rpc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	// Synchronize with the poller's baseline read so the simulated remote
	// replica's increment is observed as a change, never absorbed into the
	// baseline.
	rpc.claimPollReady = make(chan struct{})
	pollerCtx, stopPoller := context.WithCancel(context.Background())
	defer stopPoller()
	go rpc.pollClaimNotifications(pollerCtx)
	select {
	case <-rpc.claimPollReady:
	case <-time.After(5 * time.Second):
		t.Fatal("claim notification poller did not initialize")
	}

	claimed := make(chan *connect.Response[runnerv1.ClaimTaskResponse], 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := rpc.ClaimTask(context.Background(), connect.NewRequest(&runnerv1.ClaimTaskRequest{
			RunnerId: "runner_remote", WaitSeconds: 20, LeaseSeconds: 60,
		}))
		if err != nil {
			errCh <- err
			return
		}
		claimed <- resp
	}()
	time.Sleep(300 * time.Millisecond)

	insertPendingTask(t, q, "task_remote_wake", "remote work", "runner:latest")
	bumpClaimNotifyCounter(context.Background(), tdb.DB, tdb.Dialect())

	select {
	case resp := <-claimed:
		if resp.Msg.Task == nil || resp.Msg.Task.TaskId != "task_remote_wake" {
			t.Fatalf("expected task_remote_wake, got %+v", resp.Msg.Task)
		}
	case err := <-errCh:
		t.Fatalf("ClaimTask: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("cross-replica DB notification did not wake ClaimTask")
	}
}

func TestRunnerDrainRequestCrossesReplicas(t *testing.T) {
	rpcA, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	rpcB := NewRunnerRPCService(q, tdb.DB, tdb.Dialect())
	ctx := context.Background()

	if err := rpcA.RequestDrain(ctx, "runner_cross_replica"); err != nil {
		t.Fatalf("RequestDrain: %v", err)
	}
	active := &runnerv1.RunnerInfo{RunnerId: "runner_cross_replica", Status: "active"}

	// Delivery is at-least-once: the command is re-sent on every heartbeat
	// until the runner acknowledges, so a lost heartbeat response cannot
	// lose the drain command.
	for i := 0; i < 2; i++ {
		resp, err := rpcB.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: active}))
		if err != nil {
			t.Fatalf("heartbeat %d: %v", i+1, err)
		}
		if len(resp.Msg.Commands) != 1 || resp.Msg.Commands[0].Type != "drain" {
			t.Fatalf("heartbeat %d commands = %+v, want one drain command", i+1, resp.Msg.Commands)
		}
	}

	// The runner acknowledges by reporting a draining status; the request is
	// removed and no longer delivered.
	draining := &runnerv1.RunnerInfo{RunnerId: "runner_cross_replica", Status: "draining"}
	resp, err := rpcB.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: draining}))
	if err != nil {
		t.Fatalf("draining Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 0 {
		t.Fatalf("draining heartbeat commands = %+v, want none", resp.Msg.Commands)
	}
	resp, err = rpcB.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: active}))
	if err != nil {
		t.Fatalf("post-ack Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 0 {
		t.Fatalf("drain request was not acked: %+v", resp.Msg.Commands)
	}
}

// TestRunnerDrainRequestDropsStaleRows verifies an unacknowledged drain
// request older than the TTL is dropped instead of resurrecting a drain long
// after the operator requested it.
func TestRunnerDrainRequestDropsStaleRows(t *testing.T) {
	rpc, _, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()

	if err := requestRunnerDrainDB(ctx, tdb.DB, tdb.Dialect(), "runner_stale_drain"); err != nil {
		t.Fatalf("request drain: %v", err)
	}
	stale := time.Now().UTC().Add(-2 * drainRequestTTL)
	if _, err := tdb.DB.ExecContext(ctx, testQuery(tdb.Dialect(),
		`UPDATE runner_drain_requests SET created_at = ? WHERE runner_id = ?`,
		`UPDATE runner_drain_requests SET created_at = $1 WHERE runner_id = $2`),
		stale, "runner_stale_drain"); err != nil {
		t.Fatalf("age drain request: %v", err)
	}

	info := &runnerv1.RunnerInfo{RunnerId: "runner_stale_drain", Status: "active"}
	resp, err := rpc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: info}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 0 {
		t.Fatalf("stale drain request was delivered: %+v", resp.Msg.Commands)
	}
	// The row is gone, not just skipped.
	requested, err := peekRunnerDrainDB(ctx, tdb.DB, tdb.Dialect(), "runner_stale_drain")
	if err != nil {
		t.Fatalf("peek after TTL drop: %v", err)
	}
	if requested {
		t.Fatal("stale drain request row was not dropped")
	}
}

// TestRecordTaskEventRedactsFromRegisteredSet verifies the same-replica
// redaction path end to end: a set registered at submission time scrubs the
// secret from every persisted field of a runner-reported event. The set is
// deliberately cache-only (plaintext secret values are never persisted), so
// there is intentionally no cross-replica fallback to test.
func TestRecordTaskEventRedactsFromRegisteredSet(t *testing.T) {
	rpc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_redact_local", "do work", "runner:latest")
	now := time.Now().UTC()
	markTaskRunning(t, q, "task_redact_local", now)
	markPendingExecutionAttemptClaimed(t, q, "task_redact_local", "runner_redact", now, now.Add(time.Minute))
	secret := "same-replica-secret-value"
	rpc.RegisterRedactSet("sess_task_redact_local", redact.NewSet(map[string]string{"API_TOKEN": secret}))

	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_redact",
		Events: []*runnerv1.TaskEvent{{
			TaskId:         "task_redact_local",
			ExecutionId:    "exec_task_redact_local",
			AgentSessionId: "sess_task_redact_local",
			UserPromptId:   "prompt_task_redact_local",
			ClaimId:        "claim_task_redact_local",
			Status:         "running",
			Summary:        "using token=" + secret,
			PayloadJson:    `{"detail":"token=` + secret + `"}`,
		}},
	})); err != nil {
		t.Fatalf("ReportTaskEvents: %v", err)
	}

	events, err := rpc.db.ListTaskEvents(ctx, repository.ListTaskEventsParams{TaskID: "task_redact_local", Limit: 50, Offset: 0})
	if err != nil {
		t.Fatalf("ListTaskEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events recorded")
	}
	for _, e := range events {
		if strings.Contains(string(e.Payload), secret) {
			t.Fatalf("secret leaked in persisted event payload: %q", string(e.Payload))
		}
		if !strings.Contains(string(e.Payload), "[REDACTED]") {
			t.Fatalf("payload not redacted: %q", string(e.Payload))
		}
	}

	// The fallback payload path (runner sends no valid PayloadJson) must
	// marshal the already-redacted event, not the original.
	if _, err := rpc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_redact",
		Events: []*runnerv1.TaskEvent{{
			TaskId:         "task_redact_local",
			ExecutionId:    "exec_task_redact_local",
			AgentSessionId: "sess_task_redact_local",
			UserPromptId:   "prompt_task_redact_local",
			ClaimId:        "claim_task_redact_local",
			Status:         "running",
			Summary:        "still using token=" + secret,
		}},
	})); err != nil {
		t.Fatalf("ReportTaskEvents (fallback payload): %v", err)
	}
	events, err = rpc.db.ListTaskEvents(ctx, repository.ListTaskEventsParams{TaskID: "task_redact_local", Limit: 50, Offset: 0})
	if err != nil {
		t.Fatalf("ListTaskEvents (fallback payload): %v", err)
	}
	for _, e := range events {
		if strings.Contains(string(e.Payload), secret) {
			t.Fatalf("secret leaked via fallback payload marshal: %q", string(e.Payload))
		}
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
		"UPDATE execution_attempts SET lease_expires_at = ? WHERE id = ?",
		"UPDATE execution_attempts SET lease_expires_at = $1 WHERE id = $2"),
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
			TaskId: "task_fenced", ExecutionId: firstExecution, ClaimId: first.Msg.Task.ClaimId,
			AgentSessionId: first.Msg.Task.AgentSessionId, UserPromptId: first.Msg.Task.UserPromptId, Status: "done",
		}},
	}))
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stale report error = %v, want fenced execution failure", err)
	}

	if _, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_2",
		Events: []*runnerv1.TaskEvent{{
			TaskId: "task_fenced", ExecutionId: secondExecution, ClaimId: second.Msg.Task.ClaimId,
			AgentSessionId: second.Msg.Task.AgentSessionId, UserPromptId: second.Msg.Task.UserPromptId, Status: "done",
		}},
	})); err != nil {
		t.Fatalf("current report: %v", err)
	}
}

func TestRPCRejectsExpiredRunningExecutionEvent(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertPendingTask(t, q, "task_expired_event", "work", "runner:latest")
	markTaskRunning(t, q, "task_expired_event", now)
	markPendingExecutionAttemptClaimed(t, q, "task_expired_event", "runner_1", now, now.Add(-time.Minute))
	before, err := q.GetExecutionAttemptByID(ctx, "exec_task_expired_event")
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	_, err = svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId: "task_expired_event", ExecutionId: "exec_task_expired_event", ClaimId: "claim_task_expired_event",
			AgentSessionId: "sess_task_expired_event", UserPromptId: "prompt_task_expired_event", Status: "running",
		}},
	}))
	if err == nil {
		t.Fatal("expired execution event was accepted")
	}
	after, err := q.GetExecutionAttemptByID(ctx, "exec_task_expired_event")
	if err != nil {
		t.Fatalf("get attempt after event: %v", err)
	}
	if after.Status != "running" || !after.LeaseExpiresAt.Time.Equal(before.LeaseExpiresAt.Time) {
		t.Fatalf("expired attempt changed after rejected event: before=%+v after=%+v", before, after)
	}
}

func TestRPCRejectsWrongExecutionClaimEvent(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_claim_fence", "work", "runner:latest")
	claim, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1"}))
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	_, err = svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId: claim.Msg.Task.TaskId, ExecutionId: claim.Msg.Task.ExecutionId,
			ClaimId: "clm_stale", AgentSessionId: claim.Msg.Task.AgentSessionId,
			UserPromptId: claim.Msg.Task.UserPromptId, Status: "done",
		}},
	}))
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("wrong claim event error = %v, want rejection", err)
	}
}

func TestRPCGitHubRequiresActiveExecutionClaim(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_github_claim", "work", "runner:latest")
	claim, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1"}))
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	request := func(claimID string) error {
		_, err := svc.GitHubCreateIssue(ctx, connect.NewRequest(&runnerv1.GitHubCreateIssueRequest{
			TaskId: claim.Msg.Task.TaskId, ExecutionId: claim.Msg.Task.ExecutionId,
			RunnerId: "runner_1", ClaimId: claimID, Repo: "org/repo", Title: "title",
		}))
		return err
	}
	if err := request("clm_wrong"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("wrong claim GitHub error = %v, want permission denied", err)
	}
	if err := request(claim.Msg.Task.ClaimId); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("valid claim GitHub error = %v, want GitHub configuration error", err)
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
	if _, err := tdb.DB.ExecContext(ctx, testQuery(tdb.Dialect(), "UPDATE execution_attempts SET required_runner_id = ? WHERE id = ?", "UPDATE execution_attempts SET required_runner_id = $1 WHERE id = $2"), "runner_pinned", "exec_task_pinned"); err != nil {
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
		ID: "task_lease", Prompt: "x", CreatedAt: now, UpdatedAt: now,
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
			CurrentExecutions: []*runnerv1.RunningExecution{runningExecution("task_cancel_me")},
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
	if cmd.AgentSessionId != "sess_task_cancel_me" || cmd.UserPromptId != "prompt_task_cancel_me" || cmd.ExecutionId != "exec_task_cancel_me" {
		t.Fatalf("cancel command hierarchy = %+v", cmd)
	}
}

func TestRPCHeartbeatRejectsMismatchedExecutionHierarchy(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_hierarchy", "x", "runner:latest")
	now := time.Now().UTC()
	markTaskRunning(t, q, "task_hierarchy", now)
	initialLease := now.Add(time.Minute)
	markPendingExecutionAttemptClaimed(t, q, "task_hierarchy", "runner_1", now, initialLease)
	before, err := q.GetExecutionAttemptByID(ctx, "exec_task_hierarchy")
	if err != nil {
		t.Fatalf("get initial execution attempt: %v", err)
	}

	execution := runningExecution("task_hierarchy")
	execution.UserPromptId = "prompt_wrong"
	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: &runnerv1.RunnerInfo{
		RunnerId: "runner_1", Status: "active", CurrentExecutions: []*runnerv1.RunningExecution{execution},
	}}))
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 1 || !strings.Contains(resp.Msg.Commands[0].Reason, "hierarchy") {
		t.Fatalf("hierarchy mismatch commands = %+v", resp.Msg.Commands)
	}
	attempt, err := q.GetExecutionAttemptByID(ctx, execution.ExecutionId)
	if err != nil {
		t.Fatalf("get execution attempt: %v", err)
	}
	if !attempt.LeaseExpiresAt.Time.Equal(before.LeaseExpiresAt.Time) {
		t.Fatalf("mismatched heartbeat renewed lease from %s to %s", before.LeaseExpiresAt.Time, attempt.LeaseExpiresAt.Time)
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
	markTaskRunning(t, q, "task_to_cancel", now)
	markPendingExecutionAttemptClaimed(t, q, "task_to_cancel", "runner_1", now, now.Add(5*time.Minute))
	if _, err := q.CancelExecutionAttemptsByTask(ctx, repository.CancelExecutionAttemptsByTaskParams{
		Error: nullString("by operator"), EndedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, TaskID: "task_to_cancel",
	}); err != nil {
		t.Fatalf("cancel execution attempt: %v", err)
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
			RunnerId:          "runner_1",
			Status:            "active",
			CurrentExecutions: []*runnerv1.RunningExecution{runningExecution("task_running"), runningExecution("task_to_cancel")},
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

func TestRPCHeartbeatAuditsMCPRelayRejections(t *testing.T) {
	svc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	svc.WithSecurityAuditLogger(&Service{repo: q})

	if _, err := svc.RegisterRunner(ctx, connect.NewRequest(&runnerv1.RegisterRunnerRequest{
		Runner: &runnerv1.RunnerInfo{RunnerId: "runner_security_audit"},
	})); err != nil {
		t.Fatalf("RegisterRunner: %v", err)
	}
	for _, rejected := range []int64{2, 2, 5, 1, 2} {
		if _, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
			Runner: &runnerv1.RunnerInfo{
				RunnerId:                 "runner_security_audit",
				McpRelayRejectedRequests: rejected,
			},
		})); err != nil {
			t.Fatalf("Heartbeat(rejected=%d): %v", rejected, err)
		}
	}

	rows, err := tdb.DB.QueryContext(ctx, `
		SELECT source_id, target_type, payload
		FROM audit_log
		WHERE event_type = 'runner_mcp_relay_request_rejected'
	`)
	if err != nil {
		t.Fatalf("query security audit events: %v", err)
	}
	defer rows.Close()

	deltas := make(map[int64]int)
	var count int
	for rows.Next() {
		var sourceID, targetType sql.NullString
		var payload json.RawMessage
		if err := rows.Scan(&sourceID, &targetType, &payload); err != nil {
			t.Fatalf("scan security audit event: %v", err)
		}
		if sourceID.String != "runner_security_audit" || targetType.String != "mcp_relay" {
			t.Errorf("unexpected audit identity: source=%q target=%q", sourceID.String, targetType.String)
		}
		var detail struct {
			Delta      int64 `json:"rejected_requests_delta"`
			Cumulative int64 `json:"rejected_requests_cumulative"`
		}
		if err := json.Unmarshal(payload, &detail); err != nil {
			t.Fatalf("decode security audit payload: %v", err)
		}
		if detail.Cumulative <= 0 {
			t.Errorf("invalid cumulative count in payload: %s", payload)
		}
		deltas[detail.Delta]++
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("security audit rows: %v", err)
	}
	if count != 3 || deltas[1] != 1 || deltas[2] != 1 || deltas[3] != 1 {
		t.Fatalf("audit deltas = %v (count %d), want one each of 1, 2, and 3", deltas, count)
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
	if _, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId: "task_report", AgentSessionId: "sess_wrong", UserPromptId: "prompt_task_report",
			ExecutionId: "exec_task_report", Status: "running",
		}},
	})); err == nil {
		t.Fatal("expected mismatched event hierarchy to be rejected")
	}

	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId:         "task_report",
			ExecutionId:    "exec_task_report",
			AgentSessionId: "sess_task_report",
			UserPromptId:   "prompt_task_report",
			ClaimId:        "claim_task_report",
			Status:         "done",
			Summary:        "completed",
			ProviderId:     "synthetic",
			ModelId:        "model-x",
			EndedAt:        endedAt,
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
	claim := func(id string) {
		t.Helper()
		markTaskRunning(t, q, id, now)
		markPendingExecutionAttemptClaimed(t, q, id, "runner_1", now, now.Add(time.Minute))
	}
	insertPendingTask(t, q, "task_a", "x", "runner:latest")
	insertPendingTask(t, q, "task_b", "x", "runner:latest")
	claim("task_a")
	claim("task_b")

	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
		Runner: &runnerv1.RunnerInfo{
			RunnerId:          "runner_1",
			Status:            "active",
			CurrentExecutions: []*runnerv1.RunningExecution{runningExecution("task_a"), runningExecution("task_b")},
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

func TestRPCHeartbeatRejectsWrongExecutionClaim(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertPendingTask(t, q, "task_heartbeat_claim", "x", "runner:latest")
	markTaskRunning(t, q, "task_heartbeat_claim", now)
	markPendingExecutionAttemptClaimed(t, q, "task_heartbeat_claim", "runner_1", now, now.Add(-time.Minute))
	before, err := q.GetExecutionAttemptByID(ctx, "exec_task_heartbeat_claim")
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	execution := runningExecution("task_heartbeat_claim")
	execution.ClaimId = "wrong-claim"
	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: &runnerv1.RunnerInfo{
		RunnerId: "runner_1", Status: "active", CurrentExecutions: []*runnerv1.RunningExecution{execution},
	}}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 1 || !strings.Contains(resp.Msg.Commands[0].Reason, "claim") {
		t.Fatalf("wrong claim commands = %+v", resp.Msg.Commands)
	}
	after, err := q.GetExecutionAttemptByID(ctx, "exec_task_heartbeat_claim")
	if err != nil {
		t.Fatalf("get attempt after heartbeat: %v", err)
	}
	if !after.LeaseExpiresAt.Time.Equal(before.LeaseExpiresAt.Time) {
		t.Fatal("wrong claim heartbeat renewed the lease")
	}
}

func TestRPCHeartbeatDoesNotRenewExpiredExecution(t *testing.T) {
	svc, q, _, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	insertPendingTask(t, q, "task_heartbeat_expired", "x", "runner:latest")
	markTaskRunning(t, q, "task_heartbeat_expired", now)
	markPendingExecutionAttemptClaimed(t, q, "task_heartbeat_expired", "runner_1", now, now.Add(-time.Minute))
	before, err := q.GetExecutionAttemptByID(ctx, "exec_task_heartbeat_expired")
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	resp, err := svc.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: &runnerv1.RunnerInfo{
		RunnerId: "runner_1", Status: "active", CurrentExecutions: []*runnerv1.RunningExecution{runningExecution("task_heartbeat_expired")},
	}}))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(resp.Msg.Commands) != 1 || !strings.Contains(resp.Msg.Commands[0].Reason, "no longer running") {
		t.Fatalf("expired heartbeat commands = %+v", resp.Msg.Commands)
	}
	after, err := q.GetExecutionAttemptByID(ctx, "exec_task_heartbeat_expired")
	if err != nil {
		t.Fatalf("get attempt after heartbeat: %v", err)
	}
	if !after.LeaseExpiresAt.Time.Equal(before.LeaseExpiresAt.Time) {
		t.Fatal("expired heartbeat renewed the lease")
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
			CurrentExecutions: []*runnerv1.RunningExecution{runningExecution("task_reclaim")},
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

func TestRPCPruneWorkspacesIsAttemptScopedAndProtectsRetainedSession(t *testing.T) {
	svc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	ctx := context.Background()
	insertPendingTask(t, q, "task_prune", "x", "runner:latest")

	firstClaim, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1"}))
	if err != nil {
		t.Fatalf("claim first attempt: %v", err)
	}
	first := firstClaim.Msg.Task
	if _, err := svc.ReportTaskEvents(ctx, connect.NewRequest(&runnerv1.ReportTaskEventsRequest{
		RunnerId: "runner_1",
		Events: []*runnerv1.TaskEvent{{
			TaskId: first.TaskId, AgentSessionId: first.AgentSessionId, UserPromptId: first.UserPromptId,
			ExecutionId: first.ExecutionId, ClaimId: first.ClaimId, Status: "done",
		}},
	})); err != nil {
		t.Fatalf("complete first attempt: %v", err)
	}

	now := time.Now().UTC()
	if err := q.InsertAgentSession(ctx, repository.InsertAgentSessionParams{
		ID: "sess_task_prune_2", TaskID: first.TaskId, Sequence: 2, Status: "running", ResumeMode: "harness_session",
		Skills: json.RawMessage(`[]`), Env: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now, StartedAt: sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		t.Fatalf("insert retained session: %v", err)
	}
	if err := q.InsertUserPrompt(ctx, repository.InsertUserPromptParams{
		ID: "prompt_task_prune_2", AgentSessionID: "sess_task_prune_2", TaskID: first.TaskId,
		Sequence: 1, Status: "pending", Prompt: "resume", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert retained prompt: %v", err)
	}
	if err := q.InsertPendingExecutionAttempt(ctx, repository.InsertPendingExecutionAttemptParams{
		ID: "exec_task_prune_2", UserPromptID: "prompt_task_prune_2", Sequence: 1, TimeoutSec: 600, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert retained attempt: %v", err)
	}
	if rows, err := q.RequeueTaskForPrompt(ctx, repository.RequeueTaskForPromptParams{ID: first.TaskId, UpdatedAt: now}); err != nil || rows != 1 {
		t.Fatalf("requeue task: rows=%d err=%v", rows, err)
	}
	secondClaim, err := svc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_1"}))
	if err != nil {
		t.Fatalf("claim retained attempt: %v", err)
	}
	second := secondClaim.Msg.Task
	retainedPath := "/var/lib/runner/task_prune/exec_task_prune_2/workspace"
	if _, err := q.PauseAgentSessionByTaskID(ctx, repository.PauseAgentSessionByTaskIDParams{
		Status: "paused", PinnedRunnerID: nullString("runner_1"), WorkspacePath: nullString(retainedPath),
		PausedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, TaskID: first.TaskId,
	}); err != nil {
		t.Fatalf("pause retained session: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, testQuery(tdb.Dialect(),
		"UPDATE execution_attempts SET status = 'succeeded' WHERE id = ?",
		"UPDATE execution_attempts SET status = 'succeeded' WHERE id = $1"), second.ExecutionId); err != nil {
		t.Fatalf("finish retained attempt: %v", err)
	}

	resp, err := svc.PruneWorkspaces(ctx, connect.NewRequest(&runnerv1.PruneWorkspacesRequest{
		RunnerId: "runner_1",
		Candidates: []*runnerv1.WorkspaceCandidate{
			{TaskId: first.TaskId, ExecutionId: first.ExecutionId, WorkspacePath: "/var/lib/runner/task_prune/" + first.ExecutionId + "/workspace"},
			{TaskId: second.TaskId, ExecutionId: second.ExecutionId, WorkspacePath: retainedPath},
		},
	}))
	if err != nil {
		t.Fatalf("prune workspaces: %v", err)
	}
	if len(resp.Msg.SafeToDelete) != 1 || resp.Msg.SafeToDelete[0].ExecutionId != first.ExecutionId {
		t.Fatalf("safe workspaces = %+v, want only %s", resp.Msg.SafeToDelete, first.ExecutionId)
	}
}

func TestTaskToProto_UsesSessionConfig(t *testing.T) {
	envJSON, _ := json.Marshal(map[string]string{"CUSTOM_VAR": "val"})
	task := repository.Task{
		ID: "task-1", Prompt: "test prompt",
		GitUrl: sql.NullString{}, GitRef: sql.NullString{},
	}
	session := repository.AgentSession{
		ID: "sess-1", AgentImage: sql.NullString{String: "img", Valid: true}, Env: envJSON, Skills: []byte(`[]`),
		Harness:           sql.NullString{String: "pi", Valid: true},
		ProviderID:        sql.NullString{},
		ModelID:           sql.NullString{},
		VariantID:         sql.NullString{},
		Agent:             sql.NullString{},
		CommitAuthorName:  sql.NullString{},
		CommitAuthorEmail: sql.NullString{},
	}
	proto := taskToProto(task, session, repository.ExecutionAttempt{ID: "exec_test", TimeoutSec: 300}, 1, "", "")
	if proto.Harness != "pi" {
		t.Fatalf("expected harness='pi', got %q", proto.Harness)
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
	task := repository.Task{
		ID: "task-2", Prompt: "test", GitUrl: sql.NullString{}, GitRef: sql.NullString{},
	}
	session := repository.AgentSession{
		ID: "sess-2", Env: envJSON, Skills: []byte(`[]`),
		ProviderID:        sql.NullString{},
		ModelID:           sql.NullString{},
		VariantID:         sql.NullString{},
		Agent:             sql.NullString{},
		AgentImage:        sql.NullString{String: "img", Valid: true},
		CommitAuthorName:  sql.NullString{},
		CommitAuthorEmail: sql.NullString{},
	}
	proto := taskToProto(task, session, repository.ExecutionAttempt{ID: "exec_test", TimeoutSec: 300}, 1, "", "")
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
