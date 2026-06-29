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
		{"alias", "set", "unsafe", "api repos/owner/repo/issues"},
		{"unsafe", "123"},
		{"config", "set", "editor", "vim"},
		{"extension", "exec", "unsafe"},
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
		{"config", "get", "git_protocol"},
		{"extension", "list"},
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

func TestGHWrapperIgnoresWorkspaceConfigAliases(t *testing.T) {
	maliciousConfig := t.TempDir()
	out, err := runGHWrapperWithEnv(t, []string{"GH_CONFIG_DIR=" + maliciousConfig}, "--repo", "owner/repo", "pr", "view", "123")
	if err != nil {
		t.Fatalf("expected read command to pass through: %v\n%s", err, out)
	}
	configDir := valueFromOutput(out, "GH_CONFIG_DIR=")
	if configDir == "" {
		t.Fatalf("fake gh output missing GH_CONFIG_DIR:\n%s", out)
	}
	if configDir == maliciousConfig {
		t.Fatalf("wrapper forwarded caller-controlled GH_CONFIG_DIR %q", maliciousConfig)
	}
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary GH_CONFIG_DIR still exists or stat failed unexpectedly: %v", err)
	}
}

func runGHWrapper(t *testing.T, args ...string) (string, error) {
	return runGHWrapperWithEnv(t, nil, args...)
}

func runGHWrapperWithEnv(t *testing.T, extraEnv []string, args ...string) (string, error) {
	t.Helper()
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "gh-real")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho REAL \"$@\"\necho GH_CONFIG_DIR=\"$GH_CONFIG_DIR\"\n"), 0755); err != nil {
		t.Fatalf("write fake gh-real: %v", err)
	}
	cmd := exec.Command("sh", append([]string{filepath.Join("..", "scripts", "gh")}, args...)...)
	cmd.Dir = filepath.Join("..", "scripts")
	cmd.Env = append(os.Environ(), append(extraEnv, "CHETTER_GH_REAL="+fake)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func valueFromOutput(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}
