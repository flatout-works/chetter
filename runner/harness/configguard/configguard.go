package configguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Protect keeps runner-generated harness configs out of commits made from the
// temporary task clone. Tracked files are hidden in the clone's index; new
// files are added to the clone-local exclude file.
func Protect(wsDir string, paths ...string) error {
	gitDir := filepath.Join(wsDir, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat task git directory: %w", err)
	}
	if !info.IsDir() {
		return nil
	}

	excludePath := filepath.Join(gitDir, "info", "exclude")
	existing, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read task git exclude: %w", err)
	}
	excluded := string(existing)
	changed := false
	for _, path := range paths {
		rel, err := filepath.Rel(wsDir, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generated config path %q is outside task workspace", path)
		}
		rel = filepath.ToSlash(rel)
		out, err := exec.Command("git", "-C", wsDir, "ls-files", "--cached", "--", rel).Output()
		if err != nil {
			return fmt.Errorf("check generated config %s in task git index: %w", rel, err)
		}
		if strings.TrimSpace(string(out)) != "" {
			if out, err := exec.Command("git", "-C", wsDir, "update-index", "--assume-unchanged", "--", rel).CombinedOutput(); err != nil {
				return fmt.Errorf("protect tracked generated config %s: %w: %s", rel, err, strings.TrimSpace(string(out)))
			}
			continue
		}

		entry := "/" + rel
		if excludeContains(excluded, entry) {
			continue
		}
		if excluded != "" && !strings.HasSuffix(excluded, "\n") {
			excluded += "\n"
		}
		excluded += entry + "\n"
		changed = true
	}
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0750); err != nil {
		return fmt.Errorf("create task git info directory: %w", err)
	}
	if err := os.WriteFile(excludePath, []byte(excluded), 0600); err != nil {
		return fmt.Errorf("write task git exclude: %w", err)
	}
	return nil
}

func excludeContains(content, entry string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == entry {
			return true
		}
	}
	return false
}
