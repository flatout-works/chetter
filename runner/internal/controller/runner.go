// Package controller orchestrates agent execution — claiming tasks via
// ConnectRPC, provisioning isolated workspaces and Docker containers,
// exposing MCP tools, and publishing results.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/harness/claude"
	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/harness/pi"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/network"
	"github.com/flatout-works/chetter/runner/internal/task"
	"github.com/flatout-works/chetter/runner/internal/workspace"
)

const (
	defaultTaskTimeoutSec  = 3600
	maxSummaryBytes        = 8000
	serveReadyTimeout      = 15 * time.Second
	servePollInterval      = 500 * time.Millisecond
	serveHTTPTimeout       = 2 * time.Second
	workspacePruneInterval = 10 * time.Minute
)

type Runner struct {
	cfg            *config.Config
	defaultHarness string
	wsManager      *workspace.Manager
	proxy          *network.TransparentProxy
	dnsProxy       *network.DNSProxy
	rpcClient      runnerRPCClient
	claimClient    runnerRPCClient
	runCtx         context.Context
	mu             sync.Mutex
	tasks          map[string]*task.TaskSession
	runnerID       string
	startedAt      time.Time

	totalStarted   int64
	totalCompleted int64
	totalErrors    int64
	terminalTasks  map[string]struct{}
	cancelledTasks map[string]struct{}
	sem            chan struct{}

	draining    atomic.Bool
	drainCh     chan struct{}
	drainedCh   chan struct{}
	drainCtx    context.Context
	drainCancel context.CancelFunc
}

func NewRunner(cfg *config.Config) (*Runner, error) {
	runnerID, err := newRunnerID(cfg.Runner.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	drainCh := make(chan struct{})
	drainedCh := make(chan struct{})
	return &Runner{
		cfg:            cfg,
		defaultHarness: cfg.Execution.Harness,
		wsManager:      workspace.NewManager(cfg.Runner.WorkspaceRoot),
		tasks:          make(map[string]*task.TaskSession),
		runnerID:       runnerID,
		startedAt:      time.Now().UTC(),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, cfg.Runner.MaxConcurrent),
		drainCh:        drainCh,
		drainedCh:      drainedCh,
	}, nil
}

func selectHarnessByName(name string) harness.Harness {
	switch name {
	case "claude-code":
		return claude.New()
	case "pi":
		return pi.New()
	case "codex":
		return opencode.New()
	default:
		return opencode.New()
	}
}

func (r *Runner) harnessFor(name string) harness.Harness {
	if name == "" {
		name = r.defaultHarness
	}
	return selectHarnessByName(name)
}

func (r *Runner) executionMode() string {
	if os.Getenv("RUNNER_LOCAL") == "true" {
		return "local"
	}
	if mode := os.Getenv("RUNNER_MODE"); mode != "" {
		return mode
	}
	return "docker"
}

func truncateSummary(s string) string {
	if len(s) > maxSummaryBytes {
		return s[:maxSummaryBytes] + "\n... (truncated)"
	}
	return s
}

func (r *Runner) Start(ctx context.Context) error {
	mode := r.executionMode()
	if mode != "local" {
		// Clean up orphaned task containers from previous runner instances.
		// When a runner is restarted, the defer in runDockerAgent that runs
		// "docker rm -f" never executes, leaving containers behind.
		slog.Info("cleaning up orphaned task containers")
		out, err := exec.Command("docker", "ps", "-a", "--filter", "name=chetter-task-", "--format", "{{.Names}}").Output()
		if err != nil {
			slog.Warn("failed to list docker containers", "err", err)
		} else {
			for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if name == "" {
					continue
				}
				// Skip containers that have checkpoints (paused for resume).
				chkOut, _ := exec.Command("docker", "checkpoint", "ls", name).CombinedOutput()
				if strings.Contains(string(chkOut), "chetter-checkpoint") {
					slog.Info("skipping orphaned container with checkpoints", "name", name)
					continue
				}
				if err := exec.Command("docker", "rm", "-f", name).Run(); err != nil {
					slog.Warn("failed to remove orphaned container", "name", name, "err", err)
				} else {
					slog.Info("removed orphaned task container", "name", name)
				}
			}
		}

		allowed := append([]string(nil), r.cfg.Proxy.AllowedDomains...)
		if r.cfg.ChetterMCP.URL != "" {
			if u, err := url.Parse(r.cfg.ChetterMCP.URL); err == nil && u.Hostname() != "" {
				allowed = append(allowed, u.Hostname())
				slog.Info("added chetter MCP domain to proxy allowlist", "host", u.Hostname())
			}
		}
		// Allow dev containers to reach the runner's own MCP server via HTTP_PROXY.
		if runnerIP := hostIP(runcNetwork()); runnerIP != "" {
			allowed = append(allowed, runnerIP)
			slog.Info("added runner IP to proxy allowlist", "host", runnerIP)
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
	} else {
		slog.Info("skipping proxy/dns (local mode)")
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
	if isTerminalStatus(status) {
		resp.EndedAt = time.Now()
	}
	if status == "error" || status == "cancelled" {
		resp.Error = message
		if resp.ErrorCategory == "" {
			resp.ErrorCategory = classifyErrorCategory(status, message)
		}
	} else {
		resp.Summary = message
	}
}

func classifyErrorCategory(status, message string) string {
	if status == "cancelled" {
		return "cancelled"
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "budget"), strings.Contains(lower, "cost limit"), strings.Contains(lower, "max budget"):
		return "budget_exceeded"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "context deadline"):
		return "timeout"
	case strings.Contains(lower, "stuck"), strings.Contains(lower, "loop"):
		return "stuck"
	case strings.Contains(lower, "model"), strings.Contains(lower, "llm"), strings.Contains(lower, "rate limit"), strings.Contains(lower, "provider"), strings.Contains(lower, "api error"):
		return "model_error"
	case message == "":
		return "unknown"
	default:
		return "runtime_error"
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

func (r *Runner) pruneOrphanedWorkspaces(ctx context.Context) error {
	root := r.wsManager.Root
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read workspace root: %w", err)
	}

	taskIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "task_") {
			continue
		}
		taskIDs = append(taskIDs, entry.Name())
	}
	if len(taskIDs) == 0 {
		return nil
	}

	slog.Info("checking orphaned workspace directories", "count", len(taskIDs), "root", root)
	resp, err := r.rpcClient.PruneWorkspaces(ctx, connect.NewRequest(&runnerv1.PruneWorkspacesRequest{
		RunnerId: r.runnerID,
		TaskIds:  taskIDs,
	}))
	if err != nil {
		return fmt.Errorf("prune workspaces rpc: %w", err)
	}

	safeToDelete := make(map[string]bool, len(resp.Msg.SafeToDelete))
	for _, id := range resp.Msg.SafeToDelete {
		safeToDelete[id] = true
	}

	deleted := 0
	skipped := 0
	for _, taskID := range taskIDs {
		if !safeToDelete[taskID] {
			skipped++
			continue
		}
		dir := filepath.Join(root, taskID)
		if err := r.wsManager.Destroy(taskID); err != nil {
			slog.Warn("failed to prune workspace", "taskID", taskID, "dir", dir, "err", err)
			continue
		}
		deleted++
	}

	slog.Info("workspace prune complete", "deleted", deleted, "skipped", skipped, "total", len(taskIDs))
	return nil
}

func (r *Runner) pruneWorkspacesPeriodically(ctx context.Context) {
	ticker := time.NewTicker(workspacePruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := r.pruneOrphanedWorkspaces(pruneCtx); err != nil {
				slog.Warn("periodic workspace prune failed", "err", err)
			}
			cancel()
		case <-ctx.Done():
			return
		}
	}
}
