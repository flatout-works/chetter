package safefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func CleanRelativePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("path must be relative")
	}
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("path must use forward slashes")
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return "", fmt.Errorf("path must stay inside workspace")
		}
	}
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay inside workspace")
	}
	return cleaned, nil
}

func EnsureDir(root, relDir string, perm os.FileMode) error {
	return ensureDir(root, relDir, perm, true)
}

func WriteFile(root, relPath string, content []byte, perm os.FileMode) error {
	relPath, err := CleanRelativePath(relPath)
	if err != nil {
		return err
	}
	if err := EnsureDir(root, filepath.Dir(relPath), 0750); err != nil {
		return err
	}
	return writeFileNoSymlink(filepath.Join(root, relPath), content, perm)
}

func ReadFile(root, relPath string) ([]byte, error) {
	relPath, err := CleanRelativePath(relPath)
	if err != nil {
		return nil, err
	}
	if err := ensureDir(root, filepath.Dir(relPath), 0, false); err != nil {
		return nil, err
	}
	return readFileNoSymlink(filepath.Join(root, relPath))
}

func RemoveAll(root, relPath string) error {
	relPath, err := CleanRelativePath(relPath)
	if err != nil {
		return err
	}
	if err := ensureDir(root, filepath.Dir(relPath), 0, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	path := filepath.Join(root, relPath)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path component %s is a symlink", path)
	}
	return os.RemoveAll(path)
}

func ensureDir(root, relDir string, perm os.FileMode, create bool) error {
	if err := requireDirectoryNoSymlink(root); err != nil {
		return err
	}
	if relDir == "." || relDir == "" {
		return nil
	}
	relDir, err := CleanRelativePath(relDir)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(filepath.ToSlash(relDir), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) || !create {
				return err
			}
			if err := os.Mkdir(current, perm); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
			if err != nil {
				return err
			}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %s is a symlink", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("path component %s is not a directory", current)
		}
	}
	return nil
}

func requireDirectoryNoSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path component %s is a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("path component %s is not a directory", path)
	}
	return nil
}

func writeFileNoSymlink(path string, content []byte, perm os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target file %s is a symlink", path)
		}
		if info.IsDir() {
			return fmt.Errorf("target file %s is a directory", path)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func readFileNoSymlink(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("target file %s is a symlink", path)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("target file %s is a directory", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
