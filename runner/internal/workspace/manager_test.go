package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateNewWorkspace(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root)

	dir, err := mgr.Create("task-1", "exec-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("workspace not created: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("dir = %q, want absolute path", dir)
	}
}

func TestCreateRemovesStaleWorkspace(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root)

	// Create a stale workspace with a file inside.
	oldDir := filepath.Join(root, "task-1", "exec-1", "workspace")
	if err := os.MkdirAll(oldDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "stale.txt"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	dir, err := mgr.Create("task-1", "exec-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The stale file should be gone.
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); err == nil {
		t.Error("stale file not removed")
	}
}

func TestDestroy(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root)

	_, err := mgr.Create("task-1", "exec-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.Destroy("task-1", "exec-1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Directory should be gone.
	if _, err := os.Stat(filepath.Join(root, "task-1")); err == nil {
		t.Error("task directory not removed")
	}
}

func TestSocketPathLength(t *testing.T) {
	mgr := NewManager("/var/lib/runner")

	// Socket paths must stay under 108 chars (Unix domain socket limit).
	path := mgr.SocketPath("very-long-task-id-1234567890")
	if len(path) > 108 {
		t.Errorf("socket path too long: %d chars: %s", len(path), path)
	}

	// Short task ID produces a simple path.
	short := mgr.SocketPath("a")
	if short != "/tmp/chetter-a.sock" {
		t.Errorf("short task socket = %q", short)
	}
}

func TestSocketPathShortensLongIDs(t *testing.T) {
	mgr := NewManager("/tmp")

	// A task ID longer than 12 chars should use only the last 12.
	long := "abc123def456ghi789jkl"
	path := mgr.SocketPath(long)
	if len(path) > len("/tmp/chetter-")+12+len(".sock")+1 {
		t.Errorf("socket path not shortened: %s", path)
	}
}

func TestCreateDifferentTasks(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root)

	d1, err := mgr.Create("task-a", "exec-a")
	if err != nil {
		t.Fatal(err)
	}
	d2, err := mgr.Create("task-b", "exec-b")
	if err != nil {
		t.Fatal(err)
	}

	if d1 == d2 {
		t.Error("different tasks got same workspace dir")
	}
}

func TestDestroyOnlyRemovesOneExecution(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root)

	first, err := mgr.Create("task-a", "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := mgr.Create("task-a", "exec-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "first.txt"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "second.txt"), []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Destroy("task-a", "exec-1"); err != nil {
		t.Fatalf("Destroy first execution: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("second execution removed with first: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "second.txt")); err != nil {
		t.Fatalf("second execution contents missing: %v", err)
	}
}

func TestDestroyNonexistent(t *testing.T) {
	mgr := NewManager(t.TempDir())

	// Destroying a nonexistent task should not error.
	err := mgr.Destroy("nonexistent", "exec-none")
	if err != nil {
		t.Fatalf("Destroy nonexistent: %v", err)
	}
}

func TestWorkspaceIDsRejectTraversal(t *testing.T) {
	m := NewManager(t.TempDir())
	for _, tc := range []struct{ taskID, executionID string }{
		{"../task", "exec_1"},
		{"task_1", "../exec"},
		{"task_1", ""},
	} {
		if _, err := m.Create(tc.taskID, tc.executionID); err == nil {
			t.Fatalf("Create(%q, %q) accepted invalid IDs", tc.taskID, tc.executionID)
		}
		if err := m.Destroy(tc.taskID, tc.executionID); err == nil {
			t.Fatalf("Destroy(%q, %q) accepted invalid IDs", tc.taskID, tc.executionID)
		}
	}
}
