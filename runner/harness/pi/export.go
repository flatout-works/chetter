package pi

import (
	"errors"
	"os"
	"path/filepath"
)

const sessionExportPath = ".pi/session-export.md"

func readSessionExport(wsDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(wsDir, sessionExportPath))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
