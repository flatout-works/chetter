//go:build !windows

package definitions

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSyncKillsGitProcessTreeOnCancellation reproduces issue #427: cancelling
// a definitions sync must kill the whole git process tree, not just the direct
// child. The fake git spawns a background child (mimicking `git pull` spawning
// `git fetch`/`git merge`); before the fix the child was orphaned and lingered
// after cancellation, eventually becoming an unreaped zombie.
func TestSyncKillsGitProcessTreeOnCancellation(t *testing.T) {
	binDir := t.TempDir()
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	script := fmt.Sprintf("#!/bin/sh\nsleep 300 &\necho $! > %q\nwait\n", childPIDFile)
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// An existing cache dir makes Sync take the `git pull` path.
	m := New("https://example.invalid/definitions.git", "main", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Sync(ctx) }()

	childPID := waitForPIDFile(t, childPIDFile)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Sync did not return after context cancellation")
	}

	waitForProcessExit(t, childPID)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for child pid file %s", path)
	return 0
}

// waitForProcessExit blocks until pid is gone or a zombie. A zombie means the
// process was killed but not yet reaped, which is acceptable: the regression
// under test is the grandchild staying alive after cancellation.
func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		alive, err := processRunning(pid)
		if err != nil {
			t.Fatalf("inspect process %d: %v", pid, err)
		}
		if !alive {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still running after cancellation; git process group was not killed", pid)
}

func processRunning(pid int) (bool, error) {
	if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
		return false, nil
	} else if err != nil {
		return false, err
	}
	data, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if readErr != nil {
		// No procfs (e.g. non-Linux): existence is the best signal available.
		return true, nil
	}
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return true, nil
	}
	return data[closeParen+2] != 'Z', nil
}
