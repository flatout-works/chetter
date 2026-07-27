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
server:
  url: https://chetter.example.com
  auth_token: test-token
runner:
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
  runtime: docker
  harness: opencode
chetter_mcp:
  relay_listen_addr: :18082
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.URL != "https://chetter.example.com" {
		t.Errorf("Server.URL = %q", cfg.Server.URL)
	}
	if cfg.Server.AuthToken != "test-token" {
		t.Errorf("Server.AuthToken = %q", cfg.Server.AuthToken)
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
	if cfg.ChetterMCP.RelayListenAddr != ":18082" {
		t.Errorf("ChetterMCP.RelayListenAddr = %q", cfg.ChetterMCP.RelayListenAddr)
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
	if cfg.DNS.Upstream != "" {
		t.Errorf("DNS.Upstream = %q", cfg.DNS.Upstream)
	}
	if cfg.ChetterMCP.RelayListenAddr != ":18081" {
		t.Errorf("ChetterMCP.RelayListenAddr = %q", cfg.ChetterMCP.RelayListenAddr)
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

func TestLoadAcceptsCodexHarness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.yaml")
	if err := os.WriteFile(path, []byte("execution:\n  harness: codex\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load codex harness: %v", err)
	}
}

func TestExecutionBackendSelection(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{name: "default docker", yaml: `{}`, want: "docker"},
		{name: "yaml local", yaml: "execution:\n  backend: local\n", want: "local"},
		{name: "environment wins", yaml: "execution:\n  backend: local\n", env: map[string]string{"EXECUTION_BACKEND": "docker"}, want: "docker"},
		{name: "unknown explicit", yaml: `{}`, env: map[string]string{"EXECUTION_BACKEND": "containerd"}, wantErr: true},
		{name: "kubernetes requires volume", yaml: "execution:\n  backend: kubernetes\n", wantErr: true},
		{name: "kubernetes pvc", yaml: "execution:\n  backend: kubernetes\nkubernetes:\n  workspace_pvc: runner-workspaces\n", want: "kubernetes"},
		{name: "kubernetes host path", yaml: "execution:\n  backend: kubernetes\nkubernetes:\n  workspace_host_path: /var/lib/runner\n  node_name: node-1\n", want: "kubernetes"},
		{name: "kubernetes host path requires node", yaml: "execution:\n  backend: kubernetes\nkubernetes:\n  workspace_host_path: /var/lib/runner\n", env: map[string]string{"NODE_NAME": ""}, wantErr: true},
		{name: "kubernetes volume conflict", yaml: "execution:\n  backend: kubernetes\nkubernetes:\n  workspace_pvc: runner-workspaces\n  workspace_host_path: /var/lib/runner\n", wantErr: true},
		{name: "kubernetes cleanup required", yaml: "execution:\n  backend: kubernetes\nkubernetes:\n  workspace_pvc: runner-workspaces\n  cleanup_after_task: false\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EXECUTION_BACKEND", "")
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			path := filepath.Join(t.TempDir(), "runner.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load succeeded, backend=%q", cfg.Execution.Backend)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Execution.Backend != tc.want {
				t.Fatalf("backend = %q, want %q", cfg.Execution.Backend, tc.want)
			}
		})
	}
}

func TestKubernetesEnvironmentOverridesYAML(t *testing.T) {
	t.Setenv("EXECUTION_BACKEND", "kubernetes")
	t.Setenv("KUBERNETES_NAMESPACE", "agents")
	t.Setenv("KUBERNETES_RUNTIME_CLASS", "gvisor")
	t.Setenv("KUBERNETES_WORKSPACE_PVC", "shared-workspaces")
	path := filepath.Join(t.TempDir(), "runner.yaml")
	data := "execution:\n  backend: docker\nkubernetes:\n  namespace: ignored\n  workspace_host_path: /ignored\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected conflicting env PVC and YAML hostPath to fail")
	}
	t.Setenv("KUBERNETES_WORKSPACE_HOST_PATH", "")
	// Empty environment values explicitly clear YAML values.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Execution.Backend != "kubernetes" || cfg.Kubernetes.Namespace != "agents" || cfg.Kubernetes.RuntimeClass != "gvisor" || cfg.Kubernetes.WorkspacePVC != "shared-workspaces" || cfg.Kubernetes.WorkspaceHostPath != "" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
