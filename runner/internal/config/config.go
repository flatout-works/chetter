package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultWorkspaceRoot    = "/var/lib/runner"
	DefaultMaxConcurrent    = 10
	DefaultProxyAddr        = ":18080"
	DefaultDNSAddr          = ":53"
	DefaultDNSUpstream      = "8.8.8.8:53"
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
	Execution  ExecutionConfig  `yaml:"execution"`
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
	BlockedDomains []string `yaml:"blocked_domains"`
}

type GitConfig struct {
	SSHKeyPath string `yaml:"ssh_key_path"`
	PAT        string `yaml:"pat"`
}

type ExecutionConfig struct {
	Runtime  string `yaml:"runtime"`
	Harness  string `yaml:"harness"`
	UseGVisor bool `yaml:"use_gvisor"`
}

type DeployConfig struct {
	Provider   string `yaml:"provider"`
	Registry   string `yaml:"registry"`
	ChetterURL string `yaml:"chetter_url"`
}

type ChetterMCPConfig struct {
	URL       string `yaml:"url"`
	AuthToken string `yaml:"auth_token"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.URL == "" {
		cfg.Server.URL = os.Getenv("CHETTER_SERVER_URL")
	}
	if cfg.Server.AuthToken == "" {
		cfg.Server.AuthToken = firstEnv("CHETTER_RUNNER_AUTH_TOKEN", "MCP_AUTH_TOKEN", "CHETTER_MCP_AUTH_TOKEN")
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
	if !cfg.Execution.UseGVisor {
		cfg.Execution.UseGVisor = os.Getenv("USE_GVISOR") == "true"
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
