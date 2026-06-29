package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "config")); err != nil {
		t.Fatalf("symlink parent: %v", err)
	}

	err := WriteFile(root, "config/tool/settings.json", []byte("{}"), 0644)
	if err == nil {
		t.Fatal("expected symlink parent rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "tool", "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file exists or stat failed unexpectedly: %v", err)
	}
}

func TestWriteFileRejectsSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(outside, []byte("original"), 0644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "settings.json")); err != nil {
		t.Fatalf("symlink target: %v", err)
	}

	err := WriteFile(root, "settings.json", []byte("modified"), 0644)
	if err == nil {
		t.Fatal("expected symlink target rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want symlink rejection", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("outside file was modified: %q", string(data))
	}
}

func TestRemoveAllRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	nodeModules := filepath.Join(outside, "node_modules")
	if err := os.Mkdir(nodeModules, 0750); err != nil {
		t.Fatalf("create outside node_modules: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".opencode")); err != nil {
		t.Fatalf("symlink parent: %v", err)
	}

	err := RemoveAll(root, ".opencode/node_modules")
	if err == nil {
		t.Fatal("expected symlink parent rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want symlink rejection", err)
	}
	if _, err := os.Stat(nodeModules); err != nil {
		t.Fatalf("outside node_modules was removed or inaccessible: %v", err)
	}
}

func TestRemoveAllMissingParentIsNoop(t *testing.T) {
	root := t.TempDir()
	if err := RemoveAll(root, ".opencode/node_modules"); err != nil {
		t.Fatalf("RemoveAll missing parent: %v", err)
	}
}
