package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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
	go func() {
		r.waitDrain(time.Second)
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

	r.waitDrain(10 * time.Millisecond)
	if !cancelled.Load() {
		t.Fatal("drain deadline did not cancel the remaining task")
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
