// Package workspace manages per-task workspace directories — creating,
// cleaning up, and tracking stale directories for the runner.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

// Manager creates, cleans up, and destroys per-execution workspace directories
// under Root. It also computes socket paths staying within Unix path limits.
type Manager struct {
	Root string
}

// NewManager creates a workspace manager.
func NewManager(root string) *Manager {
	return &Manager{Root: root}
}

// Create prepares a workspace directory for one execution of a task.
// If a stale execution directory exists it is removed first.
func (m *Manager) Create(taskID, executionID string) (string, error) {
	if err := validateWorkspaceID(taskID, executionID); err != nil {
		return "", err
	}
	if err := ensureManagedTaskDir(m.Root, taskID); err != nil {
		return "", err
	}
	parent := filepath.Join(m.Root, taskID, executionID)
	dir := filepath.Join(parent, "workspace")

	if err := os.RemoveAll(parent); err != nil {
		return "", fmt.Errorf("remove stale execution directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("mkdir workspace: %w", err)
	}
	return dir, nil
}

// ResolveResumeWorkspace validates and canonicalizes a persisted workspace
// path before it is used for config writes, bind mounts, or Kubernetes
// subpaths. Resumable workspaces must have the manager-owned shape
// <root>/<taskID>/<executionID>/workspace and may not traverse symlinks.
func (m *Manager) ResolveResumeWorkspace(taskID, path string) (string, error) {
	if err := validateWorkspaceComponent("task_id", taskID); err != nil {
		return "", err
	}
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("resume workspace path %q must be absolute", path)
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("resume workspace path %q contains traversal", path)
		}
	}
	root, err := filepath.Abs(m.Root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(root, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("resume workspace path %q is outside workspace root %q", path, root)
	}
	if err := rejectSymlinkComponents(root, rel); err != nil {
		return "", fmt.Errorf("inspect resume workspace path: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return "", fmt.Errorf("resolve resume workspace path: %w", err)
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("stat resume workspace: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("resume workspace path %q is not a directory", path)
	}
	resolvedRel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", fmt.Errorf("resolve resume workspace relative path: %w", err)
	}
	parts := strings.Split(resolvedRel, string(filepath.Separator))
	if len(parts) != 3 || parts[0] != taskID || parts[2] != "workspace" {
		return "", fmt.Errorf("resume workspace path %q is not owned by task %q", path, taskID)
	}
	if err := validateWorkspaceID(taskID, parts[1]); err != nil {
		return "", fmt.Errorf("invalid resume workspace: %w", err)
	}
	return resolvedPath, nil
}

// SocketDir returns the directory for the MCP socket.
func (m *Manager) SocketDir(taskID string) string {
	return filepath.Join(m.Root, taskID)
}

// SocketPath returns the full path to the MCP Unix socket.
// Delegates to task.SocketPath for consistency with the builder.
func (m *Manager) SocketPath(taskID string) string {
	return task.SocketPath(taskID)
}

// Destroy removes a workspace and its socket.
// It chmods everything writable first because git hooks are read-only.
func (m *Manager) Destroy(taskID, executionID string) error {
	if err := validateWorkspaceID(taskID, executionID); err != nil {
		return err
	}
	if err := ensureManagedTaskDir(m.Root, taskID); err != nil {
		return err
	}
	dir := filepath.Join(m.Root, taskID, executionID)
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // chmod follows symlinks and could mutate an outside target.
		}
		if err := os.Chmod(path, 0750); err != nil {
			return err
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("make workspace writable: %w", err)
	}
	if err := removeAllWithRetry(dir); err != nil {
		return fmt.Errorf("remove workspace: %w", err)
	}
	parent := filepath.Dir(dir)
	if err := os.Remove(parent); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
		return fmt.Errorf("remove execution directory: %w", err)
	}
	return nil
}

func removeAllWithRetry(dir string) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		if attempt < 5 {
			time.Sleep(100 * time.Millisecond << attempt)
		}
	}
	return err
}

func ensureManagedTaskDir(root, taskID string) error {
	if err := os.MkdirAll(root, 0750); err != nil {
		return fmt.Errorf("create workspace root: %w", err)
	}
	taskDir := filepath.Join(root, taskID)
	info, err := os.Lstat(taskDir)
	switch {
	case os.IsNotExist(err):
		if err := os.Mkdir(taskDir, 0750); err != nil {
			return fmt.Errorf("create task workspace directory: %w", err)
		}
	case err != nil:
		return fmt.Errorf("inspect task workspace directory: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("task workspace directory %q is a symlink", taskDir)
	case !info.IsDir():
		return fmt.Errorf("task workspace path %q is not a directory", taskDir)
	}
	return nil
}

func rejectSymlinkComponents(root, relativePath string) error {
	current := root
	for _, part := range strings.Split(relativePath, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink", current)
		}
	}
	return nil
}

func validateWorkspaceComponent(name, value string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	return nil
}

func validateWorkspaceID(taskID, executionID string) error {
	if err := validateWorkspaceComponent("task_id", taskID); err != nil {
		return err
	}
	return validateWorkspaceComponent("execution_id", executionID)
}
