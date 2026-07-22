package controller

import (
	"context"
	"testing"

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
