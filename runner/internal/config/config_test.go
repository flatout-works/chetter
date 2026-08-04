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

func TestContainerLimitsValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		wantErr bool
	}{
		{name: "unset allowed", yaml: `{}`},
		{name: "zero allowed", yaml: "execution:\n  container_cpu: 0\n  container_pids: 0\n"},
		{name: "valid cpu and pids", yaml: "execution:\n  container_cpu: 2\n  container_pids: 256\n"},
		{name: "fractional cpu", yaml: "execution:\n  container_cpu: 1.5\n"},
		{name: "valid memory mb", yaml: "execution:\n  container_memory: 512m\n"},
		{name: "valid memory gb", yaml: "execution:\n  container_memory: 1g\n"},
		{name: "valid memory fractional gb", yaml: "execution:\n  container_memory: 1.5g\n"},
		{name: "valid memory bytes", yaml: "execution:\n  container_memory: \"2048\"\n"},
		{name: "invalid memory format", yaml: "execution:\n  container_memory: abc\n", wantErr: true},
		{name: "invalid memory zero", yaml: "execution:\n  container_memory: 0\n", wantErr: true},
		{name: "invalid memory negative", yaml: "execution:\n  container_memory: -512m\n", wantErr: true},
		{name: "negative cpu", yaml: "execution:\n  container_cpu: -1\n", wantErr: true},
		{name: "negative pids", yaml: "execution:\n  container_pids: -1\n", wantErr: true},
		{name: "env cpu override", yaml: `{}`, env: map[string]string{"CHETTER_CONTAINER_CPU": "1.5"}},
		{name: "env pids override", yaml: `{}`, env: map[string]string{"CHETTER_CONTAINER_PIDS": "100"}},
		{name: "env memory override", yaml: `{}`, env: map[string]string{"CHETTER_CONTAINER_MEMORY": "512m"}},
		{name: "invalid env cpu", yaml: `{}`, env: map[string]string{"CHETTER_CONTAINER_CPU": "abc"}, wantErr: true},
		{name: "invalid env pids", yaml: `{}`, env: map[string]string{"CHETTER_CONTAINER_PIDS": "abc"}, wantErr: true},
		{name: "invalid env memory", yaml: `{}`, env: map[string]string{"CHETTER_CONTAINER_MEMORY": "abc"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CHETTER_CONTAINER_CPU", "")
			t.Setenv("CHETTER_CONTAINER_PIDS", "")
			t.Setenv("CHETTER_CONTAINER_MEMORY", "")
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			path := filepath.Join(t.TempDir(), "runner.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected validation error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestContainerLimitsEnvOverridesYAML(t *testing.T) {
	t.Setenv("CHETTER_CONTAINER_CPU", "1.5")
	t.Setenv("CHETTER_CONTAINER_PIDS", "100")
	t.Setenv("CHETTER_CONTAINER_MEMORY", "512m")
	path := filepath.Join(t.TempDir(), "runner.yaml")
	data := "execution:\n  container_cpu: 4\n  container_pids: 200\n  container_memory: 1g\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Execution.ContainerCPU != 1.5 {
		t.Errorf("ContainerCPU = %v, want 1.5 (env should override YAML)", cfg.Execution.ContainerCPU)
	}
	if cfg.Execution.ContainerPIDs != 100 {
		t.Errorf("ContainerPIDs = %v, want 100 (env should override YAML)", cfg.Execution.ContainerPIDs)
	}
	if cfg.Execution.ContainerMemory != "512m" {
		t.Errorf("ContainerMemory = %q, want 512m (env should override YAML)", cfg.Execution.ContainerMemory)
	}
}

func TestParseMemoryBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		err   bool
	}{
		{"512m", 512 << 20, false},
		{"512M", 512 << 20, false},
		{"1g", 1 << 30, false},
		{"2G", 2 << 30, false},
		{"1.5g", 1536 << 20, false},
		{"256k", 256 << 10, false},
		{"2048", 2048, false},
		{"4b", 4, false},
		{" 1g ", 1 << 30, false},
		{"", 0, true},
		{"abc", 0, true},
		{"0", 0, true},
		{"-512m", 0, true},
		{"1x", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseMemoryBytes(tc.input)
			if tc.err {
				if err == nil {
					t.Fatalf("ParseMemoryBytes(%q) = %d, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMemoryBytes(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseMemoryBytes(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestAllowUnisolatedEnvAndYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	data := `
execution:
  allow_unisolated: true
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Execution.AllowUnisolated {
		t.Fatal("execution.allow_unisolated: true should be honored")
	}

	// Env var also enables the escape hatch (issue #291).
	t.Setenv("CHETTER_ALLOW_UNISOLATED", "true")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if !cfg.Execution.AllowUnisolated {
		t.Fatal("CHETTER_ALLOW_UNISOLATED=true should enable the escape hatch")
	}

	// Unset stays false (hardened default), even when the env var is cleared
	// and the YAML does not enable it.
	plain := filepath.Join(dir, "plain.yaml")
	if err := os.WriteFile(plain, []byte("execution:\n  backend: docker\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHETTER_ALLOW_UNISOLATED", "")
	cfg, err = Load(plain)
	if err != nil {
		t.Fatalf("Load without env: %v", err)
	}
	if cfg.Execution.AllowUnisolated {
		t.Fatal("unset CHETTER_ALLOW_UNISOLATED must default to false")
	}
}
