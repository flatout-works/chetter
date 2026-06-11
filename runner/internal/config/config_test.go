package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	data := `
nats:
  url: nats://example:4222
runner:
  listen_subject: devfleet.test.tasks
  result_subject: devfleet.test.results
  workspace_root: /tmp/ws
  max_concurrent: 5
proxy:
  listen_addr: ":18080"
  allowed_domains: [github.com]
  blocked_domains: [pastebin.com]
dns:
  listen_addr: ":5300"
  upstream: 1.1.1.1:53
  blocked_domains: [metadata.google.internal]
git:
  ssh_key_path: /home/user/.ssh/id_rsa
  pat: ghp_token
execution:
  runtime: containerd
  harness: default
embedded_nats: true
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATS.URL != "nats://example:4222" {
		t.Errorf("NATS.URL = %q, want nats://example:4222", cfg.NATS.URL)
	}
	if cfg.Runner.ListenSubject != "devfleet.test.tasks" {
		t.Errorf("Runner.ListenSubject = %q", cfg.Runner.ListenSubject)
	}
	if cfg.Runner.MaxConcurrent != 5 {
		t.Errorf("Runner.MaxConcurrent = %d, want 5", cfg.Runner.MaxConcurrent)
	}
	if cfg.Proxy.ListenAddr != ":18080" {
		t.Errorf("Proxy.ListenAddr = %q", cfg.Proxy.ListenAddr)
	}
	if len(cfg.Proxy.AllowedDomains) != 1 || cfg.Proxy.AllowedDomains[0] != "github.com" {
		t.Errorf("Proxy.AllowedDomains = %v", cfg.Proxy.AllowedDomains)
	}
	if cfg.DNS.Upstream != "1.1.1.1:53" {
		t.Errorf("DNS.Upstream = %q", cfg.DNS.Upstream)
	}
	if cfg.Git.SSHKeyPath != "/home/user/.ssh/id_rsa" {
		t.Errorf("Git.SSHKeyPath = %q", cfg.Git.SSHKeyPath)
	}
	if cfg.Git.PAT != "ghp_token" {
		t.Errorf("Git.PAT = %q", cfg.Git.PAT)
	}
	if !cfg.EmbeddedNATS {
		t.Error("EmbeddedNATS = false, want true")
	}
}

func TestLoadDefaultsAreApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "minimal.yaml")
	data := `{}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATS.URL != "nats://localhost:4222" {
		t.Errorf("NATS.URL = %q, want default", cfg.NATS.URL)
	}
	if cfg.Runner.ListenSubject != "devfleet.runner.tasks" {
		t.Errorf("ListenSubject = %q", cfg.Runner.ListenSubject)
	}
	if cfg.Runner.ResultSubject != "devfleet.tasks" {
		t.Errorf("ResultSubject = %q", cfg.Runner.ResultSubject)
	}
	if cfg.Runner.WorkspaceRoot != "/var/lib/runner" {
		t.Errorf("WorkspaceRoot = %q", cfg.Runner.WorkspaceRoot)
	}
	if cfg.Runner.MaxConcurrent != 10 {
		t.Errorf("MaxConcurrent = %d, want 10", cfg.Runner.MaxConcurrent)
	}
	if cfg.Proxy.ListenAddr != ":18080" {
		t.Errorf("Proxy.ListenAddr = %q", cfg.Proxy.ListenAddr)
	}
	if cfg.DNS.ListenAddr != ":53" {
		t.Errorf("DNS.ListenAddr = %q", cfg.DNS.ListenAddr)
	}
	if cfg.DNS.Upstream != "8.8.8.8:53" {
		t.Errorf("DNS.Upstream = %q", cfg.DNS.Upstream)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/runner.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(path, []byte("{{{broken"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

