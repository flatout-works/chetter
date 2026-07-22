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

func TestReadSessionExportReadsClaudeContentBlocks(t *testing.T) {
	wsDir := t.TempDir()
	projectDir := filepath.Join(wsDir, ".claude", "projects", "project")
	if err := os.MkdirAll(projectDir, 0750); err != nil {
		t.Fatal(err)
	}
	contents := "{\"type\":\"user\",\"message\":{\"content\":\"Implement it\"}}\n" +
		"{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Implementation complete\"}]}}\n"
	if err := os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(contents), 0640); err != nil {
		t.Fatal(err)
	}

	export, err := readSessionExport(wsDir, "unused")
	if err != nil {
		t.Fatalf("readSessionExport returned error: %v", err)
	}
	if !strings.Contains(export, "> Implement it") || !strings.Contains(export, "Implementation complete") {
		t.Fatalf("expected rendered session content, got %q", export)
	}
}

func TestReadSessionExportUsesMappedNativeIDWithMultipleSessions(t *testing.T) {
	wsDir := t.TempDir()
	projectDir := filepath.Join(wsDir, ".claude", "projects", "project")
	mappingDir := filepath.Join(wsDir, ".claude", "chetter-sessions")
	if err := os.MkdirAll(projectDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mappingDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "native-a.jsonl"), []byte(`{"type":"assistant","message":{"text":"wrong session"}}`+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "native-b.jsonl"), []byte(`{"type":"assistant","message":{"text":"selected session"}}`+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mappingDir, "proxy-id.json"), []byte(`{"native_session_id":"native-b"}`), 0640); err != nil {
		t.Fatal(err)
	}

	export, err := readSessionExport(wsDir, "proxy-id")
	if err != nil {
		t.Fatalf("readSessionExport returned error: %v", err)
	}
	if !strings.Contains(export, "selected session") || strings.Contains(export, "wrong session") {
		t.Fatalf("selected wrong transcript: %q", export)
	}
}

func TestReadSessionExportRefusesToGuessAmongMultipleSessions(t *testing.T) {
	wsDir := t.TempDir()
	projectDir := filepath.Join(wsDir, ".claude", "projects", "project")
	if err := os.MkdirAll(projectDir, 0750); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"native-a", "native-b"} {
		if err := os.WriteFile(filepath.Join(projectDir, id+".jsonl"), []byte("{}\n"), 0640); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := readSessionExport(wsDir, "unknown-proxy"); err == nil {
		t.Fatal("expected ambiguous export selection to fail")
	}
}
