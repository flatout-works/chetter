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
	parent := filepath.Join(m.Root, taskID, executionID)
	dir := filepath.Join(parent, "workspace")

	// Remove any stale workspace
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("remove stale workspace: %w", err)
	}
	if err := os.RemoveAll(parent); err != nil {
		return "", fmt.Errorf("remove stale execution directory: %w", err)
	}

	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("mkdir workspace: %w", err)
	}
	return dir, nil
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
	dir := filepath.Join(m.Root, taskID, executionID)
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(path, 0750)
		}
		return nil
	})
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if err := os.Remove(parent); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
		return err
	}
	return nil
}

func validateWorkspaceID(taskID, executionID string) error {
	for name, value := range map[string]string{"task_id": taskID, "execution_id": executionID} {
		if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
			return fmt.Errorf("invalid %s %q", name, value)
		}
	}
	return nil
}
