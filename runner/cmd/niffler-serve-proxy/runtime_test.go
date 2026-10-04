package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeRejectsSubstitutedAssets(t *testing.T) {
	for _, name := range []string{"var", "var/bin/niffler", "manifest.yaml"} {
		t.Run(name, func(t *testing.T) {
			source := t.TempDir()
			ws := t.TempDir()
			root := filepath.Join(ws, ".niffler", "runtime")
			os.MkdirAll(filepath.Join(source, "var", "bin"), 0700)
			os.WriteFile(filepath.Join(source, "manifest.yaml"), []byte("components: []"), 0600)
			os.WriteFile(filepath.Join(source, "var", "bin", "niffler"), []byte("trusted"), 0700)
			os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700)
			if name == "var" {
				os.Symlink(t.TempDir(), filepath.Join(root, name))
			} else {
				os.WriteFile(filepath.Join(root, name), []byte("substitute"), 0700)
			}
			if e := prepareRuntime(source, root, ws); e == nil {
				t.Fatal("untrusted asset accepted")
			}
		})
	}
}
func TestRuntimeDoesNotCopyInstallationState(t *testing.T) {
	source := t.TempDir()
	ws := t.TempDir()
	root := filepath.Join(ws, ".niffler", "runtime")
	os.MkdirAll(filepath.Join(source, "var", "bin"), 0700)
	os.WriteFile(filepath.Join(source, "manifest.yaml"), []byte("components: []"), 0600)
	os.WriteFile(filepath.Join(source, ".env"), []byte("SECRET=yes"), 0600)
	os.WriteFile(filepath.Join(source, "var", "store.db"), []byte("history"), 0600)
	os.WriteFile(filepath.Join(source, "var", "bin", "niffler"), []byte("binary"), 0700)
	if e := prepareRuntime(source, root, ws); e != nil {
		t.Fatal(e)
	}
	if e := prepareRuntime(source, root, ws); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{".env", "var/store.db"} {
		if _, e := os.Stat(filepath.Join(root, name)); !os.IsNotExist(e) {
			t.Fatal(name)
		}
	}
}
func TestTerminalEventPreservesUsageAndFiltersChildren(t *testing.T) {
	b := &bridge{secrets: []string{"secret"}, listeners: map[chan event]bool{}}
	send := func(id, subject string) {
		raw, _ := json.Marshal(map[string]any{"sessionId": id, "turnId": "turn-1", "phase": "done", "outcome": "cancelled", "usage": map[string]any{"promptTokens": 10, "cacheWriteTokens": 2}})
		b.observe(subject, raw, "parent")
	}
	send("child", "ev.session.child.turn")
	if len(b.history) != 0 {
		t.Fatal("child included")
	}
	send("parent", "ev.session.parent.status")
	if len(b.history) != 0 {
		t.Fatal("round usage double counted")
	}
	send("parent", "ev.session.parent.turn")
	if len(b.history) != 2 || !strings.Contains(string(b.history[1].Data), "cacheWriteTokens") {
		t.Fatal(b.history)
	}
	if b.redact("a secret b") != "a [REDACTED] b" {
		t.Fatal("redaction")
	}
}
