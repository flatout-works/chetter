package configguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectExcludesUntrackedGeneratedConfig(t *testing.T) {
	wsDir := initRepo(t)
	configPath := filepath.Join(wsDir, ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"token":"secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Protect(wsDir, configPath); err != nil {
		t.Fatalf("Protect: %v", err)
	}
	if out, err := exec.Command("git", "-C", wsDir, "check-ignore", ".mcp.json").CombinedOutput(); err != nil {
		t.Fatalf("generated config is not ignored: %v: %s", err, out)
	}
}

func TestProtectHidesTrackedGeneratedConfigChange(t *testing.T) {
	wsDir := initRepo(t)
	configPath := filepath.Join(wsDir, ".opencode.json")
	if err := os.WriteFile(configPath, []byte(`{"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, wsDir, "add", ".opencode.json")
	runGit(t, wsDir, "-c", "user.name=Chetter", "-c", "user.email=chetter@example.com", "commit", "-m", "add config")
	if err := os.WriteFile(configPath, []byte(`{"token":"secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Protect(wsDir, configPath); err != nil {
		t.Fatalf("Protect: %v", err)
	}
	out := runGit(t, wsDir, "status", "--porcelain")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("tracked generated config remains committable: %q", out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	wsDir := t.TempDir()
	runGit(t, wsDir, "init", "--quiet")
	return wsDir
}

func runGit(t *testing.T, wsDir string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", wsDir}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
