package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const heartbeatInterval = 30 * time.Second

type runnerHeartbeat struct {
	EventType      string    `json:"event_type"`
	RunnerID       string    `json:"runner_id"`
	Status         string    `json:"status"`
	ImageRef       string    `json:"image_ref,omitempty"`
	ImageDigest    string    `json:"image_digest,omitempty"`
	Version        string    `json:"version,omitempty"`
	ListenSubject  string    `json:"listen_subject,omitempty"`
	ResultSubject  string    `json:"result_subject,omitempty"`
	MaxConcurrent  int       `json:"max_concurrent"`
	RunningTasks   int       `json:"running_tasks"`
	AvailableSlots int       `json:"available_slots"`
	TotalStarted   int64     `json:"total_started"`
	TotalCompleted int64     `json:"total_completed"`
	TotalErrors    int64     `json:"total_errors"`
	CurrentTaskIDs []string  `json:"current_task_ids,omitempty"`
	ExecutionMode  string    `json:"execution_mode,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	SentAt         time.Time `json:"sent_at"`
}

func newRunnerID() string {
	for _, key := range []string{"RUNNER_ID", "HOSTNAME"} {
		if value := sanitizeSubjectToken(os.Getenv(key)); value != "" {
			return value
		}
	}
	if hostname, err := os.Hostname(); err == nil {
		if value := sanitizeSubjectToken(hostname); value != "" {
			return value
		}
	}
	return "runner-unknown"
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
			r.publishRunnerHeartbeat("active")
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runner) publishRunnerHeartbeat(status string) {
	payload, err := json.Marshal(r.runnerHeartbeatSnapshot(status))
	if err != nil {
		slog.Error("failed to marshal runner heartbeat", "err", err)
		return
	}
	subject := fmt.Sprintf("%s.runners.%s.heartbeat", r.cfg.Runner.ResultSubject, r.runnerID)
	if err := r.natsClient.Publish(subject, payload); err != nil {
		slog.Warn("failed to publish runner heartbeat", "runner_id", r.runnerID, "err", err)
		return
	}
	if err := r.natsClient.Conn.Flush(); err != nil {
		slog.Warn("failed to flush runner heartbeat", "runner_id", r.runnerID, "err", err)
	}
}

func (r *Runner) runnerHeartbeatSnapshot(status string) runnerHeartbeat {
	r.mu.Lock()
	taskIDs := make([]string, 0, len(r.tasks))
	for taskID := range r.tasks {
		taskIDs = append(taskIDs, taskID)
	}
	totalStarted := r.totalStarted
	totalCompleted := r.totalCompleted
	totalErrors := r.totalErrors
	r.mu.Unlock()

	maxConcurrent := r.cfg.Runner.MaxConcurrent
	availableSlots := maxConcurrent - len(r.sem)
	if availableSlots < 0 {
		availableSlots = 0
	}
	return runnerHeartbeat{
		EventType:      "runner_heartbeat",
		RunnerID:       r.runnerID,
		Status:         status,
		ImageRef:       firstEnv("DEVFLEET_RUNNER_IMAGE", "CONTAINER_IMAGE"),
		ImageDigest:    firstEnv("DEVFLEET_RUNNER_IMAGE_DIGEST"),
		Version:        firstEnv("DEVFLEET_RUNNER_VERSION", "VERSION", "GITHUB_SHA"),
		ListenSubject:  r.cfg.Runner.ListenSubject,
		ResultSubject:  r.cfg.Runner.ResultSubject,
		MaxConcurrent:  maxConcurrent,
		RunningTasks:   len(taskIDs),
		AvailableSlots: availableSlots,
		TotalStarted:   totalStarted,
		TotalCompleted: totalCompleted,
		TotalErrors:    totalErrors,
		CurrentTaskIDs: taskIDs,
		ExecutionMode:  r.executionMode(),
		StartedAt:      r.startedAt,
		SentAt:         time.Now().UTC(),
	}
}

func (r *Runner) recordTerminalStatus(taskID, status string) {
	if taskID == "" || (status != "done" && status != "error") {
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
	r.totalErrors++
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
