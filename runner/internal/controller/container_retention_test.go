package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
)

type retainedWorkspaceHarness struct{ harness.ServeHarness }

func (h retainedWorkspaceHarness) WaitForReady(context.Context, string, string, time.Duration) error {
	return fmt.Errorf("test harness unavailable")
}

// Exercise the real first-execution defer, including PreserveWorkspace set by
// drain. It must remove the container without deleting the bind-mounted state.
func TestDockerAgentRemovesContainerWhenWorkspaceRetained(t *testing.T) {
	t.Setenv("RUNNER_BIND_ADDR", "127.0.0.1")
	root := t.TempDir()
	workspace := filepath.Join(root, "task_retained", "exec_retained", "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "resume-state")
	if err := os.WriteFile(marker, []byte("session state"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDockerCLI{}
	fake.install(t)
	r := &Runner{
		cfg:      &config.Config{Runner: config.RunnerConfig{WorkspaceRoot: root}},
		runnerID: "runner-test", rpcClient: &mockEventClient{},
	}
	session := &task.TaskSession{WorkspaceDir: workspace, PreserveWorkspace: true}
	req := task.TaskRequest{TaskID: "task_retained", ExecutionID: "exec_retained", Prompt: "x", AgentImage: "test:latest", CheckpointAfterSuccess: true}
	r.runDockerAgent(context.Background(), session, req, retainedWorkspaceHarness{opencode.New()})
	// One initial stale-container cleanup, then the actual teardown. The old
	// PreserveWorkspace guard returned before the second removal.
	removed := fake.removedContainers(t)
	if len(removed) != 2 {
		t.Fatalf("container removals = %v, want initial cleanup and teardown", removed)
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "session state" {
		t.Fatalf("workspace lost: contents=%q err=%v", contents, err)
	}
	if !session.PreserveWorkspace {
		t.Fatal("workspace retention was cleared")
	}
}
