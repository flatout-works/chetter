package codewhale

import (
	"fmt"
	"os"
	"strings"
)

func readSessionExport(wsDir, sessionID string) (string, error) {
	entries, err := os.ReadDir(wsDir + "/.codewhale/sessions")
	if err != nil {
		return "", fmt.Errorf("read sessions dir: %w", err)
	}

	var latestDir string
	for _, entry := range entries {
		if entry.IsDir() {
			latestDir = entry.Name()
		}
	}

	if latestDir == "" {
		return "", fmt.Errorf("no codewhale session directories found")
	}

	sessionDir := wsDir + "/.codewhale/sessions/" + latestDir
	sessionFiles, err := os.ReadDir(sessionDir)
	if err != nil {
		return "", fmt.Errorf("read session dir: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("# CodeWhale Session\n\n")

	for _, sf := range sessionFiles {
		if sf.IsDir() || !strings.HasSuffix(sf.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(sessionDir + "/" + sf.Name())
		if err != nil {
			continue
		}
		sb.Write(data)
		sb.WriteString("\n")
	}

	if sb.Len() == 0 {
		return "", fmt.Errorf("no session export files in %s", sessionDir)
	}

	return sb.String(), nil
}
