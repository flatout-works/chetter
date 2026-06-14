// Package tools implements MCP tool handlers for workspace I/O, git
// operations, and URL fetching.
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Workspace holds MCP tool handlers for file I/O, directory listing, bash
// execution, and result reporting within a specific workspace directory.
type Workspace struct {
	BaseDir string
}

// NewWorkspace creates a workspace tool handler rooted at baseDir.
func NewWorkspace(baseDir string) *Workspace {
	return &Workspace{BaseDir: baseDir}
}

func (w *Workspace) resolve(path string) string {
	clean := filepath.Clean("/" + path)
	return filepath.Join(w.BaseDir, clean)
}

// ReadFile handles workspace_read_file.
func (w *Workspace) ReadFile(ctx context.Context, args map[string]any) (any, error) {
	path, err := getString(args, "path")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(w.resolve(path))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

// WriteFile handles workspace_write_file.
func (w *Workspace) WriteFile(ctx context.Context, args map[string]any) (any, error) {
	path, err := getString(args, "path")
	if err != nil {
		return nil, err
	}
	content, err := getString(args, "content")
	if err != nil {
		return nil, err
	}
	full := w.resolve(path)
	if err := os.MkdirAll(filepath.Dir(full), 0750); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(full, []byte(content), 0640); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}
	return "written", nil
}

// ListDirectory handles workspace_list_directory.
func (w *Workspace) ListDirectory(ctx context.Context, args map[string]any) (any, error) {
	path := getOptString(args, "path", ".")
	if path == "" {
		path = "."
	}
	entries, err := os.ReadDir(w.resolve(path))
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name()+"/")
		} else {
			names = append(names, e.Name())
		}
	}
	return strings.Join(names, "\n"), nil
}

// Bash handles workspace_bash.
func (w *Workspace) Bash(ctx context.Context, args map[string]any) (any, error) {
	command, err := getString(args, "command")
	if err != nil {
		return nil, err
	}
	timeoutSec := getOptFloat64(args, "timeout_sec", 60)
	if timeoutSec == 0 {
		timeoutSec = 60
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = w.BaseDir
	out, err := cmd.CombinedOutput()
	result := string(out)
	if err != nil {
		return result + "\n" + err.Error(), nil
	}
	return result, nil
}
