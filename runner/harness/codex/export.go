package codex

import (
	"fmt"
	"os"
	"path/filepath"
)

func sessionExportPath(wsDir, sessionID string) string {
	return filepath.Join(wsDir, ".codex", "session-"+sessionID+".md")
}

func readSessionExport(wsDir, sessionID string) (string, error) {
	data, err := os.ReadFile(sessionExportPath(wsDir, sessionID))
	if err != nil {
		return "", fmt.Errorf("read Codex session export: %w", err)
	}
	return string(data), nil
}
