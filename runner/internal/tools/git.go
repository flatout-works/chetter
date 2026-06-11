package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Git holds MCP tool handlers for status, pull, push, and commit operations
// within a workspace directory. Credentials are injected via environment
// variables rather than stored in the agent container.
type Git struct {
	BaseDir    string
	SSHKeyPath string
	PAT        string
}

// NewGit creates a git tool handler for the given workspace directory.
func NewGit(baseDir, sshKeyPath, pat string) *Git {
	return &Git{BaseDir: baseDir, SSHKeyPath: sshKeyPath, PAT: pat}
}

func (g *Git) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.BaseDir
	if g.SSHKeyPath != "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -i "+g.SSHKeyPath+" -o StrictHostKeyChecking=no")
	}
	if g.PAT != "" {
		// Write a temporary askpass script so git can authenticate non-interactively.
		askpass, cleanup, err := writeAskpassScript(g.PAT)
		if err != nil {
			return "", fmt.Errorf("create git askpass script: %w", err)
		}
		defer cleanup()
		cmd.Env = append(cmd.Env, "GIT_ASKPASS="+askpass)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %v: %w", args, err)
	}
	return string(out), nil
}

// writeAskpassScript creates a temporary executable script that echoes the
// given PAT, suitable for GIT_ASKPASS. The caller must call the returned
// cleanup function to remove the script.
func writeAskpassScript(pat string) (string, func(), error) {
	f, err := os.CreateTemp("", "git-askpass-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", pat)
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write askpass script: %w", err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0750); err != nil {
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("chmod askpass script: %w", err)
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// Status handles git_status.
func (g *Git) Status(ctx context.Context, args map[string]any) (any, error) {
	out, err := g.git(ctx, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	return map[string]any{"clean": len(lines) == 0, "files": lines}, nil
}

// Pull handles git_pull.
func (g *Git) Pull(ctx context.Context, args map[string]any) (any, error) {
	remote := getOptString(args, "remote", "origin")
	branch := getOptString(args, "branch", "")
	var cmdArgs []string
	if branch != "" {
		cmdArgs = []string{"pull", remote, branch}
	} else {
		cmdArgs = []string{"pull", remote}
	}
	out, err := g.git(ctx, cmdArgs...)
	if err != nil {
		return nil, err
	}
	return strings.TrimSpace(out), nil
}

// Push handles git_push.
func (g *Git) Push(ctx context.Context, args map[string]any) (any, error) {
	remote := getOptString(args, "remote", "origin")
	branch := getOptString(args, "branch", "")
	cmdArgs := []string{"push", remote}
	if branch != "" {
		cmdArgs = append(cmdArgs, branch)
	}
	out, err := g.git(ctx, cmdArgs...)
	if err != nil {
		return nil, err
	}
	return strings.TrimSpace(out), nil
}

// Commit handles git_commit.
func (g *Git) Commit(ctx context.Context, args map[string]any) (any, error) {
	message, err := getString(args, "message")
	if err != nil {
		return nil, err
	}
	all := getOptBool(args, "all", false)
	cmdArgs := []string{"commit", "-m", message}
	if all {
		cmdArgs = append(cmdArgs, "-a")
	}
	out, err := g.git(ctx, cmdArgs...)
	if err != nil {
		return nil, err
	}
	return strings.TrimSpace(out), nil
}
