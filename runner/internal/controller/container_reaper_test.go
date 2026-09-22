package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
)

// reapTestContainer is one container the fake docker CLI should report.
type reapTestContainer struct {
	Name        string
	RunnerID    string
	TaskID      string
	ExecutionID string
	Workspace   string
	Created     time.Time
	Checkpoints []string
}

// fakeDockerCLI installs a fake "docker" executable that answers ps, inspect,
// checkpoint ls, and rm -f from an in-memory container list. It lets the
// reaper's decision logic be tested without a Docker daemon.
type fakeDockerCLI struct {
	mu         sync.Mutex
	containers []reapTestContainer
	removed    []string
	rmCalls    int
	// rmFailures makes the first N rm invocations fail, to exercise retries.
	rmFailures int
}

func (f *fakeDockerCLI) install(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
ps)
  # -a --filter name=... --format ...
  IFS='	'
  while read -r name runner task exec ws; do
    [ -z "$name" ] && continue
    printf '%s\t%s\t%s\t%s\t%s\n' "$name" "$runner" "$task" "$exec" "$ws"
  done < "$FAKE_DOCKER_STATE/containers"
  ;;
inspect)
  shift
  # -f template, then one or more container names
  [ "$1" = "-f" ] && shift
  tmpl="$1"
  shift
  for name in "$@"; do
    while IFS='	' read -r cname ccreated; do
      if [ "$cname" = "$name" ]; then
        printf '/%s\t%s\n' "$cname" "$ccreated"
      fi
    done < "$FAKE_DOCKER_STATE/created"
  done
  ;;
checkpoint)
  # checkpoint ls <name>
  shift
  [ "$1" = "ls" ] && shift
  name="$1"
  printf 'CHECKPOINT NAME\n'
  if [ -f "$FAKE_DOCKER_STATE/checkpoints/$name" ]; then
    cat "$FAKE_DOCKER_STATE/checkpoints/$name"
  fi
  ;;
rm)
  # rm -f <name>
  shift
  [ "$1" = "-f" ] && shift
  name="$1"
  echo "$name" >> "$FAKE_DOCKER_STATE/rm_calls"
  if [ -f "$FAKE_DOCKER_STATE/rm_failures" ]; then
    n=$(cat "$FAKE_DOCKER_STATE/rm_failures")
    if [ "$n" -gt 0 ]; then
      echo $((n - 1)) > "$FAKE_DOCKER_STATE/rm_failures"
      echo "Error response from daemon: cannot remove, container is busy" >&2
      exit 1
    fi
  fi
  # Remove the container from the state so a second pass sees it gone.
  grep -v "^$name	" "$FAKE_DOCKER_STATE/containers" > "$FAKE_DOCKER_STATE/containers.tmp" 2>/dev/null
  mv "$FAKE_DOCKER_STATE/containers.tmp" "$FAKE_DOCKER_STATE/containers"
  echo "$name" >> "$FAKE_DOCKER_STATE/removed"
  ;;
*)
  exit 0
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_STATE", f.stateDir(t))
}

// stateDir materializes the fake CLI's backing files from the container list.
func (f *fakeDockerCLI) stateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	f.mu.Lock()
	defer f.mu.Unlock()
	var containers, created strings.Builder
	for _, c := range f.containers {
		fmt.Fprintf(&containers, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.RunnerID, c.TaskID, c.ExecutionID, c.Workspace)
		fmt.Fprintf(&created, "%s\t%s\n", c.Name, c.Created.UTC().Format(time.RFC3339Nano))
	}
	if err := os.WriteFile(filepath.Join(dir, "containers"), []byte(containers.String()), 0o644); err != nil {
		t.Fatalf("write containers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "created"), []byte(created.String()), 0o644); err != nil {
		t.Fatalf("write created: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "checkpoints"), 0o755); err != nil {
		t.Fatalf("mkdir checkpoints: %v", err)
	}
	for _, c := range f.containers {
		if len(c.Checkpoints) == 0 {
			continue
		}
		body := strings.Join(c.Checkpoints, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, "checkpoints", c.Name), []byte(body), 0o644); err != nil {
			t.Fatalf("write checkpoint: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "rm_failures"), []byte(fmt.Sprintf("%d\n", f.rmFailures)), 0o644); err != nil {
		t.Fatalf("write rm_failures: %v", err)
	}
	return dir
}

// removedContainers returns the container names the fake CLI was asked to
// remove, in order. It reads from the fake state files rather than the struct
// because the fake CLI runs as a separate process.
func (f *fakeDockerCLI) removedContainers(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "removed"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read removed: %v", err)
	}
	return strings.Fields(string(data))
}

// stubPruneClient records PruneWorkspaces calls and returns a configurable
// verdict. Embedding runnerRPCClient keeps the stub minimal.
type stubPruneClient struct {
	runnerRPCClient
	mu       sync.Mutex
	requests []*runnerv1.PruneWorkspacesRequest
	safe     map[string]bool
	err      error
}

func (s *stubPruneClient) PruneWorkspaces(_ context.Context, req *connect.Request[runnerv1.PruneWorkspacesRequest]) (*connect.Response[runnerv1.PruneWorkspacesResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req.Msg)
	if s.err != nil {
		return nil, s.err
	}
	resp := &runnerv1.PruneWorkspacesResponse{}
	for _, candidate := range req.Msg.Candidates {
		if s.safe[candidate.TaskId+"\x00"+candidate.ExecutionId] {
			resp.SafeToDelete = append(resp.SafeToDelete, &runnerv1.WorkspaceKey{
				TaskId:      candidate.TaskId,
				ExecutionId: candidate.ExecutionId,
			})
		}
	}
	return connect.NewResponse(resp), nil
}

func (s *stubPruneClient) calls() []*runnerv1.PruneWorkspacesRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*runnerv1.PruneWorkspacesRequest(nil), s.requests...)
}

func newReaperTestRunner(client runnerRPCClient) *Runner {
	return &Runner{
		cfg:       &config.Config{Execution: config.ExecutionConfig{Backend: "docker"}},
		runnerID:  "runner-under-test",
		rpcClient: client,
		runCtx:    context.Background(),
	}
}

func TestSweepOrphanedTaskContainersRemovesControlPlaneConfirmedLeaks(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{{
		Name: "chetter-task-task_leaked-exec_1", RunnerID: "runner-crashed",
		TaskID: "task_leaked", ExecutionID: "exec_1",
		Workspace: "/var/lib/runner/task_leaked/exec_1/workspace",
		Created:   time.Now().Add(-time.Hour),
	}}}
	fake.install(t)
	client := &stubPruneClient{safe: map[string]bool{"task_leaked\x00exec_1": true}}
	r := newReaperTestRunner(client)

	r.sweepOrphanedTaskContainers(context.Background())

	removed := fake.removedContainers(t)
	if len(removed) != 1 || removed[0] != "chetter-task-task_leaked-exec_1" {
		t.Fatalf("removed containers = %v, want the leaked container", removed)
	}
	calls := client.calls()
	if len(calls) != 1 {
		t.Fatalf("PruneWorkspaces calls = %d, want 1", len(calls))
	}
	if !calls[0].ContainerScope {
		t.Fatalf("container_scope = false, want true for the ownership-agnostic reaper")
	}
	if len(calls[0].Candidates) != 1 || calls[0].Candidates[0].WorkspacePath == "" {
		t.Fatalf("candidates = %+v, want one candidate carrying its workspace path", calls[0].Candidates)
	}
}

// TestSweepOrphanedTaskContainersProtectsLiveSibling is the regression test for
// the shared-daemon hazard: two runners share one Docker daemon, so a sweep
// must not remove a container the control plane still considers live even
// though it belongs to another runner.
func TestSweepOrphanedTaskContainersProtectsLiveSibling(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{
		{
			Name: "chetter-task-task_live-exec_live", RunnerID: "runner-sibling",
			TaskID: "task_live", ExecutionID: "exec_live",
			Workspace: "/var/lib/runner/task_live/exec_live/workspace",
			Created:   time.Now().Add(-time.Hour),
		},
		{
			Name: "chetter-task-task_dead-exec_dead", RunnerID: "runner-sibling",
			TaskID: "task_dead", ExecutionID: "exec_dead",
			Workspace: "/var/lib/runner/task_dead/exec_dead/workspace",
			Created:   time.Now().Add(-time.Hour),
		},
	}}
	fake.install(t)
	client := &stubPruneClient{safe: map[string]bool{"task_dead\x00exec_dead": true}}
	r := newReaperTestRunner(client)

	r.sweepOrphanedTaskContainers(context.Background())

	removed := fake.removedContainers(t)
	if len(removed) != 1 || removed[0] != "chetter-task-task_dead-exec_dead" {
		t.Fatalf("removed containers = %v, want only the dead sibling's container", removed)
	}
}

// TestSweepOrphanedTaskContainersKeepsRecentAndActiveContainers verifies the
// two local guards: the grace period (so the reaper never races inline
// teardown) and the active-session check.
func TestSweepOrphanedTaskContainersKeepsRecentAndActiveContainers(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{
		{
			Name: "chetter-task-task_young-exec_young", RunnerID: "runner-under-test",
			TaskID: "task_young", ExecutionID: "exec_young",
			Workspace: "/var/lib/runner/task_young/exec_young/workspace",
			Created:   time.Now().Add(-time.Minute),
		},
		{
			Name: "chetter-task-task_active-exec_active", RunnerID: "runner-under-test",
			TaskID: "task_active", ExecutionID: "exec_active",
			Workspace: "/var/lib/runner/task_active/exec_active/workspace",
			Created:   time.Now().Add(-time.Hour),
		},
	}}
	fake.install(t)
	client := &stubPruneClient{safe: map[string]bool{
		"task_young\x00exec_young":   true,
		"task_active\x00exec_active": true,
	}}
	r := newReaperTestRunner(client)
	r.tasks = map[string]*task.TaskSession{"exec_active": {TaskID: "task_active", ExecutionID: "exec_active"}}

	r.sweepOrphanedTaskContainers(context.Background())

	if removed := fake.removedContainers(t); len(removed) != 0 {
		t.Fatalf("removed containers = %v, want none (young + active are protected)", removed)
	}
	if calls := client.calls(); len(calls) != 0 {
		t.Fatalf("PruneWorkspaces calls = %d, want 0 when nothing is eligible", len(calls))
	}
}

func TestSweepOrphanedTaskContainersFailsClosedWithoutVerdict(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{{
		Name: "chetter-task-task_x-exec_x", RunnerID: "runner-crashed",
		TaskID: "task_x", ExecutionID: "exec_x",
		Workspace: "/var/lib/runner/task_x/exec_x/workspace",
		Created:   time.Now().Add(-time.Hour),
	}}}
	fake.install(t)
	client := &stubPruneClient{err: fmt.Errorf("rpc unavailable")}
	r := newReaperTestRunner(client)

	r.sweepOrphanedTaskContainers(context.Background())

	if removed := fake.removedContainers(t); len(removed) != 0 {
		t.Fatalf("removed containers = %v, want none when the verdict is unavailable", removed)
	}
}

// TestSweepOrphanedTaskContainersKeepsCheckpointedContainer verifies the
// local checkpoint guard: docker is authoritative about checkpoint ownership,
// so a container holding one is never force-removed even if the control plane
// reported it safe.
func TestSweepOrphanedTaskContainersKeepsCheckpointedContainer(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{{
		Name: "chetter-task-task_chk-exec_chk", RunnerID: "runner-crashed",
		TaskID: "task_chk", ExecutionID: "exec_chk",
		Workspace:   "/var/lib/runner/task_chk/exec_chk/workspace",
		Created:     time.Now().Add(-time.Hour),
		Checkpoints: []string{"chetter-checkpoint"},
	}}}
	fake.install(t)
	client := &stubPruneClient{safe: map[string]bool{"task_chk\x00exec_chk": true}}
	r := newReaperTestRunner(client)

	r.sweepOrphanedTaskContainers(context.Background())

	if removed := fake.removedContainers(t); len(removed) != 0 {
		t.Fatalf("removed containers = %v, want none for a checkpointed container", removed)
	}
}

func TestRemoveTaskContainerRetriesUntilSuccess(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{{
		Name: "chetter-task-task_r-exec_r", RunnerID: "runner-under-test",
		TaskID: "task_r", ExecutionID: "exec_r",
		Workspace: "/var/lib/runner/task_r/exec_r/workspace",
		Created:   time.Now().Add(-time.Hour),
	}}, rmFailures: 1}
	fake.install(t)

	removeTaskContainer("chetter-task-task_r-exec_r")

	removed := fake.removedContainers(t)
	if len(removed) != 1 {
		t.Fatalf("removed containers = %v, want the container removed on retry", removed)
	}
}

func TestListTaskContainersParsesLabels(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{{
		Name: "chetter-task-task_p-exec_p", RunnerID: "runner-r",
		TaskID: "task_p", ExecutionID: "exec_p",
		Workspace: "/var/lib/runner/task_p/exec_p/workspace",
		Created:   time.Now(),
	}}}
	fake.install(t)
	r := newReaperTestRunner(nil)

	candidates, err := r.listTaskContainers(context.Background())
	if err != nil {
		t.Fatalf("listTaskContainers: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want 1", candidates)
	}
	got := candidates[0]
	if got.Name != "chetter-task-task_p-exec_p" || got.RunnerID != "runner-r" ||
		got.TaskID != "task_p" || got.ExecutionID != "exec_p" ||
		got.WorkspacePath != "/var/lib/runner/task_p/exec_p/workspace" {
		t.Fatalf("candidate = %+v, want all labels parsed", got)
	}
}

func TestContainerAgesAndReapTimeoutConstants(t *testing.T) {
	if taskContainerMinAge <= containerCleanupTimeout {
		t.Fatalf("taskContainerMinAge (%v) must exceed containerCleanupTimeout (%v) so the reaper cannot race inline teardown",
			taskContainerMinAge, containerCleanupTimeout)
	}
	if containerCleanupAttempts < 2 {
		t.Fatalf("containerCleanupAttempts = %d, want at least 2 so a context-killed removal is retried", containerCleanupAttempts)
	}
}

// TestSweepOrphanedTaskContainersHandlesPreLabelContainers covers the upgrade
// path: containers created before the chetter.workspace_path label existed
// (including every container leaked during the 2026-09-19 wowbagger incident)
// carry no workspace path. The reaper must still submit them for a verdict
// rather than skipping them forever.
func TestSweepOrphanedTaskContainersHandlesPreLabelContainers(t *testing.T) {
	fake := &fakeDockerCLI{containers: []reapTestContainer{{
		Name: "chetter-task-task_old-exec_old", RunnerID: "runner-crashed",
		TaskID: "task_old", ExecutionID: "exec_old",
		Workspace: "", // no chetter.workspace_path label
		Created:   time.Now().Add(-24 * time.Hour),
	}}}
	fake.install(t)
	client := &stubPruneClient{safe: map[string]bool{"task_old\x00exec_old": true}}
	r := newReaperTestRunner(client)

	r.sweepOrphanedTaskContainers(context.Background())

	if removed := fake.removedContainers(t); len(removed) != 1 {
		t.Fatalf("removed containers = %v, want the pre-label container removed", removed)
	}
	calls := client.calls()
	if len(calls) != 1 {
		t.Fatalf("PruneWorkspaces calls = %d, want 1", len(calls))
	}
	if got := calls[0].Candidates[0].WorkspacePath; got != "" {
		t.Fatalf("workspace path = %q, want empty for a pre-label container", got)
	}
}
