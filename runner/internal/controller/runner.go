// Package controller orchestrates agent execution — receiving task requests
// via NATS, provisioning isolated workspaces and Kata Containers, exposing MCP
// tools, and publishing results.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/config"
	"github.com/flatout-works/devfleet/runner/internal/containerd"
	runnerNats "github.com/flatout-works/devfleet/runner/internal/nats"
	"github.com/flatout-works/devfleet/runner/internal/network"
	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/flatout-works/devfleet/runner/internal/workspace"

	nats "github.com/nats-io/nats.go"
)

const (
	defaultTaskTimeoutSec = 3600
	maxSummaryBytes       = 8000
	serveReadyTimeout     = 15 * time.Second
	servePollInterval     = 500 * time.Millisecond
	serveHTTPTimeout      = 2 * time.Second
	opencodePluginNS      = "devfleet-runner"
)

// Runner orchestrates agent execution — managing task intake, workspace
// provisioning, network isolation, agent lifecycle (local/Kata), and
// result publishing.
type Runner struct {
	cfg        *config.Config
	natsClient *runnerNats.Client
	wsManager  *workspace.Manager
	proxy      *network.TransparentProxy
	dnsProxy   *network.DNSProxy
	bridgeMgr  *network.BridgeManager
	containerd *containerd.Client
	mu         sync.Mutex
	tasks      map[string]*task.TaskSession
	runnerID   string
	startedAt  time.Time

	totalStarted   int64
	totalCompleted int64
	totalErrors    int64
	terminalTasks  map[string]struct{}
	sem            chan struct{}
}

// NewRunner creates a runner with the given configuration and NATS client.
// Use Start to begin processing tasks.
func NewRunner(cfg *config.Config, nc *runnerNats.Client) (*Runner, error) {
	cd := containerd.NewClient(opencodePluginNS)
	return &Runner{
		cfg:           cfg,
		natsClient:    nc,
		wsManager:     workspace.NewManager(cfg.Runner.WorkspaceRoot),
		containerd:    cd,
		bridgeMgr:     network.NewBridgeManager(cfg.Proxy.ListenAddr, cfg.DNS.ListenAddr),
		tasks:         make(map[string]*task.TaskSession),
		runnerID:      newRunnerID(),
		startedAt:     time.Now().UTC(),
		terminalTasks: make(map[string]struct{}),
		sem:           make(chan struct{}, cfg.Runner.MaxConcurrent),
	}, nil
}

// executionMode returns the agent execution mode.
//   - "local": plain process on the host (RUNNER_LOCAL=true)
//   - "docker": OpenCode inside a Docker/Podman container (RUNNER_MODE=docker)
//   - "kata": OpenCode inside a Kata micro-VM (default for cloud/devfleet)
func (r *Runner) executionMode() string {
	if os.Getenv("RUNNER_LOCAL") == "true" {
		return "local"
	}
	if mode := os.Getenv("RUNNER_MODE"); mode != "" {
		return mode
	}
	return "kata"
}

// truncateSummary truncates s to maxSummaryBytes with an ellipsis marker.
func truncateSummary(s string) string {
	if len(s) > maxSummaryBytes {
		return s[:maxSummaryBytes] + "\n... (truncated)"
	}
	return s
}

// Start begins listening for NATS tasks and serving the proxy.
func (r *Runner) Start(ctx context.Context) error {
	// Start transparent proxy and DNS (Kata mode only — Docker and local don't need them).
	if r.executionMode() == "kata" {
		allowed := append([]string(nil), r.cfg.Proxy.AllowedDomains...)
		if r.cfg.DevfleetMCP.URL != "" {
			if u, err := url.Parse(r.cfg.DevfleetMCP.URL); err == nil && u.Host != "" {
				allowed = append(allowed, u.Host)
				slog.Info("added devfleet MCP domain to proxy allowlist", "host", u.Host)
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
		slog.Info("skipping proxy/dns (local mode)")
	}

	// Subscribe to tasks.
	var sub *nats.Subscription
	var err error
	if r.cfg.JetStream.Enabled {
		eventSubjects := []string{
			fmt.Sprintf("%s.>", r.cfg.Runner.ResultSubject),
			"devfleet.activity.>",
		}
		if err := r.natsClient.EnableJetStream(
			r.cfg.JetStream.TaskStream,
			r.cfg.JetStream.EventStream,
			r.cfg.Runner.ListenSubject,
			eventSubjects,
			r.cfg.JetStream.Storage,
		); err != nil {
			return fmt.Errorf("enable jetstream: %w", err)
		}
		sub, err = r.natsClient.QueueSubscribeManualAck(
			r.cfg.Runner.ListenSubject,
			r.cfg.JetStream.TaskQueue,
			r.cfg.JetStream.TaskDurable,
			time.Duration(r.cfg.JetStream.AckWaitSeconds)*time.Second,
			r.cfg.JetStream.MaxDeliver,
			r.cfg.JetStream.MaxAckPending,
			r.onTask,
		)
	} else {
		sub, err = r.natsClient.Conn.Subscribe(r.cfg.Runner.ListenSubject, r.onTask)
	}
	if err != nil {
		return fmt.Errorf("subscribe tasks: %w", err)
	}
	defer sub.Unsubscribe()

	// If in Kata mode, verify containerd/Kata are installed.
	if r.executionMode() == "kata" {
		if err := containerd.CheckInstall(); err != nil {
			return fmt.Errorf("prerequisites check failed: %w. "+
				"Install containerd + Kata Containers: see README.md", err)
		}
		slog.Info("containerd + Kata runtime verified")
	}

	slog.Info("listening", "subject", r.cfg.Runner.ListenSubject)
	r.publishRunnerHeartbeat("active")
	go r.heartbeatLoop(ctx)

	// Subscribe to task cancellation notifications.
	// Subject is derived from ResultSubject so it works with both global
	// (devfleet.tasks.*.cancel) and project-scoped (devfleet.<projectID>.tasks.*.cancel) topologies.
	cancelSubject := fmt.Sprintf("%s.*.cancel", r.cfg.Runner.ResultSubject)
	cancelSub, cancelErr := r.natsClient.Conn.Subscribe(cancelSubject, r.onCancel)
	if cancelErr != nil {
		slog.Warn("cancel subscription failed (cancellation via NATS unavailable)", "err", cancelErr)
	} else {
		defer cancelSub.Unsubscribe()
	}

	<-ctx.Done()

	slog.Info("shutting down proxy and DNS...")
	r.publishRunnerHeartbeat("stopping")
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
	return ctx.Err()
}

func (r *Runner) onTask(msg *nats.Msg) {
	var req task.TaskRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.Error("bad task request", "err", err)
		if r.cfg.JetStream.Enabled {
			if ackErr := msg.Ack(); ackErr != nil {
				slog.Warn("jetstream ack failed for bad task", "err", ackErr)
			}
		}
		return
	}

	if req.TaskID == "" {
		req.TaskID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	if req.TimeoutSec == 0 {
		req.TimeoutSec = defaultTaskTimeoutSec
	}

	select {
	case r.sem <- struct{}{}:
	default:
		slog.Warn("runner at capacity, nacking task for another runner", "taskID", req.TaskID)
		if r.cfg.JetStream.Enabled && msg != nil {
			if err := msg.Nak(); err != nil {
				slog.Warn("jetstream nak failed", "taskID", req.TaskID, "err", err)
			}
		}
		return
	}

	if r.cfg.JetStream.Enabled && msg != nil {
		if err := msg.Ack(); err != nil {
			slog.Warn("jetstream ack failed for task", "taskID", req.TaskID, "err", err)
		}
	}

	go r.runTask(req, msg)
}

// onCancel handles task cancellation notifications on
// devfleet.tasks.<taskID>.cancel. It cancels the task's context and
// publishes a cancelled status event so the DB is updated promptly.
func (r *Runner) onCancel(msg *nats.Msg) {
	// Extract task ID from subject: devfleet.tasks.<taskID>.cancel
	parts := strings.Split(msg.Subject, ".")
	if len(parts) < 4 {
		return
	}
	taskID := parts[len(parts)-2]
	reason := string(msg.Data)

	r.mu.Lock()
	session, ok := r.tasks[taskID]
	r.mu.Unlock()
	if !ok {
		return
	}

	slog.Info("cancelling task", "taskID", taskID, "reason", reason)
	session.Cancel()
	r.publishStatus(taskID, "cancelled", "cancelled by operator", nil)
}

// publishStatus sends a task status update to NATS on the configured
// result subject.
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
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("failed to marshal status response", "err", err)
		return
	}
	subject := fmt.Sprintf("%s.%s.status", r.cfg.Runner.ResultSubject, resp.TaskID)
	if err := r.natsClient.Publish(subject, data); err != nil {
		slog.Error("failed to publish status", "err", err)
		return
	}
	if err := r.natsClient.Conn.Flush(); err != nil {
		slog.Error("failed to flush status", "err", err)
		return
	}
	r.recordTerminalStatus(resp.TaskID, resp.Status)
	slog.Info("published status", "taskID", resp.TaskID, "status", resp.Status)
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
		resp.RunnerImageDigest = os.Getenv("DEVFLEET_RUNNER_IMAGE_DIGEST")
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

// projectID extracts the project identifier from the listen subject.
func (r *Runner) projectID() string {
	parts := strings.Split(r.cfg.Runner.ListenSubject, ".")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

// publishActivityEvent emits a structured activity event over NATS
// so the builder can persist it in the local SQLite store.
func (r *Runner) publishActivityEvent(category, action, description, status, details string, durationMs int64) {
	event := map[string]interface{}{
		"id":          fmt.Sprintf("evt-%d", time.Now().UnixNano()),
		"projectId":   r.projectID(),
		"category":    category,
		"action":      action,
		"description": description,
		"status":      status,
		"details":     details,
		"createdAt":   time.Now().UnixMilli(),
		"durationMs":  durationMs,
		"source":      "agent",
	}
	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("failed to marshal activity event", "err", err)
		return
	}
	subject := fmt.Sprintf("devfleet.activity.%s", r.projectID())
	if err := r.natsClient.Publish(subject, data); err != nil {
		slog.Error("failed to publish activity event", "err", err)
		return
	}
	_ = r.natsClient.Conn.Flush()
	slog.Info("published activity", "category", category, "action", action)
}

// Home and config helpers.

func candidateHomes() []string {
	homes := []string{os.Getenv("HOME")}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
		if u, err := user.Lookup(sudoUser); err == nil {
			homes = append(homes, u.HomeDir)
		} else {
			homes = append(homes, "/home/"+sudoUser)
		}
	}
	return homes
}

func readOpenCodeConfig() ([]byte, string) {
	for _, home := range candidateHomes() {
		if home == "" {
			continue
		}
		for _, path := range []string{
			home + "/.config/opencode/config.json",
			home + "/.opencode/config.json",
		} {
			data, err := os.ReadFile(path)
			if err == nil {
				return data, path
			}
		}
	}
	return []byte("{}"), "<empty>"
}

func copyFirstExisting(label, dst string, candidates func(string) []string) {
	for _, home := range candidateHomes() {
		for _, src := range candidates(home) {
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
				slog.Warn("copy warning", "label", label, "err", err)
				return
			}
			if err := os.WriteFile(dst, data, 0644); err != nil {
				slog.Warn("copy warning", "label", label, "err", err)
				return
			}
			slog.Info("copied state", "label", label, "src", src, "dst", dst, "bytes", len(data))
			return
		}
	}
	slog.Warn("copy no source found", "label", label)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0750)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0640)
	})
}
