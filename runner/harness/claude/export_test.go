package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSessionExportReadsJSONLDirectlyFromProjectDirectory(t *testing.T) {
	wsDir := t.TempDir()
	projectDir := filepath.Join(wsDir, ".claude", "projects", "project")
	if err := os.MkdirAll(projectDir, 0750); err != nil {
		t.Fatal(err)
	}
	contents := "{\"type\":\"user\",\"message\":{\"text\":\"Implement it\"}}\n" +
		"{\"type\":\"assistant\",\"message\":{\"text\":\"Implementation complete\"}}\n"
	if err := os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(contents), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectDir, "memory"), 0750); err != nil {
		t.Fatal(err)
	}

	export, err := readSessionExport(wsDir, "unused")
	if err != nil {
		t.Fatalf("readSessionExport returned error: %v", err)
	}
	if !strings.Contains(export, "Implement it") || !strings.Contains(export, "Implementation complete") {
		t.Fatalf("expected rendered session content, got %q", export)
	}
}

func TestReadSessionExportReadsJSONLFromSessionSubdirectory(t *testing.T) {
	wsDir := t.TempDir()
	sessionDir := filepath.Join(wsDir, ".claude", "projects", "project", "session")
	if err := os.MkdirAll(sessionDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.jsonl"), []byte(`{"type":"assistant","message":{"text":"Done"}}`+"\n"), 0640); err != nil {
		t.Fatal(err)
	}

	export, err := readSessionExport(wsDir, "unused")
	if err != nil {
		t.Fatalf("readSessionExport returned error: %v", err)
	}
	if !strings.Contains(export, "Done") {
		t.Fatalf("expected rendered session content, got %q", export)
	}
}
