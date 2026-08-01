package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFile(t *testing.T) {
	root := t.TempDir()
	if err := WriteFile(root, "recovery/context.md", []byte("transcript")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "recovery", "context.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "transcript" {
		t.Fatalf("content = %q, want transcript", got)
	}
}

func TestWriteFileRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "absolute.md")
	tests := []string{
		"",
		".",
		"..",
		"../outside.md",
		"nested/../../outside.md",
		`nested\..\..\outside.md`,
		absolute,
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			if err := WriteFile(root, path, []byte("unsafe")); err == nil {
				t.Fatalf("WriteFile(%q) accepted unsafe path", path)
			}
		})
	}
}

func TestWriteFileRejectsSymlinkParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(root, "linked/escaped.md", []byte("unsafe")); err == nil {
		t.Fatal("WriteFile accepted a symlink parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped.md")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestWriteFileRejectsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "context.md")); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(root, "context.md", []byte("unsafe")); err == nil {
		t.Fatal("WriteFile accepted a symlink target")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("outside file was modified: %q", got)
	}
}
