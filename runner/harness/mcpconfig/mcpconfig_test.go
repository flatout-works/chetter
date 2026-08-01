package mcpconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWritePrivateFileTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(path, []byte("secret")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "secret" {
		t.Fatalf("content = %q", data)
	}
}

func TestSetBearerToken(t *testing.T) {
	t.Run("sets authorization header", func(t *testing.T) {
		server := map[string]any{"url": "http://runner.test/mcp"}
		SetBearerToken(server, "task-token")
		headers, ok := server["headers"].(map[string]string)
		if !ok || headers["Authorization"] != "Bearer task-token" {
			t.Fatalf("headers = %#v", server["headers"])
		}
	})

	t.Run("ignores empty token", func(t *testing.T) {
		server := map[string]any{"url": "http://runner.test/mcp"}
		SetBearerToken(server, "")
		if _, ok := server["headers"]; ok {
			t.Fatalf("empty token added headers: %#v", server["headers"])
		}
	})
}
