package scripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGHWrapperBlocksWriteCommands(t *testing.T) {
	tests := [][]string{
		{"--repo", "owner/repo", "pr", "review", "123", "--comment", "--body", "blocked"},
		{"--repo=owner/repo", "pr", "review", "123", "--comment", "--body", "blocked"},
		{"-R", "owner/repo", "issue", "comment", "123", "--body", "blocked"},
		{"-Rowner/repo", "issue", "comment", "123", "--body", "blocked"},
		{"--hostname", "github.com", "api", "repos/owner/repo/issues"},
		{"pr", "--repo", "owner/repo", "review", "123", "--comment", "--body", "blocked"},
		{"--repo", "owner/repo", "pr", "merge", "123", "--squash"},
		{"pr", "--repo", "owner/repo", "merge", "123", "--squash"},
		{"-R", "owner/repo", "issue", "close", "123"},
		{"release", "delete", "v1.0.0", "--yes"},
		{"workflow", "run", "ci.yml"},
		{"repo", "delete", "owner/repo", "--yes"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			out, err := runGHWrapper(t, args...)
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("expected wrapper to block with exit error, got err=%v out=%s", err, out)
			}
			if exitErr.ExitCode() != 64 {
				t.Fatalf("exit code = %d, want 64; out=%s", exitErr.ExitCode(), out)
			}
			if !strings.Contains(out, "Chetter-managed GitHub writes") {
				t.Fatalf("block output missing guidance:\n%s", out)
			}
		})
	}
}

func TestGHWrapperAllowsReadCommandsWithGlobalFlags(t *testing.T) {
	tests := [][]string{
		{"--repo", "owner/repo", "pr", "view", "123"},
		{"pr", "--repo", "owner/repo", "checks", "123"},
		{"issue", "view", "123"},
		{"release", "view", "v1.0.0"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			out, err := runGHWrapper(t, args...)
			if err != nil {
				t.Fatalf("expected read command to pass through: %v\n%s", err, out)
			}
			want := "REAL " + strings.Join(args, " ")
			if !strings.Contains(out, want) {
				t.Fatalf("unexpected fake gh output: %s; want %s", out, want)
			}
		})
	}
}

func runGHWrapper(t *testing.T, args ...string) (string, error) {
	t.Helper()
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "gh-real")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho REAL \"$@\"\n"), 0755); err != nil {
		t.Fatalf("write fake gh-real: %v", err)
	}
	cmd := exec.Command("sh", append([]string{filepath.Join("..", "scripts", "gh")}, args...)...)
	cmd.Dir = filepath.Join("..", "scripts")
	cmd.Env = append(os.Environ(), "CHETTER_GH_REAL="+fake)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
