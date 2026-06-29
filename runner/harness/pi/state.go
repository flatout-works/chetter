package pi

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/flatout-works/chetter/runner/internal/safefs"
)

func copyPiState(wsDir string) {
	copyFirstExisting("pi auth state", wsDir, ".pi/agent/auth.json", candidatePiAuthPaths())
}

func candidatePiAuthPaths() []string {
	home := os.Getenv("HOME")
	return []string{
		filepath.Join(home, ".pi", "agent", "auth.json"),
	}
}

func copyFirstExisting(label, wsDir, relDst string, candidates []string) {
	for _, src := range candidates {
		if _, err := os.Stat(src); err == nil {
			data, err := os.ReadFile(src)
			if err != nil {
				slog.Warn("copy state read warning", "label", label, "src", src, "err", err)
				continue
			}
			if err := safefs.WriteFile(wsDir, relDst, data, 0600); err != nil {
				slog.Warn("copy state write warning", "label", label, "dst", filepath.Join(wsDir, relDst), "err", err)
				continue
			}
			slog.Info("copied state", "label", label, "src", src, "dst", filepath.Join(wsDir, relDst), "bytes", len(data))
			return
		}
	}
	slog.Info("no state file found for copy", "label", label)
}
