// Package controller orchestrates agent execution: claiming tasks via
// ConnectRPC, provisioning isolated workspaces and backend-specific agents,
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultTaskTimeoutSec  = 3600
	maxSummaryBytes        = 8000
	serveReadyTimeout      = 15 * time.Second
	servePollInterval      = 500 * time.Millisecond
	serveHTTPTimeout       = 2 * time.Second
	workspacePruneInterval = 10 * time.Minute

	// taskContainerReapInterval is how often the periodic container reaper
	// looks for chetter-task-* containers that no longer belong to any live
	// task. It is the safety net that bounds sandbox leaks even when the
	// inline teardown path fails (host pressure, runner crash, docker daemon
	// slowness). See issue #418.
	taskContainerReapInterval = 5 * time.Minute
	// taskContainerMinAge is the grace period before the reaper considers a
	// container abandoned. It must comfortably exceed the longest inline
	// teardown (dockerAbortTimeout + containerCleanupTimeout + session export)
	// so the reaper never races a task that is still cleaning up after itself.
	taskContainerMinAge = 10 * time.Minute
	// taskContainerReapTimeout bounds one reaper pass so a wedged docker
	// daemon cannot stall the reaper goroutine forever.
	taskContainerReapTimeout = 2 * time.Minute
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
	// forcedExit is set when waitDrain force-cancelled in-flight tasks after
	// the drain deadline expired (see issue #97). main.go reads it via
	// ForcedExit to exit with a non-zero status after a forced termination.
	forcedExit atomic.Bool
	// lastAdmissionPause records the most recent host-pressure admission state
	// observed by the claim loop ("" = healthy, otherwise the admission-paused
	// status). Used to log pause/resume transitions exactly once. See issue
	// #397.
	lastAdmissionPause atomic.Value
	// hostSampler returns the host resource snapshot the claim gate and
	// heartbeat status evaluate. nil samples /proc directly; tests inject a
	// stub to simulate a low-memory or overloaded host. See issue #397.
	hostSampler func() resourceSnapshot
	kubeClient  kubernetes.Interface
	kubeConfig  *rest.Config
	// drainHardKillTimeout overrides CHETTER_DRAIN_HARD_KILL_TIMEOUT_SEC in
	// tests. Zero means "use the environment/default" (see drainHardKillTimeout).
	drainHardKillTimeout time.Duration
	// drainHardKillDeadline is the absolute deadline of the forced-cleanup
	// phase, set by waitForTaskCleanup when waitDrain force-cancels tasks.
	// Terminal report retries are clamped to it so reporting provably
	// finishes before the runner exits. See issue #313.
	drainHardKillDeadline time.Time
	// taskWG tracks in-flight runTask goroutines. The drain cleanup phase
	// waits on it so the runner never exits while a task goroutine is
	// mid-teardown (sandbox teardown, workspace destroy, session-export
	// flush) or mid terminal report delivery. See issue #313.
	taskWG sync.WaitGroup
	// reportWG tracks in-flight terminal ReportTaskEvents deliveries.
	// Terminal reports are published synchronously inside runTask, so once
	// taskWG drains this is necessarily empty; the drain cleanup phase joins
	// on it explicitly so no report path can be abandoned at exit. See issue
	// #313.
	reportWG sync.WaitGroup
	// reportDelivered records execution IDs whose terminal report was
	// accepted by the server (ReportTaskEvents returned success). The
	// hard-kill audit log compares it against in-flight tasks to report lost
	// results. See issue #313.
	reportDelivered map[string]bool
	// sandbox holds cumulative runtime sandbox (gVisor/runsc) metrics for
	// isolated executions, surfaced in heartbeats and exposed via the
	// server's Prometheus endpoint. See issue #302.
	sandbox *sandboxMetrics
}

func NewRunner(cfg *config.Config) (*Runner, error) {
	if cfg.Execution.Backend == "kubernetes" && sanitizeSubjectToken(os.Getenv("RUNNER_ID")) == "" {
		return nil, fmt.Errorf("RUNNER_ID is required in kubernetes mode and must uniquely identify the runner Pod")
	}
	runnerID, err := newRunnerID(cfg.Runner.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	r := &Runner{
		cfg:             cfg,
		defaultHarness:  cfg.Execution.Harness,
		harnessFactory:  selectHarnessByName,
		wsManager:       workspace.NewManager(cfg.Runner.WorkspaceRoot),
		tasks:           make(map[string]*task.TaskSession),
		tasksChanged:    make(chan struct{}),
		runnerID:        runnerID,
		startedAt:       time.Now().UTC(),
		terminalTasks:   make(map[string]struct{}),
		cancelledTasks:  make(map[string]struct{}),
		reportDelivered: make(map[string]bool),
		sem:             make(chan struct{}, cfg.Runner.MaxConcurrent+1),
		sandbox:         newSandboxMetrics(),
	}
	if cfg.Execution.Backend == "kubernetes" {
		if err := r.initializeKubernetesClient(); err != nil {
			return nil, err
		}
	}
	return r, nil
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
	return r.cfg.Execution.Backend
}

func truncateSummary(s string) string {
	if len(s) > maxSummaryBytes {
		return s[:maxSummaryBytes] + "\n... (truncated)"
	}
	return s
}

func (r *Runner) Start(ctx context.Context) error {
	mode := r.executionMode()
	// The container sweep and reaper run in startConnectRPC once the RPC
	// client exists, because only the control plane can confirm which
	// chetter-task-* containers on the shared Docker daemon are still needed.
	// See issue #418.
	if mode == "docker" {
		slog.Info("orphaned task containers will be reconciled after RPC registration")
	}
	if mode != "local" {
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
		if runnerIP := r.runnerHostIP(); runnerIP != "" {
			allowed = append(allowed, runnerIP)
			slog.Info("added runner IP to proxy allowlist", "host", runnerIP)
		}
		r.proxy = network.NewProxy(r.cfg.Proxy.ListenAddr, allowed, r.cfg.Proxy.BlockedDomains)
		if err := r.proxy.Start(); err != nil {
			return fmt.Errorf("start proxy: %w", err)
		}
		slog.Info("proxy started", "addr", r.cfg.Proxy.ListenAddr)
		if r.cfg.ChetterMCP.URL != "" {
			relayAddr, err := network.NarrowListenAddr(r.cfg.ChetterMCP.RelayListenAddr, r.runnerHostIP())
			if err != nil {
				r.stopNetwork()
				return fmt.Errorf("configure Chetter MCP relay listener: %w", err)
			}
			relay, err := network.NewMCPRelay(relayAddr, r.cfg.ChetterMCP.URL, r.cfg.ChetterMCP.AuthToken)
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
	if mode == "kubernetes" {
		if err := r.verifyAndReconcileKubernetes(ctx); err != nil {
			r.stopNetwork()
			return err
		}
	}

	return r.startConnectRPC(ctx)
}

func (r *Runner) publishStatusForRequest(req task.TaskRequest, status, message string, artifacts []string) {
	r.publishStatusWithToken(req, status, message, artifacts, task.TokenUsage{})
}

// publishStatusWithErrorCategory is like publishStatusForRequest but forces a
// specific error_category on terminal error responses instead of deriving it
// from the message text. Used by the claim-time isolation gate so the control
// plane sees a clear isolation_unavailable classification. See issue #291.
func (r *Runner) publishStatusWithErrorCategory(req task.TaskRequest, status, message, errorCategory string, artifacts []string) {
	resp := task.TaskResponse{
		TaskID:         req.TaskID,
		ExecutionID:    req.ExecutionID,
		AgentSessionID: req.AgentSessionID,
		UserPromptID:   req.UserPromptID,
		Status:         status,
		Artifacts:      artifacts,
		ErrorCategory:  errorCategory,
	}
	r.decorateTaskResponseForRequest(&resp, req, "")
	r.finishStatusResponse(&resp, status, message)
	r.publishTaskResponse(resp)
}

// publishStatusWithToken is like publishStatusForRequest but carries a token
// usage delta that the server accumulates into the running task totals.
func (r *Runner) publishStatusWithToken(req task.TaskRequest, status, message string, artifacts []string, tokenUsage task.TokenUsage) {
	resp := task.TaskResponse{
		TaskID:         req.TaskID,
		ExecutionID:    req.ExecutionID,
		AgentSessionID: req.AgentSessionID,
		UserPromptID:   req.UserPromptID,
		Status:         status,
		Artifacts:      artifacts,
		TokenUsage:     tokenUsage,
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
	case strings.Contains(lower, "sandbox failed to start"), strings.Contains(lower, "sandbox_start_failed"):
		return "sandbox_start_failed"
	case strings.Contains(lower, "sandbox crashed"), strings.Contains(lower, "sandbox_crashed"):
		return "sandbox_crashed"
	case strings.Contains(lower, "budget"), strings.Contains(lower, "cost limit"), strings.Contains(lower, "max budget"):
		return "budget_exceeded"
	case strings.Contains(lower, "oomkilled"), strings.Contains(lower, "out of memory"), strings.Contains(lower, "memory limit"), strings.Contains(lower, "resource limit"), strings.Contains(lower, "cgroup memory"):
		return "resource_limit"
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
	redactRunnerMCPToken(&resp)
	r.reportTaskResponse(resp)
}

func redactRunnerMCPToken(resp *task.TaskResponse) {
	if resp.RunnerMCPToken == "" {
		return
	}
	const replacement = "[REDACTED]"
	resp.Summary = strings.ReplaceAll(resp.Summary, resp.RunnerMCPToken, replacement)
	resp.Error = strings.ReplaceAll(resp.Error, resp.RunnerMCPToken, replacement)
	resp.SessionExport = strings.ReplaceAll(resp.SessionExport, resp.RunnerMCPToken, replacement)
	for i := range resp.Artifacts {
		resp.Artifacts[i] = strings.ReplaceAll(resp.Artifacts[i], resp.RunnerMCPToken, replacement)
	}
	resp.RunnerMCPToken = ""
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
	resp.ClaimID = req.ClaimID
	resp.AgentSessionID = req.AgentSessionID
	resp.RunnerMCPToken = req.RunnerMCPToken
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

// containerReapCandidate is a chetter-task-* container the reaper is
// considering for removal, resolved from its Docker labels.
type containerReapCandidate struct {
	Name          string
	RunnerID      string
	TaskID        string
	ExecutionID   string
	WorkspacePath string
}

// sweepOrphanedTaskContainers removes chetter-task-* containers that the
// control plane confirms no longer back live work. r.runCtx must be set (the
// sweep needs the RPC client). now bounds the age filter so the reaper never
// races a task that is still tearing itself down.
//
// Ownership is deliberately not used to pre-filter candidates. A container
// leaked by a crashed runner instance keeps that instance's runner ID, and two
// runners can share one Docker daemon (wowbagger runs two), so the runner
// cannot distinguish "my orphan" from "my sibling's live sandbox" locally.
// The control plane answers with container_scope, which protects every live
// attempt, retained session, and ready checkpoint regardless of owner. See
// issue #418.
func (r *Runner) sweepOrphanedTaskContainers(ctx context.Context) {
	if r.executionMode() != "docker" || r.rpcClient == nil || r.runCtx == nil {
		return
	}
	sweepCtx, cancel := context.WithTimeout(ctx, taskContainerReapTimeout)
	defer cancel()

	candidates, err := r.listTaskContainers(sweepCtx)
	if err != nil {
		slog.Warn("container reaper could not list task containers", "err", err)
		return
	}
	if len(candidates) == 0 {
		return
	}

	ages := containerAges(sweepCtx, candidates)
	removable := make([]containerReapCandidate, 0, len(candidates))
	verdicts := make([]*runnerv1.WorkspaceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		// Never touch a container this runner is actively managing.
		if r.isActiveExecution(candidate.ExecutionID) {
			continue
		}
		// Give inline teardown its full grace period before stepping in. An
		// unknown age counts as "too young": leaking for one more pass is
		// cheaper than racing a teardown or a sibling's fresh sandbox.
		age, known := ages[candidate.Name]
		if !known || age < taskContainerMinAge {
			continue
		}
		removable = append(removable, candidate)
		verdicts = append(verdicts, &runnerv1.WorkspaceCandidate{
			TaskId:        candidate.TaskID,
			ExecutionId:   candidate.ExecutionID,
			WorkspacePath: candidate.WorkspacePath,
		})
	}
	if len(verdicts) == 0 {
		return
	}

	// Drop containers the control plane says may still be needed. A ready
	// checkpoint or a paused/resumable session must outlive its container's
	// owning runner, so absence from the DB is not enough to reap. The
	// verdict comes from the shared control plane (not local state) because
	// two runners can share one Docker daemon.
	safe, err := r.containerReapVerdict(sweepCtx, verdicts)
	if err != nil {
		// Fail closed: without a control-plane verdict, leaking a container is
		// strictly better than killing a live sibling's sandbox.
		slog.Warn("container reaper skipped pass: control plane verdict unavailable", "candidates", len(verdicts), "err", err)
		return
	}

	removed, skipped := 0, 0
	for _, candidate := range removable {
		if _, ok := safe[candidate.TaskID+"\x00"+candidate.ExecutionID]; !ok {
			skipped++
			continue
		}
		// Verify no process checkpoint owns the container. A container with a
		// real checkpoint must never be force-removed here even if the control
		// plane lost track of it (docker refuses checkpoints for containers
		// that lack one, so a positive answer is authoritative).
		if containerHasCheckpoint(sweepCtx, candidate.Name) {
			slog.Info("container reaper skipping container with checkpoint", "container", candidate.Name)
			skipped++
			continue
		}
		slog.Info("container reaper removing abandoned task container", "container", candidate.Name, "task_id", candidate.TaskID, "execution_id", candidate.ExecutionID, "owner_runner", candidate.RunnerID)
		removeTaskContainer(candidate.Name)
		removed++
	}
	if removed > 0 || skipped > 0 {
		slog.Info("container reaper pass complete", "removed", removed, "protected", skipped, "candidates", len(verdicts))
	}
}

// containerReapVerdict asks the control plane which of the candidate
// containers are safe to remove, keyed by task and execution ID. Candidates
// created before the workspace-path label existed are checked task-wide, which
// is the conservative direction.
func (r *Runner) containerReapVerdict(ctx context.Context, verdicts []*runnerv1.WorkspaceCandidate) (map[string]struct{}, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	resp, err := r.rpcClient.PruneWorkspaces(ctxWithTimeout, connect.NewRequest(&runnerv1.PruneWorkspacesRequest{
		RunnerId:       r.runnerID,
		Candidates:     verdicts,
		ContainerScope: true,
	}))
	if err != nil {
		return nil, err
	}
	safe := make(map[string]struct{}, len(resp.Msg.SafeToDelete))
	for _, key := range resp.Msg.SafeToDelete {
		if key != nil {
			safe[key.TaskId+"\x00"+key.ExecutionId] = struct{}{}
		}
	}
	return safe, nil
}

// isActiveExecution reports whether this runner currently owns a live task
// session for the execution, in which case the container must not be touched.
func (r *Runner) isActiveExecution(executionID string) bool {
	if executionID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, active := r.tasks[executionID]
	return active
}

// listTaskContainers returns every chetter-task-* container with its identity
// labels, including created-but-never-started and exited containers (a runner
// killed mid-teardown leaves exactly those behind).
func (r *Runner) listTaskContainers(ctx context.Context) ([]containerReapCandidate, error) {
	format := "{{.Names}}\t{{.Label \"chetter.runner_id\"}}\t{{.Label \"chetter.task_id\"}}\t{{.Label \"chetter.execution_id\"}}\t{{.Label \"chetter.workspace_path\"}}"
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "name=chetter-task-", "--format", format).Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	var candidates []containerReapCandidate
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Accept fewer than five fields: a missing trailing label (for example
		// chetter.workspace_path on containers created before it existed) can
		// come through with the field absent entirely rather than empty, and
		// skipping such a container would silently defeat the safety net.
		fields := strings.Split(line, "\t")
		for len(fields) < 5 {
			fields = append(fields, "")
		}
		candidate := containerReapCandidate{
			Name:          strings.TrimPrefix(strings.TrimSpace(fields[0]), "/"),
			RunnerID:      strings.TrimSpace(fields[1]),
			TaskID:        strings.TrimSpace(fields[2]),
			ExecutionID:   strings.TrimSpace(fields[3]),
			WorkspacePath: strings.TrimSpace(fields[4]),
		}
		if candidate.Name == "" || candidate.TaskID == "" || candidate.ExecutionID == "" {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// containerAges resolves container creation times in a single docker inspect
// call. Containers whose age cannot be determined are omitted, which the
// caller treats as "too young to reap".
func containerAges(ctx context.Context, candidates []containerReapCandidate) map[string]time.Duration {
	ages := make(map[string]time.Duration, len(candidates))
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Name)
	}
	args := append([]string{"inspect", "-f", "{{.Name}}\t{{.Created}}"}, names...)
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return ages
	}
	now := time.Now()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(fields[0]), "/")
		created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(fields[1]))
		if err != nil || name == "" {
			continue
		}
		ages[name] = now.Sub(created)
	}
	return ages
}

// containerHasCheckpoint reports whether a process checkpoint owns the
// container. docker checkpoint ls prints a header line plus one line per
// checkpoint, so any non-header output means a checkpoint exists. Errors (for
// example a runtime without checkpoint support) report false so ordinary
// teardown is not blocked.
func containerHasCheckpoint(ctx context.Context, name string) bool {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctxWithTimeout, "docker", "checkpoint", "ls", name).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(strings.ToUpper(trimmed), "CHECKPOINT") {
			return true
		}
	}
	return false
}

// reapTaskContainersPeriodically is the container-leak safety net: it bounds
// how long any chetter-task-* sandbox can survive its task, even when inline
// teardown failed (host memory pressure, runner crash, slow docker daemon).
// Without it a failed removal leaks an 8 GB gVisor sandbox permanently and the
// host spirals into swap thrash. See issue #418.
func (r *Runner) reapTaskContainersPeriodically(ctx context.Context) {
	ticker := time.NewTicker(taskContainerReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.sweepOrphanedTaskContainers(ctx)
		case <-ctx.Done():
			return
		}
	}
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
