package skilltar

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func buildSkillTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractWritesFiles(t *testing.T) {
	dest := t.TempDir()
	data := buildSkillTar(t, map[string]string{
		"SKILL.md":       "# skill\n",
		"scripts/run.sh": "echo hi\n",
	})
	if err := Extract(data, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for name, want := range map[string]string{
		"SKILL.md":       "# skill\n",
		"scripts/run.sh": "echo hi\n",
	} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestExtractRejectsPathEscape(t *testing.T) {
	dest := t.TempDir()
	data := buildSkillTar(t, map[string]string{
		"../../escape.txt": "nope",
	})
	if err := Extract(data, dest); err == nil {
		t.Fatal("Extract accepted a path-escaping entry")
	}
}

func TestExtractCreatesDirectories(t *testing.T) {
	dest := t.TempDir()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "nested/", Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Extract(buf.Bytes(), dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	info, err := os.Stat(filepath.Join(dest, "nested"))
	if err != nil || !info.IsDir() {
		t.Fatalf("nested dir missing: %v", err)
	}
}
