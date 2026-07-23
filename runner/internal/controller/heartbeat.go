package controller

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
)

const heartbeatInterval = 5 * time.Second

// defaultDrainTimeout is the grace period the runner waits for in-flight tasks
// to finish after a drain is triggered (e.g. SIGTERM during a Kubernetes
// rolling deployment). It defaults to 30s to align with Kubernetes' default
// terminationGracePeriodSeconds. Operators whose tasks need longer to wind
// down cleanly, or who run with a larger terminationGracePeriodSeconds, can
// raise it via CHETTER_DRAIN_TIMEOUT_SEC (set it a few seconds below the pod's
// grace period to leave time for the forced task shutdown). See issue #97.
const defaultDrainTimeout = 30 * time.Second

func newRunnerID(workspaceRoot string) (string, error) {
	if value := sanitizeSubjectToken(os.Getenv("RUNNER_ID")); value != "" {
		return value, nil
	}

	idFile := filepath.Join(workspaceRoot, ".runner-id")
	if data, err := os.ReadFile(idFile); err == nil {
		if id := sanitizeSubjectToken(string(data)); id != "" {
			return id, nil
		}
	}

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate runner id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	id := fmt.Sprintf("runner-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])

	if err := os.MkdirAll(workspaceRoot, 0750); err == nil {
		if err := os.WriteFile(idFile, []byte(id+"\n"), 0640); err != nil {
			slog.Warn("failed to persist runner ID", "err", err)
		}
	} else {
		slog.Warn("cannot persist runner ID, workspace root not writable", "root", workspaceRoot, "err", err)
	}

	return id, nil
}

func sanitizeSubjectToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return strings.Trim(b.String(), "_")
}

func (r *Runner) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			status := "active"
			if r.draining.Load() {
				status = "draining"
			}
			r.publishRunnerHeartbeat(status)
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runner) publishRunnerHeartbeat(status string) {
	r.publishRunnerHeartbeatRPC(status)
}

func (r *Runner) runnerInfoProto(status string) *runnerv1.RunnerInfo {
	r.mu.Lock()
	currentExecutions := make([]*runnerv1.RunningExecution, 0, len(r.tasks))
	for executionID, session := range r.tasks {
		currentExecutions = append(currentExecutions, &runnerv1.RunningExecution{
			TaskId: session.TaskID, ExecutionId: executionID,
			AgentSessionId: session.Request.AgentSessionID, UserPromptId: session.Request.UserPromptID,
		})
	}
	totalStarted := r.totalStarted
	totalCompleted := r.totalCompleted
	totalErrors := r.totalErrors
	maxConcurrent := r.cfg.Runner.MaxConcurrent
	availableSlots := maxConcurrent - len(r.sem)
	if availableSlots < 0 {
		availableSlots = 0
	}
	r.mu.Unlock()

	gvisorEnabled := r.cfg.Execution.UseGVisor
	checkpointRestore := false
	runscVersion := ""
	if gvisorEnabled {
		checkpointRestore = true
		runscVersion = firstEnv("RUNSC_VERSION")
	}

	return &runnerv1.RunnerInfo{
		RunnerId:          r.runnerID,
		Status:            status,
		ImageRef:          firstEnv("CHETTER_RUNNER_IMAGE", "CONTAINER_IMAGE"),
		ImageDigest:       firstEnv("CHETTER_RUNNER_IMAGE_DIGEST"),
		Version:           firstEnv("CHETTER_RUNNER_VERSION", "VERSION", "GITHUB_SHA"),
		MaxConcurrent:     int32(maxConcurrent),
		RunningTasks:      int32(len(currentExecutions)),
		AvailableSlots:    int32(availableSlots),
		TotalStarted:      totalStarted,
		TotalCompleted:    totalCompleted,
		TotalErrors:       totalErrors,
		CurrentExecutions: currentExecutions,
		ExecutionMode:     r.executionMode(),
		StartedAt:         formatProtoTime(r.startedAt),
		GvisorEnabled:     gvisorEnabled,
		CheckpointRestore: checkpointRestore,
		RunscVersion:      runscVersion,
	}
}

func (r *Runner) publishRunnerHeartbeatRPC(status string) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	cmd, err := r.rpcClient.Heartbeat(ctx, connect.NewRequest(&runnerv1.HeartbeatRequest{Runner: r.runnerInfoProto(status)}))
	if err != nil {
		slog.Warn("failed to publish runner heartbeat", "runner_id", r.runnerID, "err", err)
		return
	}
	for _, command := range cmd.Msg.Commands {
		switch command.Type {
		case "cancel":
			r.cancelTask(command.TaskId, command.AgentSessionId, command.UserPromptId, command.ExecutionId, command.Reason)
		case "drain":
			r.startDrain()
		}
	}
}

func (r *Runner) cancelTask(taskID, agentSessionID, userPromptID, executionID, reason string) {
	r.mu.Lock()
	if _, seen := r.cancelledTasks[executionID]; seen {
		r.mu.Unlock()
		return
	}
	session, ok := r.tasks[executionID]
	if ok && (session.Request.TaskID != taskID || session.Request.AgentSessionID != agentSessionID || session.Request.UserPromptID != userPromptID) {
		r.mu.Unlock()
		slog.Warn("ignoring cancellation with mismatched execution hierarchy", "taskID", taskID, "agentSessionID", agentSessionID, "userPromptID", userPromptID, "executionID", executionID)
		return
	}
	if ok {
		r.cancelledTasks[executionID] = struct{}{}
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	if reason == "" {
		reason = "cancelled by operator"
	}
	slog.Info("cancelling task", "taskID", taskID, "executionID", executionID, "reason", reason)
	session.Cancel()
}

// startDrain marks the runner as draining. It returns true if this call
// transitioned the runner into the draining state, and false if it was
// already draining (idempotent). Callers that want to act on the transition
// (e.g. publishing a final heartbeat) should check the return value.
func (r *Runner) startDrain() bool {
	if r.draining.Swap(true) {
		return false // already draining
	}
	slog.Info("runner draining — will stop claiming tasks and wait for running tasks to finish", "runner_id", r.runnerID)
	return true
}

// BeginGracefulShutdown initiates an operator- or signal-triggered graceful
// drain. It marks the runner as draining (so claim loops stop accepting new
// tasks) and, on the first transition, immediately publishes a final heartbeat
// reporting the "draining" status, so the server knows this runner is going
// away and can reassign in-flight tasks sooner. The heartbeat loop would
// otherwise only report the new status on its next 5s tick, which may never
// arrive if the run context is cancelled immediately afterwards (e.g. on
// SIGTERM). See issue #97.
//
// The caller (cmd/runner/main.go on SIGTERM/SIGINT) cancels the run context
// after this returns, which lets the existing startConnectRPC shutdown path
// invoke waitDrain to wait for in-flight tasks.
func (r *Runner) BeginGracefulShutdown() {
	if r.startDrain() {
		r.publishRunnerHeartbeat("draining")
	}
}

// ForcedExit reports whether the most recent drain force-cancelled in-flight
// tasks after the drain deadline expired. main.go uses this to exit with a
// non-zero status code after a forced termination (issue #97).
func (r *Runner) ForcedExit() bool {
	return r.forcedExit.Load()
}

// waitDrain blocks until all in-flight tasks have finished or the deadline
// expires. It returns true if the deadline expired while tasks were still
// running (i.e. the remaining tasks were force-cancelled), and false if all
// tasks completed cleanly within the grace period. Callers (the SIGTERM/SIGINT
// path in startConnectRPC) use the result to set the process exit code. See
// issue #97.
//
// For tasks with CheckpointAfterSuccess (resumable sessions), the
// force-cancel path preserves the workspace and session export so the task
// can be resumed on a fresh runner — the workspace is not destroyed and the
// session ID is included in the terminal report. See issue #160.
func (r *Runner) waitDrain(deadline time.Duration) bool {
	if !r.draining.Load() {
		return false
	}

	slog.Info("waiting for running tasks to finish before exit", "runner_id", r.runnerID, "deadline", deadline)
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		r.mu.Lock()
		count := len(r.tasks)
		tasksChanged := r.tasksChanged
		r.mu.Unlock()
		if count == 0 {
			slog.Info("all tasks completed, drain finished", "runner_id", r.runnerID)
			return false
		}
		select {
		case <-ctx.Done():
			slog.Warn("drain deadline exceeded, forcing exit",
				"runner_id", r.runnerID, "remaining_tasks", count)
			r.mu.Lock()
			for executionID, session := range r.tasks {
				// For resumable tasks (CheckpointAfterSuccess), preserve the
				// workspace on disk so a fresh runner can resume from where the
				// session left off. The task's error handler checks
				// session.PreserveWorkspace to include the workspace path in
				// the terminal report. See issue #160.
				if session.Request.CheckpointAfterSuccess {
					session.PreserveWorkspace = true
					session.PauseOnDrain = true
					slog.Info("preserving workspace for resumable task on drain",
						"runner_id", r.runnerID, "task_id", session.TaskID, "execution_id", executionID, "workspace", session.WorkspaceDir)
				}
				slog.Warn("cancelling remaining task", "runner_id", r.runnerID, "task_id", session.TaskID, "execution_id", executionID, "resumable", session.Request.CheckpointAfterSuccess)
				session.Cancel()
			}
			r.mu.Unlock()
			return true
		case <-tasksChanged:
			continue
		case <-ticker.C:
			slog.Info("drain waiting", "runner_id", r.runnerID, "remaining_tasks", count)
		}
	}
}

// computeDrainDeadline derives the drain wait deadline from the in-flight
// tasks' remaining timeouts, clamped by the configured ceiling
// (CHETTER_DRAIN_TIMEOUT_SEC). Instead of a fixed cap, short tasks finish
// cleanly; only genuinely long-running tasks approach the ceiling. See issue
// #160 criterion 1.
func (r *Runner) computeDrainDeadline() time.Duration {
	ceiling := drainTimeout()
	ceilingConfigured := false
	if value := strings.TrimSpace(os.Getenv("CHETTER_DRAIN_TIMEOUT_SEC")); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			ceilingConfigured = true
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tasks) == 0 {
		return ceiling
	}

	maxRemaining := time.Duration(0)
	now := time.Now()
	for _, session := range r.tasks {
		timeout := time.Duration(session.Request.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = time.Duration(defaultTaskTimeoutSec) * time.Second
		}
		elapsed := now.Sub(session.StartedAt)
		remaining := timeout - elapsed
		if remaining > maxRemaining {
			maxRemaining = remaining
		}
	}

	// An explicitly configured value is an operator ceiling. With no explicit
	// ceiling, derive the deadline entirely from the in-flight task timeouts;
	// this is the default behavior required for long-running tasks by #160.
	if ceilingConfigured && maxRemaining > ceiling {
		return ceiling
	}
	if maxRemaining < 30*time.Second {
		return 30 * time.Second
	}
	return maxRemaining
}

func drainTimeout() time.Duration {
	seconds, err := strconv.Atoi(firstEnv("CHETTER_DRAIN_TIMEOUT_SEC"))
	if err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return defaultDrainTimeout
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
