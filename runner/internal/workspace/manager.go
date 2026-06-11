// Package workspace manages per-task workspace directories — creating,
// cleaning up, and tracking stale directories for the runner.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/flatout-works/devfleet/runner/internal/task"
)

// Manager creates, cleans up, and destroys per-task workspace directories
// under Root. It also computes socket paths staying within Unix path limits.
type Manager struct {
	Root string
}

// NewManager creates a workspace manager.
func NewManager(root string) *Manager {
	return &Manager{Root: root}
}

// Create prepares a workspace directory for a task.
// If a stale directory exists it is removed first.
func (m *Manager) Create(taskID string) (string, error) {
	parent := filepath.Join(m.Root, taskID)
	dir := filepath.Join(parent, "workspace")

	// Remove any stale workspace
	if err := os.RemoveAll(dir); err != nil {
		// Best-effort; continue to try re-creating
	}
	if err := os.RemoveAll(parent); err != nil {
		// Best-effort
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
func (m *Manager) Destroy(taskID string) error {
	dir := filepath.Join(m.Root, taskID)
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(path, 0750)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
