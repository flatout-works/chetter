// Package controller orchestrates agent execution — claiming tasks via
// ConnectRPC, provisioning isolated workspaces and Kata Containers,
// exposing MCP tools, and publishing results.
package controller

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/harness/claude"
	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/containerd"
	"github.com/flatout-works/chetter/runner/internal/network"
	"github.com/flatout-works/chetter/runner/internal/task"
	"github.com/flatout-works/chetter/runner/internal/workspace"
)

const (
	defaultTaskTimeoutSec = 3600
	maxSummaryBytes       = 8000
	serveReadyTimeout     = 15 * time.Second
	servePollInterval     = 500 * time.Millisecond
	serveHTTPTimeout      = 2 * time.Second
	opencodePluginNS      = "chetter-runner"
)

type Runner struct {
	cfg        *config.Config
	h          harness.Harness
	wsManager  *workspace.Manager
	proxy      *network.TransparentProxy
	dnsProxy   *network.DNSProxy
	bridgeMgr  *network.BridgeManager
	containerd *containerd.Client
	rpcClient   runnerRPCClient
	claimClient runnerRPCClient
	runCtx      context.Context
	mu         sync.Mutex
	tasks      map[string]*task.TaskSession
	runnerID   string
	startedAt  time.Time

	totalStarted   int64
	totalCompleted int64
	totalErrors    int64
	terminalTasks  map[string]struct{}
	cancelledTasks map[string]struct{}
	sem            chan struct{}
}

func NewRunner(cfg *config.Config) (*Runner, error) {
	cd := containerd.NewClient(opencodePluginNS)
	runnerID, err := newRunnerID()
	if err != nil {
		return nil, err
	}
	return &Runner{
		cfg:            cfg,
		h:              selectHarness(cfg),
		wsManager:      workspace.NewManager(cfg.Runner.WorkspaceRoot),
		containerd:     cd,
		bridgeMgr:      network.NewBridgeManager(cfg.Proxy.ListenAddr, cfg.DNS.ListenAddr),
		tasks:          make(map[string]*task.TaskSession),
		runnerID:       runnerID,
		startedAt:      time.Now().UTC(),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, cfg.Runner.MaxConcurrent),
	}, nil
}

func selectHarness(cfg *config.Config) harness.Harness {
	switch cfg.Execution.Harness {
	case "claude-code":
		return claude.New()
	case "codex":
		return opencode.New()
	default:
		return opencode.New()
	}
}

func (r *Runner) executionMode() string {
	if os.Getenv("RUNNER_LOCAL") == "true" {
		return "local"
	}
	if mode := os.Getenv("RUNNER_MODE"); mode != "" {
		return mode
	}
	return "kata"
}

func truncateSummary(s string) string {
	if len(s) > maxSummaryBytes {
		return s[:maxSummaryBytes] + "\n... (truncated)"
	}
	return s
}

func (r *Runner) Start(ctx context.Context) error {
	if r.executionMode() == "kata" {
		allowed := append([]string(nil), r.cfg.Proxy.AllowedDomains...)
		if r.cfg.ChetterMCP.URL != "" {
			if u, err := url.Parse(r.cfg.ChetterMCP.URL); err == nil && u.Host != "" {
				allowed = append(allowed, u.Host)
				slog.Info("added chetter MCP domain to proxy allowlist", "host", u.Host)
			}
		}
		r.proxy = network.NewProxy(r.cfg.Proxy.ListenAddr, allowed, r.cfg.Proxy.BlockedDomains)
		go func() {
			if err := r.proxy.Start(); err != nil {
				slog.Error("proxy error", "err", err)
			}
		}()
		slog.Info("proxy started", "addr", r.cfg.Proxy.ListenAddr)

		r.dnsProxy = network.NewDNSProxy(r.cfg.DNS.ListenAddr, r.cfg.DNS.Upstream, r.cfg.DNS.BlockedDomains)
		go func() {
			if err := r.dnsProxy.Start(); err != nil {
				slog.Error("dns error", "err", err)
			}
		}()
		if err := network.EnableIPForwarding(); err != nil {
			slog.Warn("could not enable IP forwarding", "err", err)
		}
	} else {
		slog.Info("skipping proxy/dns (non-kata mode)")
	}

	return r.startConnectRPC(ctx)
}

func (r *Runner) publishStatus(taskID, status, message string, artifacts []string) {
	resp := task.TaskResponse{
		TaskID:    taskID,
		Status:    status,
		Artifacts: artifacts,
	}
	r.decorateTaskResponse(&resp, nil, "")
	r.finishStatusResponse(&resp, status, message)
	r.publishTaskResponse(resp)
}

func (r *Runner) publishStatusForRequest(req task.TaskRequest, status, message string, artifacts []string) {
	resp := task.TaskResponse{
		TaskID:    req.TaskID,
		Status:    status,
		Artifacts: artifacts,
	}
	r.decorateTaskResponseForRequest(&resp, req, "")
	r.finishStatusResponse(&resp, status, message)
	r.publishTaskResponse(resp)
}

func (r *Runner) finishStatusResponse(resp *task.TaskResponse, status, message string) {
	if status != "running" {
		resp.StartedAt = time.Now()
		resp.EndedAt = time.Now()
	}
	if status == "error" || status == "cancelled" {
		resp.Error = message
	} else {
		resp.Summary = message
	}
}

func (r *Runner) publishTaskResponse(resp task.TaskResponse) {
	r.reportTaskResponse(resp)
}

func (r *Runner) decorateTaskResponse(resp *task.TaskResponse, env map[string]string, sessionID string) {
	if env == nil {
		env = map[string]string{}
	}
	if resp.ProviderID == "" {
		resp.ProviderID = envValue(env, "LLM_PROVIDER", "")
	}
	if resp.ModelID == "" {
		resp.ModelID = envValue(env, "LLM_MODEL_CODER", "")
	}
	if resp.VariantID == "" {
		resp.VariantID = envValue(env, "LLM_VARIANT", "")
	}
	if resp.OpenCodeSessionID == "" {
		resp.OpenCodeSessionID = sessionID
	}
	if resp.RunnerImageDigest == "" {
		resp.RunnerImageDigest = os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST")
	}
}

func (r *Runner) decorateTaskResponseForRequest(resp *task.TaskResponse, req task.TaskRequest, sessionID string) {
	if resp.ProviderID == "" {
		resp.ProviderID = req.ProviderID
	}
	if resp.ModelID == "" {
		resp.ModelID = req.ModelID
	}
	if resp.ProviderID == "" && strings.Contains(resp.ModelID, "/") {
		parts := strings.SplitN(resp.ModelID, "/", 2)
		resp.ProviderID = parts[0]
		resp.ModelID = parts[1]
	}
	if resp.VariantID == "" {
		resp.VariantID = req.VariantID
	}
	r.decorateTaskResponse(resp, req.Env, sessionID)
}

// publishActivityEvent is kept as a no-op for lifecycle call sites that only
// need task status reporting through ConnectRPC.
func (r *Runner) publishActivityEvent(category, action, description, status, details string, durationMs int64) {
}

func (r *Runner) recordTerminalStatus(taskID, status string) {
	if taskID == "" || (status != "done" && status != "error" && status != "cancelled") {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminalTasks == nil {
		r.terminalTasks = make(map[string]struct{})
	}
	if _, ok := r.terminalTasks[taskID]; ok {
		return
	}
	r.terminalTasks[taskID] = struct{}{}
	if status == "done" {
		r.totalCompleted++
		return
	}
	if status == "cancelled" {
		return
	}
	r.totalErrors++
}

func (r *Runner) stopNetwork() {
	if r.dnsProxy != nil {
		if err := r.dnsProxy.Stop(); err != nil {
			slog.Error("dns stop error", "err", err)
		}
	}
	if r.proxy != nil {
		if err := r.proxy.Stop(); err != nil {
			slog.Error("proxy stop error", "err", err)
		}
	}
}
