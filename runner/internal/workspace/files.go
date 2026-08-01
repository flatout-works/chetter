package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFile writes a file beneath root without following symlinks in the
// relative path. It rejects absolute paths and traversal components so task
// metadata cannot write outside its workspace.
func WriteFile(root, relativePath string, content []byte) error {
	clean, err := cleanRelativePath(relativePath)
	if err != nil {
		return err
	}

	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat workspace root: %w", err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("workspace root %q is not a directory", root)
	}

	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workspace file path %q escapes workspace", relativePath)
	}

	if err := ensureDirectoryPath(root, filepath.Dir(clean)); err != nil {
		return fmt.Errorf("prepare workspace file %q: %w", relativePath, err)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace file path %q is a symlink", relativePath)
		}
		if info.IsDir() {
			return fmt.Errorf("workspace file path %q is a directory", relativePath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect workspace file %q: %w", relativePath, err)
	}

	if err := os.WriteFile(target, content, 0644); err != nil {
		return fmt.Errorf("write workspace file %q: %w", relativePath, err)
	}
	return nil
}

func cleanRelativePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("workspace file path %q must be relative", path)
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return "", fmt.Errorf("workspace file path %q contains traversal", path)
		}
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid workspace file path %q", path)
	}
	return clean, nil
}

func ensureDirectoryPath(root, relativeDir string) error {
	if relativeDir == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(relativeDir, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		switch {
		case os.IsNotExist(err):
			if err := os.Mkdir(current, 0750); err != nil {
				return fmt.Errorf("create directory %q: %w", current, err)
			}
		case err != nil:
			return fmt.Errorf("inspect directory %q: %w", current, err)
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("directory %q is a symlink", current)
		case !info.IsDir():
			return fmt.Errorf("path %q is not a directory", current)
		}
	}
	return nil
}
