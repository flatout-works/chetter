package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHarnessChoices(t *testing.T) {
	for _, name := range []string{"", "opencode", "claude-code", "pi", "codewhale", "codex", "niffler", "unknown"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runner.yaml")
			if err := os.WriteFile(path, []byte("execution:\n  harness: '"+name+"'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if name == "unknown" {
				if err == nil {
					t.Fatal("unknown harness accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Execution.Harness != name {
				t.Fatalf("harness = %q, want %q", cfg.Execution.Harness, name)
			}
		})
	}
}
