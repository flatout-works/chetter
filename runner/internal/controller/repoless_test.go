package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/task"
	"github.com/flatout-works/chetter/runner/internal/workspace"
)

// Stop at config generation, before starting a sandbox or calling a provider.
// Reaching here proves a real diagnostics run passed shared preparation.
type repolessProbeHarness struct {
	harness.Harness
	name       string
	configured bool
}

func (h *repolessProbeHarness) Name() string { return h.name }
func (h *repolessProbeHarness) GenerateConfig(ws, runnerURL, chetterURL, token string, req task.TaskRequest, local bool) error {
	if _, err := os.Stat(filepath.Join(ws, ".chetter-git-askpass")); err != nil {
		return err
	}
	if runnerURL == "" || req.RunnerMCPToken == "" || req.SelfTestNonce == "" {
		return errors.New("diagnostics MCP setup missing")
	}
	h.configured = true
	return errors.New("probe reached harness config")
}
func TestRunDiagnosticsWithoutRepositoryReachesEveryHarness(t *testing.T) {
	for _, name := range []string{"opencode", "claude-code", "pi", "codewhale", "codex", "niffler"} {
		t.Run(name, func(t *testing.T) {
			client := newRecordingReportClient(0, 0)
			r := newReportTestRunner(t, client, 0)
			r.cfg.Execution.Backend = "local"
			r.wsManager = workspace.NewManager(t.TempDir())
			r.sem = make(chan struct{}, 1)
			r.sem <- struct{}{}
			h := &repolessProbeHarness{name: name}
			r.harnessFactory = func(string) harness.Harness { return h }
			req := task.TaskRequest{TaskID: "task_probe", ExecutionID: "exec_probe", ClaimID: "claim_probe", Harness: name, TimeoutSec: 5, SelfTestNonce: "nonce_probe", SelfTestCheck: "harness:" + name, GitAuthorName: "Test Bot", GitAuthorEmail: "bot@example.com"}
			r.runTask(req)
			if !h.configured {
				t.Fatal("diagnostics failed before harness configuration")
			}
			events := client.terminalEvents()
			if len(events) != 1 || !strings.Contains(events[0].Error, "probe reached harness config") {
				t.Fatalf("terminal events: %+v", events)
			}
			if len(r.tasks) != 0 || len(r.sem) != 0 {
				t.Fatal("execution state not cleaned up")
			}
			if _, err := os.Stat(filepath.Join(r.wsManager.Root, req.TaskID, req.ExecutionID)); !os.IsNotExist(err) {
				t.Fatalf("workspace cleanup: %v", err)
			}
		})
	}
}

func TestSplitTaskRepositoriesEmpty(t *testing.T) {
	for _, repos := range [][]task.RepoRef{nil, {}} {
		primary, secondary := splitTaskRepositories(repos)
		if primary != (task.RepoRef{}) || len(secondary) != 0 {
			t.Fatalf("empty set split into %+v, %+v", primary, secondary)
		}
	}
}

// All diagnostics harnesses omit git_url/repos. The shared preparation path
// must still write askpass and enforce the existing resolved-identity policy.
func TestPrepareGitWorkspacesWithoutRepository(t *testing.T) {
	r := &Runner{}
	for _, name := range []string{"opencode", "claude-code", "pi", "codewhale", "codex", "niffler"} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			req := task.TaskRequest{Harness: name, GitAuthorName: "Test Bot", GitAuthorEmail: "bot@example.com"}
			if err := r.prepareGitWorkspaces(context.Background(), ws, req); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(ws, ".chetter-git-askpass")
			st, err := os.Stat(helper)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0700 {
				t.Fatalf("askpass mode %o", st.Mode().Perm())
			}
			if err = r.prepareGitWorkspaces(context.Background(), ws, task.TaskRequest{Harness: name}); err == nil || !strings.Contains(err.Error(), "resolved Git identity") {
				t.Fatalf("identity policy changed: %v", err)
			}
		})
	}
}
