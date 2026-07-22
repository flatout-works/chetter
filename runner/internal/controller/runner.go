// Package controller orchestrates agent execution — claiming tasks via
// ConnectRPC, provisioning isolated workspaces and Docker containers,
// exposing MCP tools, and publishing results.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/flatout-works/chetter/runner/harness/codewhale"
	"github.com/flatout-works/chetter/runner/harness/codex"
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
	harnessFactory func(string) harness.Harness
	wsManager      *workspace.Manager
	proxy          *network.TransparentProxy
	dnsProxy       *network.DNSProxy
	mcpRelay       *network.MCPRelay
	rpcClient      runnerRPCClient
	claimClient    runnerRPCClient
	runCtx         context.Context
	mu             sync.Mutex
	tasks          map[string]*task.TaskSession
	tasksChanged   chan struct{}
	runnerID       string
	startedAt      time.Time

	totalStarted   int64
	totalCompleted int64
	totalErrors    int64
	terminalTasks  map[string]struct{}
	cancelledTasks map[string]struct{}
	sem            chan struct{}

	draining atomic.Bool
}

func NewRunner(cfg *config.Config) (*Runner, error) {
	runnerID, err := newRunnerID(cfg.Runner.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	return &Runner{
		cfg:            cfg,
		defaultHarness: cfg.Execution.Harness,
		harnessFactory: selectHarnessByName,
		wsManager:      workspace.NewManager(cfg.Runner.WorkspaceRoot),
		tasks:          make(map[string]*task.TaskSession),
		tasksChanged:   make(chan struct{}),
		runnerID:       runnerID,
		startedAt:      time.Now().UTC(),
		terminalTasks:  make(map[string]struct{}),
		cancelledTasks: make(map[string]struct{}),
		sem:            make(chan struct{}, cfg.Runner.MaxConcurrent),
	}, nil
}

func selectHarnessByName(name string) harness.Harness {
	switch name {
	case "claude-code":
		return claude.New()
	case "pi":
		return pi.New()
	case "codewhale":
		return codewhale.New()
	case "codex":
		return codex.New()
	default:
		return opencode.New()
	}
}

func (r *Runner) harnessFor(name string) harness.Harness {
	if name == "" {
		name = r.defaultHarness
	}
	factory := r.harnessFactory
	if factory == nil {
		factory = selectHarnessByName
	}
	return factory(name)
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
		out, err := exec.Command("docker", "ps", "-a", "--filter", "name=chetter-task-", "--filter", "label=chetter.runner_id="+r.runnerID, "--format", "{{.Names}}").Output()
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
			if u, err := url.Parse(r.cfg.ChetterMCP.URL); err == nil && u.Host != "" {
				allowHost := u.Hostname()
				if allowHost == "" {
					allowHost = u.Host
				}
				allowed = append(allowed, allowHost)
				slog.Info("added chetter MCP domain to proxy allowlist", "host", allowHost)
			}
		}
		// Allow dev containers to reach the runner's own MCP server via HTTP_PROXY.
		if runnerIP := hostIP(runcNetwork()); runnerIP != "" {
			allowed = append(allowed, runnerIP)
			slog.Info("added runner IP to proxy allowlist", "host", runnerIP)
		}
		r.proxy = network.NewProxy(r.cfg.Proxy.ListenAddr, allowed, r.cfg.Proxy.BlockedDomains)
		if err := r.proxy.Start(); err != nil {
			return fmt.Errorf("start proxy: %w", err)
		}
		slog.Info("proxy started", "addr", r.cfg.Proxy.ListenAddr)
		if r.cfg.ChetterMCP.URL != "" {
			relay, err := network.NewMCPRelay(r.cfg.ChetterMCP.RelayListenAddr, r.cfg.ChetterMCP.URL, r.cfg.ChetterMCP.AuthToken)
			if err != nil {
				r.stopNetwork()
				return fmt.Errorf("create Chetter MCP relay: %w", err)
			}
			r.mcpRelay = relay
			if err := relay.Start(); err != nil {
				r.stopNetwork()
				return fmt.Errorf("start Chetter MCP relay: %w", err)
			}
			slog.Info("Chetter MCP relay started", "addr", relay.Addr(), "target", r.cfg.ChetterMCP.URL)
		}

		dnsAllowed := append([]string(nil), r.cfg.DNS.AllowedDomains...)
		dnsRecords := make(map[string][]net.IP)
		if r.cfg.ChetterMCP.URL != "" {
			if u, err := url.Parse(r.cfg.ChetterMCP.URL); err == nil && u.Hostname() != "" {
				host := u.Hostname()
				if len(dnsAllowed) > 0 {
					dnsAllowed = append(dnsAllowed, host)
				}
				if ips, lookupErr := net.LookupIP(host); lookupErr != nil {
					slog.Warn("resolve chetter MCP DNS record", "host", host, "err", lookupErr)
				} else {
					dnsRecords[host] = ips
					slog.Info("configured chetter MCP DNS record", "host", host, "ips", ips)
				}
			}
		}
		r.dnsProxy = network.NewDNSProxy(r.cfg.DNS.ListenAddr, r.cfg.DNS.Upstream, dnsAllowed, r.cfg.DNS.BlockedDomains, dnsRecords)
		if err := r.dnsProxy.Start(); err != nil {
			r.stopNetwork()
			return fmt.Errorf("start DNS proxy: %w", err)
		}
	} else {
		slog.Info("skipping proxy/dns (local mode)")
	}

	return r.startConnectRPC(ctx)
}

func (r *Runner) publishStatusForRequest(req task.TaskRequest, status, message string, artifacts []string) {
	resp := task.TaskResponse{
		TaskID:         req.TaskID,
		ExecutionID:    req.ExecutionID,
		AgentSessionID: req.AgentSessionID,
		UserPromptID:   req.UserPromptID,
		Status:         status,
		Artifacts:      artifacts,
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
	case isPromptTransportFailureMessage(lower):
		return "transport_error"
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

func isPromptTransportFailureMessage(lower string) bool {
	if !strings.Contains(lower, "post /message") {
		return false
	}
	return strings.Contains(lower, "eof") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "server closed") ||
		strings.Contains(lower, "connection refused")
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
	resp.AgentSessionID = req.AgentSessionID
	resp.UserPromptID = req.UserPromptID
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

func (r *Runner) recordTerminalStatus(executionID, status string) {
	if executionID == "" || (status != "done" && status != "error" && status != "cancelled") {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminalTasks == nil {
		r.terminalTasks = make(map[string]struct{})
	}
	if _, ok := r.terminalTasks[executionID]; ok {
		return
	}
	r.terminalTasks[executionID] = struct{}{}
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
	if r.mcpRelay != nil {
		if err := r.mcpRelay.Stop(); err != nil {
			slog.Error("Chetter MCP relay stop error", "err", err)
		}
	}
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

	candidates := make([]*runnerv1.WorkspaceCandidate, 0)
	for _, taskEntry := range entries {
		if !taskEntry.IsDir() || !strings.HasPrefix(taskEntry.Name(), "task_") {
			continue
		}
		executions, err := os.ReadDir(filepath.Join(root, taskEntry.Name()))
		if err != nil {
			return fmt.Errorf("read task workspace %s: %w", taskEntry.Name(), err)
		}
		for _, executionEntry := range executions {
			if !executionEntry.IsDir() || !strings.HasPrefix(executionEntry.Name(), "exec_") {
				continue
			}
			candidates = append(candidates, &runnerv1.WorkspaceCandidate{
				TaskId: taskEntry.Name(), ExecutionId: executionEntry.Name(),
				WorkspacePath: filepath.Join(root, taskEntry.Name(), executionEntry.Name(), "workspace"),
			})
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	slog.Info("checking orphaned execution workspaces", "count", len(candidates), "root", root)
	resp, err := r.rpcClient.PruneWorkspaces(ctx, connect.NewRequest(&runnerv1.PruneWorkspacesRequest{
		RunnerId: r.runnerID, Candidates: candidates,
	}))
	if err != nil {
		return fmt.Errorf("prune workspaces rpc: %w", err)
	}

	safeToDelete := make(map[string]bool, len(resp.Msg.SafeToDelete))
	for _, key := range resp.Msg.SafeToDelete {
		if key != nil {
			safeToDelete[key.TaskId+"\x00"+key.ExecutionId] = true
		}
	}

	deleted := 0
	skipped := 0
	for _, candidate := range candidates {
		key := candidate.TaskId + "\x00" + candidate.ExecutionId
		if !safeToDelete[key] {
			skipped++
			continue
		}
		r.mu.Lock()
		_, active := r.tasks[candidate.ExecutionId]
		r.mu.Unlock()
		if active {
			skipped++
			continue
		}
		dir := filepath.Join(root, candidate.TaskId, candidate.ExecutionId)
		if err := r.wsManager.Destroy(candidate.TaskId, candidate.ExecutionId); err != nil {
			slog.Warn("failed to prune workspace", "taskID", candidate.TaskId, "executionID", candidate.ExecutionId, "dir", dir, "err", err)
			continue
		}
		deleted++
	}

	slog.Info("workspace prune complete", "deleted", deleted, "skipped", skipped, "total", len(candidates))
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
