package controller

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
)

const heartbeatInterval = 5 * time.Second

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
			r.cancelTask(command.TaskId, command.ExecutionId, command.Reason)
		case "drain":
			r.startDrain()
		}
	}
}

func (r *Runner) cancelTask(taskID, executionID, reason string) {
	r.mu.Lock()
	if _, seen := r.cancelledTasks[executionID]; seen {
		r.mu.Unlock()
		return
	}
	session, ok := r.tasks[executionID]
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
	r.publishStatusForRequest(session.Request, "cancelled", reason, nil)
}

func (r *Runner) startDrain() {
	if r.draining.Swap(true) {
		return // already draining
	}
	slog.Info("runner draining — will stop claiming tasks and wait for running tasks to finish", "runner_id", r.runnerID)
	close(r.drainCh)
}

func (r *Runner) waitDrain(deadline time.Duration) {
	select {
	case <-r.drainCh:
		// not draining, nothing to wait for
		return
	default:
	}

	slog.Info("waiting for running tasks to finish before exit", "runner_id", r.runnerID, "deadline", deadline)
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	r.drainCtx = ctx
	r.drainCancel = cancel
	defer cancel()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		r.mu.Lock()
		count := len(r.tasks)
		r.mu.Unlock()
		if count == 0 {
			slog.Info("all tasks completed, drain finished", "runner_id", r.runnerID)
			return
		}
		select {
		case <-ctx.Done():
			slog.Warn("drain deadline exceeded, forcing exit",
				"runner_id", r.runnerID, "remaining_tasks", count)
			r.mu.Lock()
			for executionID, session := range r.tasks {
				slog.Warn("cancelling remaining task", "runner_id", r.runnerID, "task_id", session.TaskID, "execution_id", executionID)
				session.Cancel()
			}
			r.mu.Unlock()
			return
		case <-ticker.C:
			slog.Info("drain waiting", "runner_id", r.runnerID, "remaining_tasks", count)
		}
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
