package pi

import (
	"log/slog"
	"os"
	"path/filepath"
)

func copyPiState(wsDir string) {
	copyFirstExisting("pi auth state", filepath.Join(wsDir, ".pi", "agent", "auth.json"), candidatePiAuthPaths())
}

func candidatePiAuthPaths() []string {
	home := os.Getenv("HOME")
	return []string{
		filepath.Join(home, ".pi", "agent", "auth.json"),
	}
}

func copyFirstExisting(label, dst string, candidates []string) {
	for _, src := range candidates {
		if _, err := os.Stat(src); err == nil {
			if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
				slog.Warn("copy state mkdir warning", "label", label, "err", err)
				continue
			}
			data, err := os.ReadFile(src)
			if err != nil {
				slog.Warn("copy state read warning", "label", label, "src", src, "err", err)
				continue
			}
			if err := os.WriteFile(dst, data, 0600); err != nil {
				slog.Warn("copy state write warning", "label", label, "dst", dst, "err", err)
				continue
			}
			slog.Info("copied state", "label", label, "src", src, "dst", dst, "bytes", len(data))
			return
		}
	}
	slog.Info("no state file found for copy", "label", label)
}
