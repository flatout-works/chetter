package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/data"
)

// TestIsolationAdmissionHardenedMode verifies the P0 isolation gate (issue
// #291): in hardened mode (no escape hatch) every task is isolation-requiring,
// and server-side admission only hands such tasks to runners that advertise
// enforced isolation. A non-isolated runner must never see the task.
func TestIsolationAdmissionHardenedMode(t *testing.T) {
	svc, tdb, cleanup := newServiceForTestWithIsolation(t, false)
	defer cleanup()
	ctx := context.Background()

	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(q, tdb.DB, tdb.Dialect())

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "hardened task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !session.IsolationRequired {
		t.Fatal("hardened-mode task should be marked isolation_required")
	}

	// A non-isolated runner (no isolation_enabled row at all) cannot claim it.
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_plain", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim by non-isolated runner: %v", err)
	}
	if claim.Msg.Task != nil {
		t.Fatalf("non-isolated runner claimed isolation-requiring task %s", claim.Msg.Task.TaskId)
	}

	// An isolation-capable runner claims it.
	registerIsolationCapableRunner(t, q, "runner_gvisor")
	claim, err = rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_gvisor", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim by isolation-capable runner: %v", err)
	}
	if claim.Msg.Task == nil || claim.Msg.Task.TaskId != rec.ID {
		t.Fatalf("isolation-capable runner did not claim the task: %+v", claim.Msg.Task)
	}
	if !claim.Msg.Task.IsolationRequired {
		t.Fatal("claimed task must carry isolation_required=true to the runner")
	}
}

// TestIsolationAdmissionTrustedMode verifies the documented escape hatch: with
// CHETTER_ALLOW_UNISOLATED=true the server only marks resumable or explicitly
// configured tasks as isolation-requiring, so a trusted single-tenant fleet
// without gVisor keeps running ordinary tasks on any runner.
func TestIsolationAdmissionTrustedMode(t *testing.T) {
	svc, tdb, cleanup := newServiceForTestWithIsolation(t, true)
	defer cleanup()
	ctx := context.Background()

	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(q, tdb.DB, tdb.Dialect())

	// Ordinary task: not isolation-requiring, claimable by a plain runner.
	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "trusted task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.IsolationRequired {
		t.Fatal("trusted-mode ordinary task should not be isolation_required")
	}
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_plain", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Msg.Task == nil || claim.Msg.Task.TaskId != rec.ID {
		t.Fatalf("plain runner could not claim trusted-mode task: %+v", claim.Msg.Task)
	}

	// Explicitly configured task (isolation: required) is isolation-requiring
	// even in trusted mode and must not land on a plain runner.
	rec2, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "explicitly isolated", AgentImage: "runner:latest", Isolation: "required"})
	if err != nil {
		t.Fatalf("submit explicit: %v", err)
	}
	run2, err := q.GetUserPromptByTaskID(ctx, rec2.ID)
	if err != nil {
		t.Fatalf("get prompt 2: %v", err)
	}
	session2, err := q.GetAgentSessionByID(ctx, run2.AgentSessionID)
	if err != nil {
		t.Fatalf("get session 2: %v", err)
	}
	if !session2.IsolationRequired {
		t.Fatal("explicit isolation: required task should be isolation_required")
	}
	claim2, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_plain", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	if claim2.Msg.Task != nil {
		t.Fatalf("plain runner claimed explicitly isolation-requiring task %s", claim2.Msg.Task.TaskId)
	}
	registerIsolationCapableRunner(t, q, "runner_gvisor")
	claim2, err = rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_gvisor", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim 2 by capable runner: %v", err)
	}
	if claim2.Msg.Task == nil || claim2.Msg.Task.TaskId != rec2.ID {
		t.Fatalf("capable runner did not claim explicit task: %+v", claim2.Msg.Task)
	}
}

// TestReaperFailsIsolationTasksWithoutCapableRunner verifies the fail-fast
// path: an isolation-requiring task with no live isolation-capable runner is
// failed with error_category isolation_unavailable instead of waiting forever.
func TestReaperFailsIsolationTasksWithoutCapableRunner(t *testing.T) {
	svc, tdb, cleanup := newServiceForTestWithIsolation(t, false)
	defer cleanup()
	ctx := context.Background()

	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(q, tdb.DB, tdb.Dialect())

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "no capable runner", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// No isolation-capable runner is registered; a plain claim gets nothing.
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_plain", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Msg.Task != nil {
		t.Fatalf("non-isolated runner claimed task: %+v", claim.Msg.Task)
	}

	// The reaper fails it fast.
	svc.reapIsolationUnavailableTasks()

	task, err := q.GetTaskByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != "error" {
		t.Fatalf("task status = %s, want error", task.Status)
	}
	if task.ErrorCategory.String != "isolation_unavailable" {
		t.Fatalf("error_category = %q, want isolation_unavailable", task.ErrorCategory.String)
	}
	if task.FailureCategory.String != "harness_error" {
		t.Fatalf("failure_category = %q, want harness_error", task.FailureCategory.String)
	}
	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	attempts, err := q.ListExecutionAttemptsByPrompt(ctx, run.ID)
	if err != nil {
		t.Fatalf("get attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(attempts))
	}
	attempt := attempts[0]
	if attempt.Status != "error" || attempt.ErrorCategory.String != "isolation_unavailable" {
		t.Fatalf("attempt status/error_category = %s/%q, want error/isolation_unavailable", attempt.Status, attempt.ErrorCategory.String)
	}
}

// TestIsolationAdmissionResumableAlwaysRequiresIsolation verifies that
// resumable sessions require enforced isolation even in trusted mode.
func TestIsolationAdmissionResumableAlwaysRequiresIsolation(t *testing.T) {
	svc, tdb, cleanup := newServiceForTestWithIsolation(t, true)
	defer cleanup()
	ctx := context.Background()

	q := data.New(tdb.DB, tdb.Dialect())
	rpc := NewRunnerRPCService(q, tdb.DB, tdb.Dialect())

	rec, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "resumable", AgentImage: "runner:latest", SessionMode: "resumable"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	run, err := q.GetUserPromptByTaskID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	session, err := q.GetAgentSessionByID(ctx, run.AgentSessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !session.IsolationRequired {
		t.Fatal("resumable session must always be isolation_required")
	}

	// A plain runner must not claim it.
	claim, err := rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_plain", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Msg.Task != nil {
		t.Fatalf("plain runner claimed resumable task: %+v", claim.Msg.Task)
	}

	registerIsolationCapableRunner(t, q, "runner_gvisor")
	claim, err = rpc.ClaimTask(ctx, connect.NewRequest(&runnerv1.ClaimTaskRequest{RunnerId: "runner_gvisor", WaitSeconds: 0}))
	if err != nil {
		t.Fatalf("claim by capable runner: %v", err)
	}
	if claim.Msg.Task == nil || claim.Msg.Task.TaskId != rec.ID {
		t.Fatalf("capable runner did not claim resumable task: %+v", claim.Msg.Task)
	}
	if !claim.Msg.Task.CheckpointAfterSuccess || !claim.Msg.Task.IsolationRequired {
		t.Fatal("resumable claim should carry checkpoint_after_success and isolation_required")
	}
}
