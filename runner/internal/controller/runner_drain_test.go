package controller

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
)

// recordingReportClient is a runnerRPCClient that records every
// ReportTaskEvents call. ReportTaskEvents blocks for `block` (simulating a
// slow server) and fails the first `failFirst` calls (simulating a flaky or
// unavailable server during a deploy). All other methods panic via the
// embedded interface.
type recordingReportClient struct {
	runnerRPCClient
	mu        sync.Mutex
	events    []*runnerv1.TaskEvent
	received  chan struct{}
	block     time.Duration
	failFirst atomic.Int64
}

func newRecordingReportClient(block time.Duration, failFirst int) *recordingReportClient {
	c := &recordingReportClient{
		received: make(chan struct{}, 64),
		block:    block,
	}
	c.failFirst.Store(int64(failFirst))
	return c
}

func (c *recordingReportClient) ReportTaskEvents(_ context.Context, req *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error) {
	if c.block > 0 {
		time.Sleep(c.block)
	}
	if c.failFirst.Load() > 0 {
		c.failFirst.Add(-1)
		return nil, errors.New("server unavailable")
	}
	c.mu.Lock()
	c.events = append(c.events, req.Msg.Events...)
	c.mu.Unlock()
	select {
	case c.received <- struct{}{}:
	default:
	}
	return connect.NewResponse(&runnerv1.ReportTaskEventsResponse{}), nil
}

// terminalEvents returns the recorded events whose status is terminal.
func (c *recordingReportClient) terminalEvents() []*runnerv1.TaskEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*runnerv1.TaskEvent
	for _, e := range c.events {
		if isTerminalStatus(e.Status) {
			out = append(out, e)
		}
	}
	return out
}

// newReportTestRunner builds a Runner wired with the given RPC client and a
// bounded hard-kill timeout for the drain completion barrier (issue #313).
func newReportTestRunner(t *testing.T, client runnerRPCClient, hardKill time.Duration) *Runner {
	t.Helper()
	r := &Runner{
		cfg:                  &config.Config{Runner: config.RunnerConfig{MaxConcurrent: 2}},
		rpcClient:            client,
		tasks:                make(map[string]*task.TaskSession),
		tasksChanged:         make(chan struct{}),
		runnerID:             "runner-test",
		startedAt:            time.Now().UTC(),
		terminalTasks:        make(map[string]struct{}),
		cancelledTasks:       make(map[string]struct{}),
		reportDelivered:      make(map[string]bool),
		drainHardKillTimeout: hardKill,
		sandbox:              newSandboxMetrics(),
	}
	return r
}

// simulateTask registers a task session and spawns a goroutine that mimics a
// real runTask: it performs `teardown` of cleanup work, publishes a terminal
// report synchronously, then removes itself from the task map and releases its
// taskWG slot (issue #313). The goroutine is tracked on the task barrier so
// waitDrain/waitForTaskCleanup join on it.
func (r *Runner) simulateTask(t *testing.T, req task.TaskRequest, teardown time.Duration, status, message string) {
	t.Helper()
	r.mu.Lock()
	r.tasks[req.ExecutionID] = &task.TaskSession{
		TaskID:      req.TaskID,
		ExecutionID: req.ExecutionID,
		Request:     req,
		Cancel:      func() {},
		StartedAt:   time.Now(),
	}
	r.mu.Unlock()
	r.taskWG.Add(1)
	go func() {
		defer r.taskWG.Done()
		defer func() {
			r.mu.Lock()
			delete(r.tasks, req.ExecutionID)
			close(r.tasksChanged)
			r.tasksChanged = make(chan struct{})
			r.mu.Unlock()
		}()
		time.Sleep(teardown)
		r.publishStatusForRequest(req, status, message, nil)
	}()
}

// TestWaitDrainForcedJoinsTerminalReportBeforeExit is the acceptance test for
// issue #313: after the drain deadline expires the runner must block until
// task cleanup completes AND the terminal report is delivered to the server —
// never exiting mid-cleanup. The report takes longer than the old cleanup
// grace, which previously lost the result.
func TestWaitDrainForcedJoinsTerminalReportBeforeExit(t *testing.T) {
	client := newRecordingReportClient(50*time.Millisecond, 0)
	r := newReportTestRunner(t, client, 5*time.Second)
	r.draining.Store(true)

	req := task.TaskRequest{TaskID: "task-1", ExecutionID: "exec-1", AgentSessionID: "sess-1", TimeoutSec: 60}
	r.simulateTask(t, req, 200*time.Millisecond, "done", "ok")

	started := time.Now()
	forced := r.waitDrain(10 * time.Millisecond)
	elapsed := time.Since(started)

	if !forced {
		t.Fatal("drain deadline expired with a running task; want forced exit")
	}
	if elapsed < 200*time.Millisecond {
		t.Fatalf("drain returned before task cleanup + terminal report completed: %v", elapsed)
	}
	events := client.terminalEvents()
	if len(events) != 1 || events[0].ExecutionId != "exec-1" || events[0].Status != "done" {
		t.Fatalf("terminal events = %+v, want exec-1/done delivered before waitDrain returns", events)
	}
}

// TestWaitDrainGracefulHappyPathStillDeliversReport is the no-regression test
// for the graceful-drain happy path (issue #97/#160): a task that finishes
// within the drain deadline exits cleanly (non-forced) with its terminal
// report delivered.
func TestWaitDrainGracefulHappyPathStillDeliversReport(t *testing.T) {
	client := newRecordingReportClient(0, 0)
	r := newReportTestRunner(t, client, 5*time.Second)
	r.draining.Store(true)

	req := task.TaskRequest{TaskID: "task-1", ExecutionID: "exec-1", TimeoutSec: 60}
	r.simulateTask(t, req, 30*time.Millisecond, "done", "ok")

	started := time.Now()
	forced := r.waitDrain(time.Second)
	elapsed := time.Since(started)

	if forced {
		t.Fatal("task finished within the drain deadline; want clean (non-forced) drain")
	}
	if elapsed < 30*time.Millisecond {
		t.Fatalf("drain returned before the task completed: %v", elapsed)
	}
	events := client.terminalEvents()
	if len(events) != 1 || events[0].ExecutionId != "exec-1" || events[0].Status != "done" {
		t.Fatalf("terminal events = %+v, want exec-1/done delivered", events)
	}
}

// TestWaitDrainHardKillLogsInFlightTasks verifies the hard-kill path: when
// cleanup is stuck past CHETTER_DRAIN_HARD_KILL_TIMEOUT_SEC the runner exits
// promptly and logs each in-flight execution with its terminal-report delivery
// status so operators can audit lost results (issue #313 criterion 4).
func TestWaitDrainHardKillLogsInFlightTasks(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	client := newRecordingReportClient(0, 0)
	r := newReportTestRunner(t, client, 100*time.Millisecond)
	r.draining.Store(true)

	req := task.TaskRequest{TaskID: "task-1", ExecutionID: "exec-1", TimeoutSec: 60}
	release := make(chan struct{})
	r.mu.Lock()
	r.tasks["exec-1"] = &task.TaskSession{
		TaskID:      req.TaskID,
		ExecutionID: req.ExecutionID,
		Request:     req,
		Cancel:      func() {},
		StartedAt:   time.Now(),
	}
	r.mu.Unlock()
	r.taskWG.Add(1)
	go func() {
		defer r.taskWG.Done()
		defer func() {
			r.mu.Lock()
			delete(r.tasks, "exec-1")
			close(r.tasksChanged)
			r.tasksChanged = make(chan struct{})
			r.mu.Unlock()
		}()
		<-release // simulate teardown stuck (e.g. docker stop hung)
	}()

	started := time.Now()
	forced := r.waitDrain(5 * time.Millisecond)
	elapsed := time.Since(started)
	close(release)

	if !forced {
		t.Fatal("want forced exit when the drain deadline expires")
	}
	if elapsed < 100*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("hard-kill wait = %v, want bounded by the hard-kill timeout (100ms)", elapsed)
	}
	logged := buf.String()
	if !strings.Contains(logged, "hard kill timeout") || !strings.Contains(logged, "exec-1") {
		t.Fatalf("audit log missing in-flight execution details: %s", logged)
	}

	// Let the released goroutine finish so the test does not leak it.
	done := make(chan struct{})
	go func() { r.taskWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task goroutine did not exit after release")
	}
}

// TestTerminalReportBudgetClampedByHardKillDeadline verifies the terminal
// report retry loop stops at the hard-kill deadline instead of retrying for
// its full 1-minute window, so a blocked ReportTaskEvents RPC during a drain
// cannot outlive the runner (issue #313 criterion 2).
func TestTerminalReportBudgetClampedByHardKillDeadline(t *testing.T) {
	client := newRecordingReportClient(0, 100000) // server never accepts the report
	r := newReportTestRunner(t, client, 0)

	// Simulate an in-flight forced cleanup whose hard-kill deadline is 150ms
	// away (waitForTaskCleanup would have set this).
	r.mu.Lock()
	r.drainHardKillDeadline = time.Now().Add(150 * time.Millisecond)
	r.mu.Unlock()

	resp := task.TaskResponse{TaskID: "task-1", ExecutionID: "exec-1", Status: "done", Summary: "ok"}
	started := time.Now()
	r.reportTaskResponse(resp)
	elapsed := time.Since(started)

	// The report must give up around the hard-kill deadline, not spin for the
	// full terminalReportRetryWindow.
	if elapsed < 100*time.Millisecond {
		t.Fatalf("terminal report gave up before the hard-kill deadline: %v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("terminal report retried past the hard-kill budget: %v", elapsed)
	}
	if got := client.terminalEvents(); len(got) != 0 {
		t.Fatalf("report should not have been delivered with a failing server: %+v", got)
	}
}

// TestTerminalReportBudgetUnboundedWhenNotDraining ensures the retry window
// stays at terminalReportRetryWindow when no forced cleanup is in progress
// (no hard-kill deadline), so normal operation still retries a flaky server
// for the full minute (issue #313 regression guard).
func TestTerminalReportBudgetUnboundedWhenNotDraining(t *testing.T) {
	client := newRecordingReportClient(0, 2) // fails twice, then succeeds
	r := newReportTestRunner(t, client, 0)

	resp := task.TaskResponse{TaskID: "task-1", ExecutionID: "exec-1", Status: "done", Summary: "ok"}
	r.reportTaskResponse(resp)

	events := client.terminalEvents()
	if len(events) != 1 || events[0].ExecutionId != "exec-1" || events[0].Status != "done" {
		t.Fatalf("terminal events = %+v, want the report retried to success", events)
	}
	r.mu.Lock()
	delivered := r.reportDelivered["exec-1"]
	r.mu.Unlock()
	if !delivered {
		t.Fatal("exec-1 should be marked delivered after the successful retry")
	}
}

func TestDrainHardKillTimeoutUsesEnvironment(t *testing.T) {
	t.Setenv("CHETTER_DRAIN_HARD_KILL_TIMEOUT_SEC", "7")
	if got := drainHardKillTimeout(); got != 7*time.Second {
		t.Fatalf("drainHardKillTimeout() = %v, want 7s", got)
	}

	t.Setenv("CHETTER_DRAIN_HARD_KILL_TIMEOUT_SEC", "invalid")
	if got := drainHardKillTimeout(); got != defaultDrainHardKillTimeout {
		t.Fatalf("invalid hard-kill timeout = %v, want %v", got, defaultDrainHardKillTimeout)
	}
}

// TestWaitDrainForcedFlakyServerRetriesUntilDelivered verifies the complete
// drain lifecycle against a flaky server: the terminal report retries (the
// first attempt fails) and the drain still blocks until the retried report is
// delivered before returning (issue #313 criteria 1 and 2).
func TestWaitDrainForcedFlakyServerRetriesUntilDelivered(t *testing.T) {
	client := newRecordingReportClient(0, 1) // first attempt fails
	r := newReportTestRunner(t, client, 5*time.Second)
	r.draining.Store(true)

	req := task.TaskRequest{TaskID: "task-1", ExecutionID: "exec-1", TimeoutSec: 60}
	r.simulateTask(t, req, 30*time.Millisecond, "error", "prompt failed")

	started := time.Now()
	forced := r.waitDrain(10 * time.Millisecond)
	elapsed := time.Since(started)

	if !forced {
		t.Fatal("drain deadline expired with a running task; want forced exit")
	}
	if elapsed < 30*time.Millisecond {
		t.Fatalf("drain returned before cleanup + retried report completed: %v", elapsed)
	}
	events := client.terminalEvents()
	if len(events) != 1 || events[0].ExecutionId != "exec-1" || events[0].Status != "error" {
		t.Fatalf("terminal events = %+v, want exec-1/error delivered after retry", events)
	}
}
