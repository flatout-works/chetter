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

	// DefaultMinFreeHostMemoryMB is the conservative default floor for the
	// runner's host-pressure claim gate (issue #397): while free host memory
	// (MemAvailable) stays above this many MiB the runner claims normally;
	// below it the runner pauses claiming to avoid admitting work into a host
	// that is about to thrash. It applies when the operator has not configured
	// CHETTER_MIN_FREE_HOST_MEMORY_MB / runner.min_free_host_memory_mb
	// explicitly; set the env var to 0 to disable the gate entirely.
	DefaultMinFreeHostMemoryMB = 1024
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
	// MinFreeHostMemoryMB gates task claiming on free host memory (issue
	// #397): while MemAvailable stays above this many MiB the runner claims
	// normally; below it the runner pauses claiming and reports
	// "admission_paused:memory_pressure" in its heartbeat status. 0 disables
	// the memory gate (CHETTER_MIN_FREE_HOST_MEMORY_MB=0). When unset, the
	// conservative DefaultMinFreeHostMemoryMB applies.
	MinFreeHostMemoryMB int `yaml:"min_free_host_memory_mb"`
	// MaxHostLoad optionally gates task claiming on the host's 1-minute load
	// average (issue #397): when the load exceeds this value the runner pauses
	// claiming and reports "admission_paused:host_load" in its heartbeat
	// status. 0 (the default) disables the load gate; it is opt-in because a
	// meaningful threshold depends on the host's core count.
	MaxHostLoad float64 `yaml:"max_host_load"`

	minFreeHostMemoryMBEnvInvalid bool
	maxHostLoadEnvInvalid         bool
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
	Backend         string  `yaml:"backend"`
	Runtime         string  `yaml:"runtime"`
	Harness         string  `yaml:"harness"`
	UseGVisor       bool    `yaml:"use_gvisor"`
	AllowUnisolated bool    `yaml:"allow_unisolated"`
	ContainerMemory string  `yaml:"container_memory"`
	ContainerCPU    float64 `yaml:"container_cpu"`
	ContainerPIDs   int     `yaml:"container_pids"`

	containerMemoryEnvInvalid bool
	containerCPUEnvInvalid    bool
	containerPIDsEnvInvalid   bool
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
	if cfg.Runner.MinFreeHostMemoryMB < 0 {
		return fmt.Errorf("runner.min_free_host_memory_mb must be greater than or equal to 0")
	}
	if cfg.Runner.MaxHostLoad < 0 {
		return fmt.Errorf("runner.max_host_load must be greater than or equal to 0")
	}
	if cfg.Runner.minFreeHostMemoryMBEnvInvalid {
		return fmt.Errorf("CHETTER_MIN_FREE_HOST_MEMORY_MB must be a non-negative integer number of MiB (0 disables the gate)")
	}
	if cfg.Runner.maxHostLoadEnvInvalid {
		return fmt.Errorf("CHETTER_MAX_HOST_LOAD must be a non-negative number (0 disables the gate)")
	}
	if cfg.Execution.containerMemoryEnvInvalid {
		return fmt.Errorf("CHETTER_CONTAINER_MEMORY must be a valid memory limit (e.g. 512m, 1g)")
	}
	if cfg.Execution.containerCPUEnvInvalid {
		return fmt.Errorf("CHETTER_CONTAINER_CPU must be a positive number")
	}
	if cfg.Execution.containerPIDsEnvInvalid {
		return fmt.Errorf("CHETTER_CONTAINER_PIDS must be a positive integer")
	}
	if cfg.Execution.ContainerMemory != "" {
		if _, err := ParseMemoryBytes(cfg.Execution.ContainerMemory); err != nil {
			return fmt.Errorf("execution.container_memory: %v", err)
		}
	}
	if cfg.Execution.ContainerCPU < 0 {
		return fmt.Errorf("execution.container_cpu must be greater than or equal to 0")
	}
	if cfg.Execution.ContainerPIDs < 0 {
		return fmt.Errorf("execution.container_pids must be greater than or equal to 0")
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
	// Host-pressure claim gate (issue #397). Env vars override YAML. A
	// non-empty CHETTER_MIN_FREE_HOST_MEMORY_MB wins even when it is "0"
	// (explicit disable); when neither env nor YAML configure a value, fall
	// back to the conservative DefaultMinFreeHostMemoryMB so every deployment
	// is protected from claiming into a thrashing host. MaxHostLoad has no
	// default (0 = disabled): a load threshold is only meaningful relative to
	// the host's core count, so it is opt-in per deployment.
	if value := strings.TrimSpace(os.Getenv("CHETTER_MIN_FREE_HOST_MEMORY_MB")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			cfg.Runner.MinFreeHostMemoryMB = parsed
		} else {
			cfg.Runner.minFreeHostMemoryMBEnvInvalid = true
		}
	} else if cfg.Runner.MinFreeHostMemoryMB == 0 {
		cfg.Runner.MinFreeHostMemoryMB = DefaultMinFreeHostMemoryMB
	}
	if value := strings.TrimSpace(os.Getenv("CHETTER_MAX_HOST_LOAD")); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed >= 0 {
			cfg.Runner.MaxHostLoad = parsed
		} else {
			cfg.Runner.maxHostLoadEnvInvalid = true
		}
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
	// Documented escape hatch for single-tenant / trusted deployments that
	// intentionally run without gVisor: accept isolation-requiring tasks even
	// when enforced isolation is unavailable. See CHETTER_ALLOW_UNISOLATED.
	if !cfg.Execution.AllowUnisolated {
		cfg.Execution.AllowUnisolated = strings.EqualFold(strings.TrimSpace(os.Getenv("CHETTER_ALLOW_UNISOLATED")), "true")
	}
	if value := strings.TrimSpace(os.Getenv("CHETTER_CONTAINER_MEMORY")); value != "" {
		if _, err := ParseMemoryBytes(value); err == nil {
			cfg.Execution.ContainerMemory = value
		} else {
			cfg.Execution.containerMemoryEnvInvalid = true
		}
	}
	if value := strings.TrimSpace(os.Getenv("CHETTER_CONTAINER_CPU")); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			cfg.Execution.ContainerCPU = parsed
		} else {
			cfg.Execution.containerCPUEnvInvalid = true
		}
	}
	if value := strings.TrimSpace(os.Getenv("CHETTER_CONTAINER_PIDS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.Execution.ContainerPIDs = parsed
		} else {
			cfg.Execution.containerPIDsEnvInvalid = true
		}
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

// ParseMemoryBytes parses a Docker-style memory limit such as "512m" or "1g"
// into bytes. Suffixes are binary (b, k, m, g map to KiB/MiB/GiB), matching
// Docker's --memory semantics; a bare number is interpreted as bytes. It is
// used both to validate execution.container_memory at startup and to convert
// the configured limit to MB for runner capacity reporting.
func ParseMemoryBytes(value string) (int64, error) {
	s := strings.TrimSpace(strings.ToLower(value))
	if s == "" {
		return 0, fmt.Errorf("empty memory limit")
	}
	multiplier := int64(1)
	switch s[len(s)-1] {
	case 'b':
		s = s[:len(s)-1]
	case 'k':
		multiplier = 1 << 10
		s = s[:len(s)-1]
	case 'm':
		multiplier = 1 << 20
		s = s[:len(s)-1]
	case 'g':
		multiplier = 1 << 30
		s = s[:len(s)-1]
	}
	if s == "" {
		return 0, fmt.Errorf("memory limit %q has no numeric value", value)
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory limit %q", value)
	}
	if n <= 0 {
		return 0, fmt.Errorf("memory limit %q must be a positive number", value)
	}
	return int64(n * float64(multiplier)), nil
}
