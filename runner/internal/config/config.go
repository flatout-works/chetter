package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultWorkspaceRoot    = "/var/lib/runner"
	DefaultMaxConcurrent    = 10
	DefaultProxyAddr        = ":18080"
	DefaultDNSAddr          = ":53"
	DefaultDNSUpstream      = ""
	DefaultMCPRelayAddr     = ":18081"
	DefaultDeployProvider   = "local"
	DefaultChetterURL       = "chetter.flatout.works"
	EventPublishMinInterval = 15 * time.Second
	MCPProtocolVersion      = "2024-11-05"
	MCPServerVersion        = "0.1.0"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Runner     RunnerConfig     `yaml:"runner"`
	Proxy      ProxyConfig      `yaml:"proxy"`
	DNS        DNSConfig        `yaml:"dns"`
	Git        GitConfig        `yaml:"git"`
	Workspace  map[string]any   `yaml:"workspace"`
	Execution  ExecutionConfig  `yaml:"execution"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	Deploy     DeployConfig     `yaml:"deploy"`
	ChetterMCP ChetterMCPConfig `yaml:"chetter_mcp"`
}

type ServerConfig struct {
	URL       string `yaml:"url"`
	AuthToken string `yaml:"auth_token"`
}

type RunnerConfig struct {
	WorkspaceRoot string `yaml:"workspace_root"`
	MaxConcurrent int    `yaml:"max_concurrent"`
}

type ProxyConfig struct {
	ListenAddr     string   `yaml:"listen_addr"`
	AllowedDomains []string `yaml:"allowed_domains"`
	BlockedDomains []string `yaml:"blocked_domains"`
}

type DNSConfig struct {
	ListenAddr     string   `yaml:"listen_addr"`
	Upstream       string   `yaml:"upstream"`
	AllowedDomains []string `yaml:"allowed_domains"`
	BlockedDomains []string `yaml:"blocked_domains"`
}

type GitConfig struct {
	SSHKeyPath string `yaml:"ssh_key_path"`
	PAT        string `yaml:"pat"`
}

type ExecutionConfig struct {
	Backend         string `yaml:"backend"`
	Runtime         string `yaml:"runtime"`
	Harness         string `yaml:"harness"`
	UseGVisor       bool   `yaml:"use_gvisor"`
	ContainerMemory string `yaml:"container_memory"`
}

type KubernetesConfig struct {
	Namespace           string `yaml:"namespace"`
	RuntimeClass        string `yaml:"runtime_class"`
	ImagePullPolicy     string `yaml:"image_pull_policy"`
	CleanupAfterTask    *bool  `yaml:"cleanup_after_task"`
	AgentServiceAccount string `yaml:"agent_service_account"`
	WorkspacePVC        string `yaml:"workspace_pvc"`
	WorkspaceHostPath   string `yaml:"workspace_host_path"`
	NodeName            string `yaml:"node_name"`
	Kubeconfig          string `yaml:"kubeconfig"`
	PodReadyTimeoutSec  int    `yaml:"pod_ready_timeout_sec"`
	cleanupEnvInvalid   bool
}

type DeployConfig struct {
	Provider   string `yaml:"provider"`
	Registry   string `yaml:"registry"`
	ChetterURL string `yaml:"chetter_url"`
}

type ChetterMCPConfig struct {
	URL             string `yaml:"url"`
	AuthToken       string `yaml:"auth_token"`
	RelayListenAddr string `yaml:"relay_listen_addr"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Runner.MaxConcurrent < 0 {
		return fmt.Errorf("runner.max_concurrent must be greater than or equal to 0")
	}
	if cfg.Execution.Harness != "" && !isSupportedHarness(cfg.Execution.Harness) {
		return fmt.Errorf("execution.harness must be one of opencode, claude-code, pi, codewhale, or codex")
	}
	switch cfg.Execution.Backend {
	case "docker", "kubernetes", "local":
	default:
		return fmt.Errorf("execution.backend must be one of docker, kubernetes, or local")
	}
	if cfg.Execution.Backend == "kubernetes" {
		if cfg.Kubernetes.cleanupEnvInvalid {
			return fmt.Errorf("KUBERNETES_CLEANUP_AFTER_TASK must be a boolean")
		}
		if (cfg.Kubernetes.WorkspacePVC == "") == (cfg.Kubernetes.WorkspaceHostPath == "") {
			return fmt.Errorf("kubernetes mode requires exactly one of kubernetes.workspace_pvc or kubernetes.workspace_host_path")
		}
		if cfg.Kubernetes.WorkspaceHostPath != "" && cfg.Kubernetes.NodeName == "" {
			return fmt.Errorf("kubernetes hostPath mode requires NODE_NAME or kubernetes.node_name")
		}
		if cfg.Kubernetes.CleanupAfterTask != nil && !*cfg.Kubernetes.CleanupAfterTask {
			return fmt.Errorf("kubernetes.cleanup_after_task must be true; live agent pods and environment secrets are never preserved")
		}
		switch cfg.Kubernetes.ImagePullPolicy {
		case "Always", "IfNotPresent", "Never":
		default:
			return fmt.Errorf("kubernetes.image_pull_policy must be Always, IfNotPresent, or Never")
		}
		if cfg.Kubernetes.PodReadyTimeoutSec <= 0 {
			return fmt.Errorf("kubernetes.pod_ready_timeout_sec must be greater than 0")
		}
	}
	return nil
}

func isSupportedHarness(harness string) bool {
	switch harness {
	case "opencode", "claude-code", "pi", "codewhale", "codex":
		return true
	default:
		return false
	}
}

func applyDefaults(cfg *Config) {
	if backend, ok := os.LookupEnv("EXECUTION_BACKEND"); ok && strings.TrimSpace(backend) != "" {
		cfg.Execution.Backend = strings.ToLower(strings.TrimSpace(backend))
	} else if cfg.Execution.Backend == "" {
		cfg.Execution.Backend = "docker"
	}
	if cfg.Server.URL == "" {
		cfg.Server.URL = os.Getenv("CHETTER_SERVER_URL")
	}
	if cfg.Server.AuthToken == "" {
		cfg.Server.AuthToken = firstEnv("CHETTER_RUNNER_AUTH_TOKEN", "CHETTER_RUNNER_RPC_TOKEN", "MCP_AUTH_TOKEN", "CHETTER_MCP_AUTH_TOKEN")
	}
	if cfg.Runner.WorkspaceRoot == "" {
		cfg.Runner.WorkspaceRoot = DefaultWorkspaceRoot
	}
	if cfg.Runner.MaxConcurrent == 0 {
		cfg.Runner.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.Proxy.ListenAddr == "" {
		cfg.Proxy.ListenAddr = DefaultProxyAddr
	}
	if cfg.DNS.ListenAddr == "" {
		cfg.DNS.ListenAddr = DefaultDNSAddr
	}
	if cfg.DNS.Upstream == "" {
		cfg.DNS.Upstream = DefaultDNSUpstream
	}
	if cfg.Deploy.Provider == "" {
		cfg.Deploy.Provider = DefaultDeployProvider
	}
	if cfg.Deploy.ChetterURL == "" {
		cfg.Deploy.ChetterURL = DefaultChetterURL
	}
	if cfg.ChetterMCP.AuthToken == "" {
		cfg.ChetterMCP.AuthToken = os.Getenv("CHETTER_MCP_AUTH_TOKEN")
	}
	if cfg.ChetterMCP.RelayListenAddr == "" {
		cfg.ChetterMCP.RelayListenAddr = DefaultMCPRelayAddr
	}
	if !cfg.Execution.UseGVisor {
		cfg.Execution.UseGVisor = os.Getenv("USE_GVISOR") == "true"
	}
	setStringFromEnv(&cfg.Kubernetes.Namespace, "KUBERNETES_NAMESPACE")
	setStringFromEnv(&cfg.Kubernetes.RuntimeClass, "KUBERNETES_RUNTIME_CLASS")
	setStringFromEnv(&cfg.Kubernetes.ImagePullPolicy, "KUBERNETES_AGENT_IMAGE_PULL_POLICY")
	setStringFromEnv(&cfg.Kubernetes.AgentServiceAccount, "KUBERNETES_AGENT_SERVICE_ACCOUNT")
	setStringFromEnv(&cfg.Kubernetes.WorkspacePVC, "KUBERNETES_WORKSPACE_PVC")
	setStringFromEnv(&cfg.Kubernetes.WorkspaceHostPath, "KUBERNETES_WORKSPACE_HOST_PATH")
	setStringFromEnv(&cfg.Kubernetes.NodeName, "NODE_NAME")
	setStringFromEnv(&cfg.Kubernetes.Kubeconfig, "KUBECONFIG")
	if cfg.Kubernetes.Namespace == "" {
		cfg.Kubernetes.Namespace = "default"
	}
	if cfg.Kubernetes.ImagePullPolicy == "" {
		cfg.Kubernetes.ImagePullPolicy = "IfNotPresent"
	}
	if cfg.Kubernetes.PodReadyTimeoutSec == 0 {
		cfg.Kubernetes.PodReadyTimeoutSec = 120
	}
	if value := strings.TrimSpace(os.Getenv("KUBERNETES_POD_READY_TIMEOUT_SEC")); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			cfg.Kubernetes.PodReadyTimeoutSec = seconds
		} else {
			cfg.Kubernetes.PodReadyTimeoutSec = -1
		}
	}
	if value, ok := os.LookupEnv("KUBERNETES_CLEANUP_AFTER_TASK"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			cfg.Kubernetes.cleanupEnvInvalid = true
		} else {
			cfg.Kubernetes.CleanupAfterTask = &parsed
		}
	}
	if cfg.Kubernetes.CleanupAfterTask == nil {
		cleanup := true
		cfg.Kubernetes.CleanupAfterTask = &cleanup
	}
}

func setStringFromEnv(target *string, key string) {
	if value, ok := os.LookupEnv(key); ok {
		*target = strings.TrimSpace(value)
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}
