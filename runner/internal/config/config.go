package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultNATSURL          = "nats://localhost:4222"
	DefaultTaskStream       = "DEVFLEET_TASKS"
	DefaultEventStream      = "DEVFLEET_EVENTS"
	DefaultTaskDurable      = "devfleet-runner"
	DefaultTaskQueue        = "devfleet-runners"
	DefaultAckWaitSeconds   = 10
	DefaultMaxDeliver       = 3
	DefaultMaxAckPending    = 4
	DefaultStorage          = "file"
	DefaultListenSubject    = "devfleet.runner.tasks"
	DefaultResultSubject    = "devfleet.tasks"
	DefaultWorkspaceRoot    = "/var/lib/runner"
	DefaultMaxConcurrent    = 10
	DefaultProxyAddr        = ":18080"
	DefaultDNSAddr          = ":53"
	DefaultDNSUpstream      = "8.8.8.8:53"
	DefaultDeployProvider   = "local"
	DefaultDevfleetURL       = "devfleet.works"
	EventPublishMinInterval = 15 * time.Second
	MCPProtocolVersion      = "2024-11-05"
	MCPServerVersion        = "0.1.0"
)

type Config struct {
	NATS         NATSConfig      `yaml:"nats"`
	JetStream    JetStreamConfig `yaml:"jetstream"`
	Runner       RunnerConfig    `yaml:"runner"`
	Proxy        ProxyConfig     `yaml:"proxy"`
	DNS          DNSConfig       `yaml:"dns"`
	Git          GitConfig       `yaml:"git"`
	Execution    ExecutionConfig `yaml:"execution"`
	Deploy       DeployConfig    `yaml:"deploy"`
	DevfleetMCP  DevfleetMCPConfig `yaml:"devfleet_mcp"`
	EmbeddedNATS bool            `yaml:"embedded_nats"`
}

// NATSConfig specifies the NATS connection URL and embedded server settings.
type NATSConfig struct {
	URL string `yaml:"url"`
}

// JetStreamConfig enables durable task consumption and event publishing via
// NATS JetStream. Plain NATS remains the default for local development.
type JetStreamConfig struct {
	Enabled        bool   `yaml:"enabled"`
	TaskStream     string `yaml:"task_stream"`
	EventStream    string `yaml:"event_stream"`
	TaskDurable    string `yaml:"task_durable"`
	TaskQueue      string `yaml:"task_queue"`
	AckWaitSeconds int    `yaml:"ack_wait_seconds"`
	MaxDeliver     int    `yaml:"max_deliver"`
	MaxAckPending  int    `yaml:"max_ack_pending"`
	Storage        string `yaml:"storage"`
}

// RunnerConfig configures runner behavior — subjects, workspace root, and
// concurrency limits.
type RunnerConfig struct {
	ListenSubject string `yaml:"listen_subject"`
	ResultSubject string `yaml:"result_subject"`
	WorkspaceRoot string `yaml:"workspace_root"`
	MaxConcurrent int    `yaml:"max_concurrent"`
}

type ProxyConfig struct {
	ListenAddr     string   `yaml:"listen_addr"`
	AllowedDomains []string `yaml:"allowed_domains"`
	BlockedDomains []string `yaml:"blocked_domains"`
}

// DNSConfig configures the DNS proxy with upstream resolver and domain
// blocklist for agent containers.
type DNSConfig struct {
	ListenAddr     string   `yaml:"listen_addr"`
	Upstream       string   `yaml:"upstream"`
	BlockedDomains []string `yaml:"blocked_domains"`
}

type GitConfig struct {
	SSHKeyPath string `yaml:"ssh_key_path"`
	PAT        string `yaml:"pat"`
}

// ExecutionConfig selects the optional container runtime and agent harness image.
type ExecutionConfig struct {
	Runtime string `yaml:"runtime"`
	Harness string `yaml:"harness"`
}

// DeployConfig configures the deployment provider and registry settings
// for MCP deploy tools.
type DeployConfig struct {
	Provider   string `yaml:"provider"`    // "local" or "preview"
	Registry   string `yaml:"registry"`    // Docker registry for push (e.g. "ghcr.io/devfleet")
	DevfleetURL string `yaml:"devfleet_url"` // Base URL for preview deployment (e.g. "devfleet.works")
}

// DevfleetMCPConfig configures the remote devfleet MCP server that exposes
// infrastructure tools (e.g. Arcane vulnerability scans) to agents running
// inside devfleet task containers.
type DevfleetMCPConfig struct {
	URL       string `yaml:"url"`        // Devfleet MCP HTTP endpoint (e.g. https://devfleet.works/mcp)
	AuthToken string `yaml:"auth_token"` // Bearer token for authenticating to the devfleet MCP
}

// Load reads configuration from the given path.
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
	if cfg.NATS.URL == "" {
		cfg.NATS.URL = DefaultNATSURL
	}
	if cfg.JetStream.TaskStream == "" {
		cfg.JetStream.TaskStream = DefaultTaskStream
	}
	if cfg.JetStream.EventStream == "" {
		cfg.JetStream.EventStream = DefaultEventStream
	}
	if cfg.JetStream.TaskDurable == "" {
		cfg.JetStream.TaskDurable = DefaultTaskDurable
	}
	if cfg.JetStream.TaskQueue == "" {
		cfg.JetStream.TaskQueue = DefaultTaskQueue
	}
	if cfg.JetStream.AckWaitSeconds == 0 {
		cfg.JetStream.AckWaitSeconds = DefaultAckWaitSeconds
	}
	if cfg.JetStream.MaxDeliver == 0 {
		cfg.JetStream.MaxDeliver = DefaultMaxDeliver
	}
	if cfg.JetStream.MaxAckPending == 0 {
		cfg.JetStream.MaxAckPending = DefaultMaxAckPending
	}
	if cfg.JetStream.Storage == "" {
		cfg.JetStream.Storage = DefaultStorage
	}
	if cfg.Runner.ListenSubject == "" {
		cfg.Runner.ListenSubject = DefaultListenSubject
	}
	if cfg.Runner.ResultSubject == "" {
		cfg.Runner.ResultSubject = DefaultResultSubject
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
	if cfg.Deploy.DevfleetURL == "" {
		cfg.Deploy.DevfleetURL = DefaultDevfleetURL
	}
	if cfg.DevfleetMCP.AuthToken == "" {
		cfg.DevfleetMCP.AuthToken = os.Getenv("DEVFLEET_MCP_AUTH_TOKEN")
	}
	if env := os.Getenv("JETSTREAM_ENABLED"); env != "" {
		cfg.JetStream.Enabled = env == "true" || env == "1"
	}
}
