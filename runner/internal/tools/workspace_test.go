package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveKeepsPathsInsideWorkspace(t *testing.T) {
	w := NewWorkspace("/tmp/ws")

	tests := []struct {
		path string
		want string
	}{
		{path: "foo.txt", want: "/tmp/ws/foo.txt"},
		{path: "subdir/file.go", want: "/tmp/ws/subdir/file.go"},
		{path: "../outside", want: "/tmp/ws/outside"},
		{path: "../../etc/passwd", want: "/tmp/ws/etc/passwd"},
		{path: "/absolute", want: "/tmp/ws/absolute"},
	}

	for _, tc := range tests {
		got := w.resolve(tc.path)
		if got != tc.want {
			t.Errorf("resolve(%q) = %q, want %q", tc.path, got, tc.want)
		}
		if !strings.HasPrefix(got, w.BaseDir) {
			t.Errorf("resolve(%q) escaped workspace: %s", tc.path, got)
		}
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkspace(dir)
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := w.ReadFile(t.Context(), map[string]any{"path": "test.txt"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if result != "hello world" {
		t.Errorf("ReadFile = %q, want %q", result, "hello world")
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkspace(dir)

	_, err := w.WriteFile(t.Context(), map[string]any{
		"path":    "nested/output.txt",
		"content": "data",
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "nested", "output.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("content = %q, want %q", string(data), "data")
	}
}

func TestListDirectory(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkspace(dir)

	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := w.ListDirectory(t.Context(), map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}

	s := result.(string)
	if !strings.Contains(s, "a.txt") {
		t.Errorf("missing a.txt: %s", s)
	}
	if !strings.Contains(s, "sub/") {
		t.Errorf("missing sub/: %s", s)
	}
}

func TestListDirectoryDefaultsToDot(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkspace(dir)

	result, err := w.ListDirectory(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}
	if result == nil {
		t.Error("unexpected nil result")
	}
}

func TestBash(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkspace(dir)

	result, err := w.Bash(t.Context(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatalf("Bash: %v", err)
	}
	s := result.(string)
	if !strings.Contains(s, "hello") {
		t.Errorf("bash output = %q, want hello", s)
	}
}

func TestBashDefaultTimeout(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkspace(dir)

	result, err := w.Bash(t.Context(), map[string]any{"command": "true"})
	if err != nil {
		t.Fatalf("Bash: %v", err)
	}
	if result == nil {
		t.Error("unexpected nil result")
	}
}

