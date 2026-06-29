package opencode

import (
	"log/slog"
	"os"
	"os/user"
	"path/filepath"

	"github.com/flatout-works/chetter/runner/internal/safefs"
)

func candidateHomes() []string {
	homes := []string{os.Getenv("HOME")}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
		if u, err := user.Lookup(sudoUser); err == nil {
			homes = append(homes, u.HomeDir)
		} else {
			homes = append(homes, "/home/"+sudoUser)
		}
	}
	return homes
}

func readOpenCodeConfig() ([]byte, string) {
	for _, home := range candidateHomes() {
		if home == "" {
			continue
		}
		for _, path := range []string{
			home + "/.config/opencode/config.json",
			home + "/.opencode/config.json",
		} {
			data, err := os.ReadFile(path)
			if err == nil {
				return data, path
			}
		}
	}
	return []byte("{}"), "<empty>"
}

func copyFirstExisting(label, wsDir, relDst string, candidates func(string) []string) {
	for _, home := range candidateHomes() {
		for _, src := range candidates(home) {
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			if err := safefs.WriteFile(wsDir, relDst, data, 0644); err != nil {
				slog.Warn("copy warning", "label", label, "err", err)
				return
			}
			slog.Info("copied state", "label", label, "src", src, "dst", filepath.Join(wsDir, relDst), "bytes", len(data))
			return
		}
	}
	slog.Warn("copy no source found", "label", label)
}

func copyDir(src, wsDir, relDst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetRel := filepath.Join(relDst, rel)
		if d.IsDir() {
			return safefs.EnsureDir(wsDir, targetRel, 0750)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return safefs.WriteFile(wsDir, targetRel, data, 0640)
	})
}
