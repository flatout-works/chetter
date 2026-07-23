package controller

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestCancelTaskRequiresExactExecutionHierarchy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{
		tasks: map[string]*task.TaskSession{
			"exec_1": {
				TaskID: "task_1", ExecutionID: "exec_1", Cancel: cancel,
				Request: task.TaskRequest{TaskID: "task_1", AgentSessionID: "sess_1", UserPromptID: "prompt_1", ExecutionID: "exec_1"},
			},
		},
		cancelledTasks: make(map[string]struct{}),
	}

	r.cancelTask("task_1", "sess_1", "prompt_wrong", "exec_1", "stop")
	select {
	case <-ctx.Done():
		t.Fatal("mismatched hierarchy cancelled execution")
	default:
	}
	if len(r.cancelledTasks) != 0 {
		t.Fatal("mismatched hierarchy was recorded as cancelled")
	}

	r.cancelTask("task_1", "sess_1", "prompt_1", "exec_1", "stop")
	select {
	case <-ctx.Done():
	default:
		t.Fatal("exact hierarchy did not cancel execution")
	}
}

func TestWaitDrainWaitsForTaskChange(t *testing.T) {
	r := &Runner{
		tasks:        map[string]*task.TaskSession{"task-1": {}},
		tasksChanged: make(chan struct{}),
	}
	r.draining.Store(true)

	done := make(chan struct{})
	var forced bool
	go func() {
		forced = r.waitDrain(time.Second)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("drain finished before the task exited")
	case <-time.After(25 * time.Millisecond):
	}

	r.mu.Lock()
	delete(r.tasks, "task-1")
	close(r.tasksChanged)
	r.tasksChanged = make(chan struct{})
	r.mu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish after the task exited")
	}
	if forced {
		t.Fatal("waitDrain reported forced, want false for a clean drain")
	}
}

func TestWaitDrainCancelsRemainingTasksAtDeadline(t *testing.T) {
	var cancelled atomic.Bool
	r := &Runner{
		tasks: map[string]*task.TaskSession{
			"task-1": {Cancel: func() { cancelled.Store(true) }},
		},
		tasksChanged: make(chan struct{}),
	}
	r.draining.Store(true)

	forced := r.waitDrain(10 * time.Millisecond)
	if !cancelled.Load() {
		t.Fatal("drain deadline did not cancel the remaining task")
	}
	if !forced {
		t.Fatal("waitDrain reported clean drain, want true when tasks were force-cancelled")
	}
}

// TestWaitDrainReturnsFalseWhenNotDraining ensures the SIGTERM path (which
// only calls waitDrain when draining is set) reports a clean, non-forced exit
// when no drain was initiated. See issue #97.
func TestWaitDrainReturnsFalseWhenNotDraining(t *testing.T) {
	r := &Runner{
		tasks:        map[string]*task.TaskSession{"task-1": {}},
		tasksChanged: make(chan struct{}),
	}
	if forced := r.waitDrain(time.Second); forced {
		t.Fatal("waitDrain reported forced when not draining, want false")
	}
}

func TestDrainTimeoutUsesEnvironment(t *testing.T) {
	t.Setenv("CHETTER_DRAIN_TIMEOUT_SEC", "7")
	if got := drainTimeout(); got != 7*time.Second {
		t.Fatalf("drainTimeout() = %v, want 7s", got)
	}

	t.Setenv("CHETTER_DRAIN_TIMEOUT_SEC", "invalid")
	if got := drainTimeout(); got != defaultDrainTimeout {
		t.Fatalf("invalid drain timeout = %v, want %v", got, defaultDrainTimeout)
	}
}

// TestDefaultDrainTimeoutAlignsWithK8sGrace locks in the issue #97
// requirement that the default drain timeout is 30s, matching Kubernetes'
// default terminationGracePeriodSeconds so the runner uses the SIGTERM
// grace window instead of dying instantly.
func TestDefaultDrainTimeoutAlignsWithK8sGrace(t *testing.T) {
	if defaultDrainTimeout != 30*time.Second {
		t.Fatalf("defaultDrainTimeout = %v, want 30s (Kubernetes default grace)", defaultDrainTimeout)
	}
}

// mockHeartbeatClient is a minimal runnerRPCClient that records the runner
// status strings published via Heartbeat. It embeds the interface so only
// Heartbeat needs to be implemented for the drain tests; other methods panic
// if called (they are not exercised here).
type mockHeartbeatClient struct {
	runnerRPCClient
	mu       sync.Mutex
	statuses []string
}

func (m *mockHeartbeatClient) Heartbeat(_ context.Context, req *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	m.mu.Lock()
	if req.Msg != nil && req.Msg.Runner != nil {
		m.statuses = append(m.statuses, req.Msg.Runner.Status)
	}
	m.mu.Unlock()
	return connect.NewResponse(&runnerv1.HeartbeatResponse{}), nil
}

func (m *mockHeartbeatClient) recordedStatuses() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.statuses...)
}

// newDrainTestRunner builds a Runner wired with a mock heartbeat client and
// just enough config/state for publishRunnerHeartbeat/runnerInfoProto to run.
func newDrainTestRunner(t *testing.T) (*Runner, *mockHeartbeatClient) {
	t.Helper()
	mb := &mockHeartbeatClient{}
	r := &Runner{
		cfg:            &config.Config{Runner: config.RunnerConfig{MaxConcurrent: 2}},
		rpcClient:      mb,
		tasks:          make(map[string]*task.TaskSession),
		tasksChanged:   make(chan struct{}),
		runnerID:       "runner-test",
		startedAt:      time.Now().UTC(),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, 2),
	}
	return r, mb
}

// TestBeginGracefulShutdownDrainsAndHeartbeats verifies the SIGTERM entry
// point (issue #97): BeginGracefulShutdown sets the draining flag and
// immediately publishes a "draining" heartbeat so the server reassigns
// in-flight tasks sooner, without waiting for the 5s heartbeat tick.
func TestBeginGracefulShutdownDrainsAndHeartbeats(t *testing.T) {
	r, mb := newDrainTestRunner(t)

	if r.draining.Load() {
		t.Fatal("runner should start not draining")
	}

	r.BeginGracefulShutdown()

	if !r.draining.Load() {
		t.Fatal("BeginGracefulShutdown did not set the draining flag")
	}
	got := mb.recordedStatuses()
	if len(got) != 1 || got[0] != "draining" {
		t.Fatalf("heartbeat statuses = %v, want [draining]", got)
	}
}

// TestBeginGracefulShutdownIdempotent ensures a second signal (e.g. a
// follow-up SIGINT during drain) does not publish duplicate draining
// heartbeats or otherwise interfere. See issue #97 acceptance criteria.
func TestBeginGracefulShutdownIdempotent(t *testing.T) {
	r, mb := newDrainTestRunner(t)

	r.BeginGracefulShutdown()
	r.BeginGracefulShutdown()

	// startDrain is a Swap-based guard, so only the first call publishes.
	got := mb.recordedStatuses()
	if len(got) != 1 {
		t.Fatalf("heartbeat statuses = %v, want exactly one draining heartbeat", got)
	}
}

// TestForcedExitDefaultFalse verifies the exit-code signal defaults to false
// for a clean drain so main.go exits 0 when no tasks were force-cancelled.
func TestForcedExitDefaultFalse(t *testing.T) {
	r, _ := newDrainTestRunner(t)
	if r.ForcedExit() {
		t.Fatal("ForcedExit should default to false on a fresh runner")
	}
}

// TestComputeDrainDeadlineNoTasks verifies that with no in-flight tasks, the
// drain deadline falls back to the configured ceiling. See issue #160.
func TestComputeDrainDeadlineNoTasks(t *testing.T) {
	r, _ := newDrainTestRunner(t)
	t.Setenv("CHETTER_DRAIN_TIMEOUT_SEC", "120")
	if got := r.computeDrainDeadline(); got != 120*time.Second {
		t.Fatalf("computeDrainDeadline with no tasks = %v, want 120s (ceiling)", got)
	}
}

// TestComputeDrainDeadlineDerivedFromTaskTimeouts verifies that the drain
// deadline is derived from the maximum remaining timeout of in-flight tasks,
// clamped by the configured ceiling. A task with 60s timeout that just started
// should produce a deadline near 60s (not the 30s ceiling). See issue #160
// criterion 1.
func TestComputeDrainDeadlineDerivedFromTaskTimeouts(t *testing.T) {
	r, _ := newDrainTestRunner(t)
	t.Setenv("CHETTER_DRAIN_TIMEOUT_SEC", "600") // 10 min ceiling

	r.mu.Lock()
	r.tasks["exec-1"] = &task.TaskSession{
		TaskID:      "task-1",
		ExecutionID: "exec-1",
		StartedAt:   time.Now().Add(-5 * time.Second), // 5s elapsed
		Request:     task.TaskRequest{TimeoutSec: 60}, // 60s timeout
	}
	r.mu.Unlock()

	got := r.computeDrainDeadline()
	// remaining ≈ 55s, should be between 50s and 60s
	if got < 50*time.Second || got > 60*time.Second {
		t.Fatalf("computeDrainDeadline = %v, want ~55s (60s timeout - 5s elapsed)", got)
	}
}

// TestComputeDrainDeadlineClampedByCeiling verifies that a long-running task
// does not extend the drain beyond the configured ceiling. See issue #160
// criterion 1.
func TestComputeDrainDeadlineClampedByCeiling(t *testing.T) {
	r, _ := newDrainTestRunner(t)
	t.Setenv("CHETTER_DRAIN_TIMEOUT_SEC", "30") // 30s ceiling

	r.mu.Lock()
	r.tasks["exec-1"] = &task.TaskSession{
		TaskID:      "task-1",
		ExecutionID: "exec-1",
		StartedAt:   time.Now().Add(-5 * time.Second),
		Request:     task.TaskRequest{TimeoutSec: 3600}, // 1 hour timeout
	}
	r.mu.Unlock()

	got := r.computeDrainDeadline()
	if got != 30*time.Second {
		t.Fatalf("computeDrainDeadline = %v, want 30s (ceiling clamp)", got)
	}
}

func TestComputeDrainDeadlineUsesTaskTimeoutByDefault(t *testing.T) {
	t.Setenv("CHETTER_DRAIN_TIMEOUT_SEC", "")
	r, _ := newDrainTestRunner(t)
	r.mu.Lock()
	r.tasks["exec-1"] = &task.TaskSession{
		TaskID:      "task-1",
		ExecutionID: "exec-1",
		StartedAt:   time.Now().Add(-5 * time.Second),
		Request:     task.TaskRequest{TimeoutSec: 120},
	}
	r.mu.Unlock()

	got := r.computeDrainDeadline()
	if got < 110*time.Second || got > 120*time.Second {
		t.Fatalf("computeDrainDeadline = %v, want ~115s from task timeout", got)
	}
}

// TestWaitDrainPreservesResumableTaskWorkspace verifies that the force-cancel
// path preserves the workspace for tasks with CheckpointAfterSuccess, so the
// session can be resumed on a fresh runner. See issue #160 criterion 3.
func TestWaitDrainPreservesResumableTaskWorkspace(t *testing.T) {
	var cancelled atomic.Bool
	r, _ := newDrainTestRunner(t)
	r.draining.Store(true)

	r.mu.Lock()
	r.tasks["exec-1"] = &task.TaskSession{
		TaskID:      "task-1",
		ExecutionID: "exec-1",
		StartedAt:   time.Now(),
		Request:     task.TaskRequest{TimeoutSec: 60, CheckpointAfterSuccess: true},
		Cancel:      func() { cancelled.Store(true) },
	}
	r.mu.Unlock()

	r.waitDrain(10 * time.Millisecond)

	if !cancelled.Load() {
		t.Fatal("drain did not cancel the task")
	}

	r.mu.Lock()
	session := r.tasks["exec-1"]
	r.mu.Unlock()
	if session == nil {
		t.Fatal("task session was removed from r.tasks — should still be present during force-cancel")
	}
	if !session.PreserveWorkspace {
		t.Fatal("PreserveWorkspace should be true for resumable tasks on drain force-cancel")
	}
}

// TestWaitDrainDoesNotPreserveNonResumableTaskWorkspace verifies that
// non-resumable tasks (CheckpointAfterSuccess=false) do NOT get their workspace
// preserved — they are cancelled normally. See issue #160 criterion 2.
func TestWaitDrainDoesNotPreserveNonResumableTaskWorkspace(t *testing.T) {
	var cancelled atomic.Bool
	r, _ := newDrainTestRunner(t)
	r.draining.Store(true)

	r.mu.Lock()
	r.tasks["exec-1"] = &task.TaskSession{
		TaskID:      "task-1",
		ExecutionID: "exec-1",
		StartedAt:   time.Now(),
		Request:     task.TaskRequest{TimeoutSec: 60, CheckpointAfterSuccess: false},
		Cancel:      func() { cancelled.Store(true) },
	}
	r.mu.Unlock()

	r.waitDrain(10 * time.Millisecond)

	if !cancelled.Load() {
		t.Fatal("drain did not cancel the task")
	}

	r.mu.Lock()
	session := r.tasks["exec-1"]
	r.mu.Unlock()
	if session != nil && session.PreserveWorkspace {
		t.Fatal("PreserveWorkspace should be false for non-resumable tasks")
	}
}
