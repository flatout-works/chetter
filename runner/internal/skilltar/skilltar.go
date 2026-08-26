// Package skilltar extracts gzipped tar archives of skill definitions into a
// target directory, shared by harnesses that materialize Git-backed skill
// definitions into a workspace.
package skilltar

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extract unpacks a gzipped tar archive into destDir. Entries that would
// escape destDir are rejected.
func Extract(data []byte, destDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	destPrefix := filepath.Clean(destDir) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		path := filepath.Join(destDir, filepath.Clean(hdr.Name))
		if !strings.HasPrefix(path, destPrefix) {
			return fmt.Errorf("tar entry escapes dest dir: %s", hdr.Name)
		}
		if hdr.Size == 0 && hdr.Name == "" || strings.HasSuffix(hdr.Name, "/") {
			if err := os.MkdirAll(path, 0750); err != nil {
				return fmt.Errorf("mkdir %s: %w", path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", path, err)
		}
		content, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
