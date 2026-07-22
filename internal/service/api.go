package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
)

// --- Task Methods ---

// GetTask returns a single task by ID, respecting team-scoped access.
func (s *Service) GetTask(ctx context.Context, taskID string) (TaskToolRecord, error) {
	task, err := s.taskForToolAccess(ctx, taskID)
	if err != nil {
		return TaskToolRecord{}, err
	}
	rec := repoTaskToToolRecord(task)
	if run, err := s.repo.GetUserPromptByTaskID(ctx, taskID); err == nil {
		rec.AgentSessionID = run.AgentSessionID
		if attempts, err := s.repo.ListExecutionAttemptsByPrompt(ctx, run.ID); err == nil && len(attempts) > 0 {
			attempt := attempts[len(attempts)-1]
			rec.ExecutionID = attempt.ID
			rec.TimeoutSec = int(attempt.TimeoutSec)
			rec.StartedAt = nullTimePtr(attempt.StartedAt)
		}
	}
	return rec, nil
}

// ExportTask returns the session export (markdown transcript) for a task.
func (s *Service) ExportTask(ctx context.Context, taskID string) (string, error) {
	if _, err := s.taskForToolAccess(ctx, taskID); err != nil {
		return "", err
	}
	prompt, err := s.repo.GetUserPromptByTaskID(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("get latest prompt for task %s: %w", taskID, err)
	}
	if !prompt.SessionExport.Valid {
		return "", fmt.Errorf("no session export available for task %s", taskID)
	}
	return strings.ReplaceAll(prompt.SessionExport.String, "\\n", "\n"), nil
}

// ListTasks returns tasks, optionally filtered by status, respecting team scope.
func (s *Service) ListTasks(ctx context.Context, status string, limit, offset int, search string, uiTeamIDs, uiRepos []string) ([]TaskToolRecord, error) {
	scope, scoped := auth.GetScope(ctx)
	clamped := clampListLimit(limit)
	clampedOffset := int32(max(offset, 0))
	var tasks []repository.ChetterTask
	var err error

	// Determine effective team IDs: auth-scoped teams intersected with UI filter.
	var effectiveTeamIDs []string
	if scoped && !scope.Admin {
		effectiveTeamIDs = scope.Teams()
	}
	if len(uiTeamIDs) > 0 {
		if len(effectiveTeamIDs) == 0 {
			effectiveTeamIDs = uiTeamIDs
		} else {
			effectiveTeamIDs = intersectStrings(effectiveTeamIDs, uiTeamIDs)
		}
	}
	hasRepos := len(uiRepos) > 0

	// When repo filtering is needed, use raw queries to apply both team and repo
	// filters before LIMIT/OFFSET (avoids client-side pagination bug).
	if hasRepos && search != "" {
		tasks, err = s.searchTasksRaw(ctx, effectiveTeamIDs, uiRepos, status, search, clamped, clampedOffset)
	} else if hasRepos {
		tasks, err = s.listTasksRaw(ctx, effectiveTeamIDs, uiRepos, status, clamped, clampedOffset)
	} else if search != "" {
		if len(effectiveTeamIDs) > 0 {
			if len(effectiveTeamIDs) > 1 {
				tasks, err = s.repo.SearchTasksByTeams(ctx, repository.SearchTasksByTeamsParams{
					TeamIds:           nullStringSlice(effectiveTeamIDs),
					StatusFilter:      status,
					TriggerNameFilter: sql.NullString{},
					Search:            search,
					Limit:             clamped,
					Offset:            clampedOffset,
				})
			} else {
				tasks, err = s.searchTasksFTS(ctx, sql.NullString{String: effectiveTeamIDs[0], Valid: true}, status, search, clamped, clampedOffset)
			}
		} else {
			tasks, err = s.searchTasksFTS(ctx, sql.NullString{String: "", Valid: true}, status, search, clamped, clampedOffset)
		}
	} else if len(effectiveTeamIDs) > 0 {
		tasks, err = s.repo.ListTasksByStatusAndTeams(ctx, repository.ListTasksByStatusAndTeamsParams{
			TeamIds:           nullStringSlice(effectiveTeamIDs),
			StatusFilter:      status,
			TriggerNameFilter: sql.NullString{},
			Limit:             clamped,
			Offset:            clampedOffset,
		})
	} else {
		tasks, err = s.repo.ListTasksByStatus(ctx, repository.ListTasksByStatusParams{
			StatusFilter: status,
			Limit:        clamped,
			Offset:       clampedOffset,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]TaskToolRecord, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, repoTaskToToolRecord(task))
	}
	return out, nil
}

// CancelTask cancels a pending or running task by ID.
func (s *Service) CancelTask(ctx context.Context, taskID, reason string) (TaskToolRecord, error) {
	if _, err := s.taskForToolAccess(ctx, taskID); err != nil {
		return TaskToolRecord{}, err
	}
	if reason == "" {
		reason = "cancelled by operator"
	}
	now := time.Now().UTC()
	err := withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
		rows, err := q.CancelExecutionAttemptsByTask(ctx, repository.CancelExecutionAttemptsByTaskParams{
			Error:     sql.NullString{String: reason, Valid: true},
			EndedAt:   sql.NullTime{Time: now, Valid: true},
			UpdatedAt: now,
			TaskID:    taskID,
		})
		if err != nil {
			return fmt.Errorf("cancel execution attempt: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("task %s is not pending or running", taskID)
		}
		rows, err = q.CancelTask(ctx, repository.CancelTaskParams{
			Error:     sql.NullString{String: reason, Valid: true},
			EndedAt:   sql.NullTime{Time: now, Valid: true},
			UpdatedAt: now,
			ID:        taskID,
		})
		if err != nil {
			return fmt.Errorf("cancel task aggregate: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("task %s is not pending or running", taskID)
		}
		return nil
	})
	if err != nil {
		return TaskToolRecord{}, err
	}
	if err := s.repo.UpdateTriggerRunStatusByTask(ctx, repository.UpdateTriggerRunStatusByTaskParams{
		Status: "cancelled",
		TaskID: taskID,
	}); err != nil {
		slog.Warn("failed to update trigger run status on cancel", "task_id", taskID, "err", err)
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "task_cancelled",
		SourceType: "api",
		TargetType: "task",
		TargetID:   taskID,
		Detail:     fmt.Sprintf("task cancelled: %s", reason),
	})
	return s.GetTask(ctx, taskID)
}

// ExtendTaskTimeout adds time to a pending or running task's deadline.
func (s *Service) ExtendTaskTimeout(ctx context.Context, taskID string, extensionSec int) (TaskToolRecord, error) {
	if extensionSec <= 0 {
		return TaskToolRecord{}, fmt.Errorf("extension must be greater than zero")
	}
	if _, err := s.taskForToolAccess(ctx, taskID); err != nil {
		return TaskToolRecord{}, err
	}
	now := time.Now().UTC()
	rows, err := s.repo.ExtendActiveExecutionAttemptTimeout(ctx, repository.ExtendActiveExecutionAttemptTimeoutParams{
		ExtensionSec: int32(extensionSec),
		UpdatedAt:    now,
		TaskID:       taskID,
	})
	if err != nil {
		return TaskToolRecord{}, fmt.Errorf("extend task timeout: %w", err)
	}
	if rows == 0 {
		return TaskToolRecord{}, fmt.Errorf("task %s is not pending or running", taskID)
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "task_timeout_extended",
		SourceType: "api",
		TargetType: "task",
		TargetID:   taskID,
		Detail:     fmt.Sprintf("task timeout extended by %d seconds", extensionSec),
	})
	return s.GetTask(ctx, taskID)
}

// ClearQueue cancels all pending tasks. Admin only.
func (s *Service) ClearQueue(ctx context.Context) (int, error) {
	if !isAdmin(ctx) {
		return 0, fmt.Errorf("admin access required")
	}
	now := time.Now().UTC()
	reason := sql.NullString{String: "cancelled by chetter_clear_queue", Valid: true}
	endedAt := sql.NullTime{Time: now, Valid: true}
	var cancelled int64
	err := withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
		if _, err := q.CancelPendingExecutionAttempts(ctx, repository.CancelPendingExecutionAttemptsParams{
			Error:     reason,
			EndedAt:   endedAt,
			UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("cancel pending execution attempts: %w", err)
		}
		var err error
		cancelled, err = q.ClearPendingTasks(ctx, repository.ClearPendingTasksParams{
			Error:     reason,
			EndedAt:   endedAt,
			UpdatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("cancel pending task aggregates: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "queue_cleared",
		SourceType: "api",
		TargetType: "task",
		Detail:     fmt.Sprintf("cleared %d pending tasks", cancelled),
	})
	return int(cancelled), nil
}

// --- Task Event Methods ---

// GetTaskEvents returns the full event history for a task.
func (s *Service) GetTaskEvents(ctx context.Context, taskID string, limit, offset int) ([]TaskEventRecord, error) {
	if _, err := s.taskForToolAccess(ctx, taskID); err != nil {
		return nil, err
	}
	events, err := s.repo.ListTaskEvents(ctx, repository.ListTaskEventsParams{
		TaskID: taskID,
		Limit:  clampEventLimit(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	out := make([]TaskEventRecord, len(events))
	for i, ev := range events {
		out[i] = TaskEventRecord{
			ID:                 ev.ID,
			TaskID:             ev.TaskID,
			Subject:            ev.Subject,
			Status:             ev.Status,
			EventType:          ev.EventType,
			ExecutionID:        ev.ExecutionAttemptID.String,
			AgentSessionID:     ev.AgentSessionID.String,
			UserPromptID:       ev.UserPromptID.String,
			ExecutionAttemptID: ev.ExecutionAttemptID.String,
			Payload:            string(ev.Payload),
			CreatedAt:          ev.CreatedAt,
		}
	}
	return out, nil
}

// GetTaskEventsSince returns events for a task created after the given time.
// Used by the streaming RPC to replay missed events on reconnect.
func (s *Service) GetTaskEventsSince(ctx context.Context, taskID string, since time.Time) ([]TaskEventRecord, error) {
	rows, err := s.repo.ListTaskEventsSince(ctx, repository.ListTaskEventsSinceParams{
		TaskID:    taskID,
		CreatedAt: since,
	})
	if err != nil {
		return nil, fmt.Errorf("get events since: %w", err)
	}
	out := make([]TaskEventRecord, len(rows))
	for i, ev := range rows {
		out[i] = TaskEventRecord{
			ID:                 ev.ID,
			TaskID:             ev.TaskID,
			Subject:            ev.Subject,
			Status:             ev.Status,
			EventType:          ev.EventType,
			ExecutionID:        ev.ExecutionAttemptID.String,
			AgentSessionID:     ev.AgentSessionID.String,
			UserPromptID:       ev.UserPromptID.String,
			ExecutionAttemptID: ev.ExecutionAttemptID.String,
			Payload:            string(ev.Payload),
			CreatedAt:          ev.CreatedAt,
		}
	}
	return out, nil
}

type TaskProgressPage struct {
	Entries    []TaskProgressRecord
	HasMore    bool
	NextOffset int
}

// GetTaskProgress returns one raw-event page distilled into timeline entries.
// Pagination advances over persisted events rather than filtered entries so
// noisy harness events cannot cause skipped or duplicated history.
func (s *Service) GetTaskProgress(ctx context.Context, taskID string, limit, offset int) (TaskProgressPage, error) {
	if _, err := s.taskForToolAccess(ctx, taskID); err != nil {
		return TaskProgressPage{}, err
	}
	pageLimit := clampProgressLimit(limit)
	pageOffset := max(offset, 0)
	events, err := s.repo.ListTaskEvents(ctx, repository.ListTaskEventsParams{
		TaskID: taskID,
		Limit:  int32(pageLimit + 1),
		Offset: int32(pageOffset),
	})
	if err != nil {
		return TaskProgressPage{}, fmt.Errorf("get events: %w", err)
	}
	hasMore := len(events) > pageLimit
	if hasMore {
		events = events[:pageLimit]
	}
	var out []TaskProgressRecord
	var lastStatus, lastSummary string
	for _, ev := range events {
		resp := parseJSON[store.TaskResponse](ev.Payload, "event:"+ev.ID+" payload")
		if isProgressHeartbeat(resp.Summary) {
			continue
		}
		summary := humanProgressSummary(resp.Summary)
		if ev.Status == "running" && isNoiseSummary(summary) {
			continue
		}
		if ev.Status == lastStatus && summary == lastSummary && resp.Error == "" {
			continue
		}
		out = append(out, TaskProgressRecord{
			Time:    ev.CreatedAt,
			Status:  ev.Status,
			Summary: summary,
			Error:   resp.Error,
		})
		lastStatus = ev.Status
		lastSummary = summary
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return TaskProgressPage{
		Entries:    out,
		HasMore:    hasMore,
		NextOffset: pageOffset + len(events),
	}, nil
}

func clampProgressLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	// Reserve one row under the repository's 500-row cap to detect another page.
	return min(limit, 499)
}

func isProgressHeartbeat(summary string) bool {
	return strings.HasPrefix(strings.TrimSpace(summary), "opencode: server.heartbeat")
}

// isNoiseSummary reports whether a humanized progress summary conveys no
// useful information and should be suppressed from the distilled timeline.
func isNoiseSummary(summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return true
	}
	// Pi harness: text-delta fragments like "pi: .", "pi: the", "pi: :"
	if strings.HasPrefix(summary, "pi:") {
		detail := strings.TrimSpace(strings.TrimPrefix(summary, "pi:"))
		if detail == "" {
			return true
		}
		words := strings.Fields(detail)
		if len(words) <= 1 && len(detail) <= 8 {
			return true
		}
	}
	// OpenCode: repetitive events that duplicate information already conveyed
	// by more specific entries (tool calls, step finishes, replies, etc.)
	switch summary {
	case "Agent session updated", "Agent message updated", "Agent updated progress":
		return true
	}
	return false
}

func humanProgressSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	if !strings.HasPrefix(summary, "opencode: ") {
		return summary
	}

	detail := strings.TrimSpace(strings.TrimPrefix(summary, "opencode: "))
	eventType, payload, _ := strings.Cut(detail, " ")
	payload = strings.TrimSpace(payload)
	props := parseJSON[map[string]any](json.RawMessage(payload), "progress_summary")

	switch eventType {
	case "server.connected":
		return "Connected to the agent runtime"
	case "server.heartbeat":
		return ""
	case "session.updated":
		return "Agent session updated"
	case "session.status":
		if status, ok := stringAt(props, "status", "type"); ok {
			switch status {
			case "busy":
				return "Agent is working"
			case "idle":
				return "Agent is waiting"
			default:
				return "Agent status: " + status
			}
		}
		return "Agent status changed"
	case "message.part.updated", "message.part.delta":
		return humanMessagePartSummary(props)
	case "message.updated":
		return "Agent message updated"
	case "session.error":
		return "Agent session error"
	case "permission.asked":
		return "Agent requested permission"
	case "permission.replied":
		return "Permission response received"
	case "file.edited":
		if path, ok := firstStringAt(props, []string{"filePath"}, []string{"path"}); ok {
			return "Edited " + path
		}
		return "Edited a file"
	case "command.executed":
		if command, ok := firstStringAt(props, []string{"command"}, []string{"cmd"}); ok {
			return "Ran command: " + truncateProgressDetail(command)
		}
		return "Ran a command"
	default:
		return "OpenCode event: " + strings.ReplaceAll(eventType, ".", " ")
	}
}

func humanMessagePartSummary(props map[string]any) string {
	part, _ := props["part"].(map[string]any)
	if part == nil {
		return "Agent updated progress"
	}
	partType, _ := part["type"].(string)
	switch partType {
	case "tool":
		tool, _ := part["tool"].(string)
		if tool == "" {
			tool = "tool"
		}
		status, _ := stringAt(part, "state", "status")
		target := toolTarget(part)
		prefix := "Using"
		switch status {
		case "running":
			prefix = "Running"
		case "completed":
			prefix = "Completed"
		case "error":
			prefix = "Tool failed:"
		}
		if target != "" {
			return fmt.Sprintf("%s %s on %s", prefix, tool, target)
		}
		return fmt.Sprintf("%s %s", prefix, tool)
	case "step-finish":
		reason, _ := part["reason"].(string)
		if reason == "tool-calls" {
			reason = "tool call"
		}
		if total, ok := numberAt(part, "tokens", "total"); ok && reason != "" {
			return fmt.Sprintf("Finished %s step (%s tokens)", reason, formatProgressNumber(total))
		}
		if reason != "" {
			return "Finished " + reason + " step"
		}
		return "Finished an agent step"
	case "text":
		if text, ok := part["text"].(string); ok && strings.TrimSpace(text) != "" {
			return "Agent replied: " + truncateProgressDetail(strings.TrimSpace(text))
		}
		return "Agent replied"
	default:
		return "Agent updated progress"
	}
}

func toolTarget(part map[string]any) string {
	if target, ok := firstStringAt(part,
		[]string{"state", "input", "filePath"},
		[]string{"state", "input", "path"},
		[]string{"state", "input", "command"},
		[]string{"state", "input", "pattern"},
	); ok {
		return truncateProgressDetail(target)
	}
	return ""
}

func firstStringAt(value map[string]any, paths ...[]string) (string, bool) {
	for _, path := range paths {
		if value, ok := stringAt(value, path...); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func stringAt(value map[string]any, path ...string) (string, bool) {
	var current any = value
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current = m[key]
	}
	text, ok := current.(string)
	return text, ok
}

func numberAt(value map[string]any, path ...string) (int64, bool) {
	var current any = value
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return 0, false
		}
		current = m[key]
	}
	switch n := current.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func truncateProgressDetail(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	const maxLen = 120
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "..."
}

func formatProgressNumber(n int64) string {
	text := fmt.Sprintf("%d", n)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}

// GetLatestTaskEvent returns the most recent event for a task with staleness info.
func (s *Service) GetLatestTaskEvent(ctx context.Context, taskID string) (TaskLatestEventOutput, error) {
	if _, err := s.taskForToolAccess(ctx, taskID); err != nil {
		return TaskLatestEventOutput{}, err
	}
	ev, err := s.repo.GetLatestTaskEvent(ctx, taskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return TaskLatestEventOutput{}, fmt.Errorf("no events found for task %s", taskID)
		}
		return TaskLatestEventOutput{}, fmt.Errorf("get latest event: %w", err)
	}
	ageSec := int(time.Since(ev.CreatedAt).Seconds())
	return TaskLatestEventOutput{
		Event: TaskEventRecord{
			ID: ev.ID, TaskID: ev.TaskID, AgentSessionID: ev.AgentSessionID.String,
			UserPromptID: ev.UserPromptID.String, ExecutionID: ev.ExecutionAttemptID.String,
			ExecutionAttemptID: ev.ExecutionAttemptID.String, Subject: ev.Subject,
			Status: ev.Status, EventType: ev.EventType, Payload: string(ev.Payload), CreatedAt: ev.CreatedAt,
		},
		AgeSec:  ageSec,
		IsStale: ageSec > reaperHealthMaxEventSec,
	}, nil
}

// --- Session Methods ---

// ListAgentSessions returns agent sessions, optionally filtered by status, respecting team scope.
func (s *Service) ListAgentSessions(ctx context.Context, status string, limit, offset int, search string, uiTeamIDs, uiRepos []string) ([]AgentSessionRecord, error) {
	scope, scoped := auth.GetScope(ctx)
	clamped := clampListLimit(limit)
	clampedOffset := int32(max(offset, 0))
	var rows []repository.ChetterAgentSession
	var err error

	// Determine effective team IDs: auth-scoped teams intersected with UI filter.
	var effectiveTeamIDs []string
	if scoped && !scope.Admin {
		effectiveTeamIDs = scope.Teams()
	}
	if len(uiTeamIDs) > 0 {
		if len(effectiveTeamIDs) == 0 {
			effectiveTeamIDs = uiTeamIDs
		} else {
			effectiveTeamIDs = intersectStrings(effectiveTeamIDs, uiTeamIDs)
		}
	}
	hasRepos := len(uiRepos) > 0

	if hasRepos && search != "" {
		rows, err = s.searchAgentSessionsRaw(ctx, effectiveTeamIDs, uiRepos, status, search, clamped, clampedOffset)
	} else if hasRepos {
		rows, err = s.listAgentSessionsRaw(ctx, effectiveTeamIDs, uiRepos, status, clamped, clampedOffset)
	} else if search != "" {
		if len(effectiveTeamIDs) > 0 {
			if len(effectiveTeamIDs) > 1 {
				rows, err = s.repo.SearchAgentSessionsByTeams(ctx, repository.SearchAgentSessionsByTeamsParams{
					TeamIds:      nullStringSlice(effectiveTeamIDs),
					StatusFilter: status,
					Search:       search,
					Limit:        clamped,
					Offset:       clampedOffset,
				})
			} else {
				rows, err = s.searchAgentSessionsFTS(ctx, sql.NullString{String: effectiveTeamIDs[0], Valid: true}, status, search, clamped, clampedOffset)
			}
		} else {
			rows, err = s.searchAgentSessionsFTS(ctx, sql.NullString{String: "", Valid: true}, status, search, clamped, clampedOffset)
		}
	} else if len(effectiveTeamIDs) > 0 {
		rows, err = s.repo.ListAgentSessionsByTeams(ctx, repository.ListAgentSessionsByTeamsParams{
			TeamIds:      nullStringSlice(effectiveTeamIDs),
			StatusFilter: status,
			Limit:        clamped,
			Offset:       clampedOffset,
		})
	} else {
		rows, err = s.repo.ListAgentSessions(ctx, repository.ListAgentSessionsParams{
			TeamFilter:   sql.NullString{String: "", Valid: true},
			StatusFilter: status,
			Limit:        clamped,
			Offset:       clampedOffset,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	out := make([]AgentSessionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentSessionRecord(row))
	}
	if len(out) > 0 {
		ids := make([]string, len(out))
		for i, s := range out {
			ids[i] = s.ID
		}
		counts := s.batchUserPromptCounts(ctx, ids)
		for i := range out {
			out[i].PromptCount = counts[out[i].ID]
		}
	}
	return out, nil
}

// GetAgentSession returns a single agent session with its user prompts.
func (s *Service) GetAgentSession(ctx context.Context, sessionID string) (AgentSessionRecord, []UserPromptRecord, error) {
	session, err := s.repo.GetAgentSessionByID(ctx, sessionID)
	if err != nil {
		return AgentSessionRecord{}, nil, fmt.Errorf("get agent session: %w", err)
	}
	if err := authorizeAgentSessionAccess(ctx, session); err != nil {
		return AgentSessionRecord{}, nil, err
	}
	runs, err := s.repo.ListUserPromptsBySession(ctx, sessionID)
	if err != nil {
		return AgentSessionRecord{}, nil, fmt.Errorf("list user prompts: %w", err)
	}
	outRuns := make([]UserPromptRecord, 0, len(runs))
	for _, run := range runs {
		record := userPromptRecord(run)
		attempts, err := s.repo.ListExecutionAttemptsByPrompt(ctx, run.ID)
		if err != nil {
			return AgentSessionRecord{}, nil, fmt.Errorf("list execution attempts: %w", err)
		}
		for _, attempt := range attempts {
			record.Attempts = append(record.Attempts, executionAttemptRecord(attempt))
		}
		outRuns = append(outRuns, record)
	}
	return agentSessionRecord(session), outRuns, nil
}

// batchUserPromptCounts returns a map of session_id -> run count for a batch of sessions.
func (s *Service) batchUserPromptCounts(ctx context.Context, sessionIDs []string) map[string]int32 {
	if len(sessionIDs) == 0 {
		return nil
	}
	db := s.repo.DB()
	if db == nil {
		return nil
	}
	args := make([]interface{}, len(sessionIDs))
	for i, v := range sessionIDs {
		args[i] = v
	}
	query := "SELECT agent_session_id, COUNT(*) FROM chetter_user_prompts WHERE agent_session_id IN (" + strings.Join(sqlPlaceholders(s.dialect, len(sessionIDs)), ",") + ") GROUP BY agent_session_id"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		slog.ErrorContext(ctx, "batch user prompt counts", "err", err)
		return nil
	}
	defer rows.Close()
	counts := make(map[string]int32, len(sessionIDs))
	for rows.Next() {
		var id string
		var count int32
		if err := rows.Scan(&id, &count); err != nil {
			continue
		}
		counts[id] = count
	}
	return counts
}

// --- Trigger Methods ---

// ListTriggers returns triggers, optionally filtered by type and enabled status, respecting team scope.
func (s *Service) ListTriggers(ctx context.Context, enabledOnly bool, triggerType string, uiTeamIDs, uiRepos []string) ([]store.TriggerRecord, error) {
	scope, scoped := auth.GetScope(ctx)
	var repoRecords []repository.ChetterTrigger
	var err error

	// Determine effective team IDs: auth-scoped teams intersected with UI filter.
	var effectiveTeamIDs []string
	if scoped && !scope.Admin {
		effectiveTeamIDs = scope.Teams()
	}
	if len(uiTeamIDs) > 0 {
		if len(effectiveTeamIDs) == 0 {
			effectiveTeamIDs = uiTeamIDs
		} else {
			effectiveTeamIDs = intersectStrings(effectiveTeamIDs, uiTeamIDs)
		}
	}

	if len(effectiveTeamIDs) > 0 {
		if enabledOnly {
			repoRecords, err = s.repo.ListEnabledTriggersByTeams(ctx, nullStringSlice(effectiveTeamIDs))
		} else {
			repoRecords, err = s.repo.ListTriggersByTeams(ctx, nullStringSlice(effectiveTeamIDs))
		}
	} else {
		if enabledOnly {
			repoRecords, err = s.repo.ListEnabledTriggers(ctx)
		} else {
			repoRecords, err = s.repo.ListTriggers(ctx)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	if triggerType != "" {
		filtered := repoRecords[:0]
		for _, r := range repoRecords {
			if r.TriggerType == triggerType {
				filtered = append(filtered, r)
			}
		}
		repoRecords = filtered
	}
	if len(uiRepos) > 0 {
		filtered := repoRecords[:0]
		for _, r := range repoRecords {
			url := r.GitUrl.String
			if url == "" {
				continue
			}
			lower := strings.ToLower(url)
			for _, repo := range uiRepos {
				if strings.Contains(lower, strings.ToLower(repo)) {
					filtered = append(filtered, r)
					break
				}
			}
		}
		repoRecords = filtered
	}
	triggers := make([]store.TriggerRecord, len(repoRecords))
	for i, r := range repoRecords {
		triggers[i] = triggerToStoreRecord(r)
	}
	s.enrichTriggerSources(ctx, triggers)
	return triggers, nil
}

// enrichTriggerSources populates the SourceRepoURL, SourceBranch, and
// SourcePath transient fields on git-managed triggers by looking up the
// definition_sources and definitions tables. Triggers without a source_id
// are left unchanged.
func (s *Service) enrichTriggerSources(ctx context.Context, triggers []store.TriggerRecord) {
	sourceCache := map[string]repository.DefinitionSource{}
	for i := range triggers {
		sid := triggers[i].SourceID
		if sid == "" {
			continue
		}
		src, ok := sourceCache[sid]
		if !ok {
			ph, err := s.repo.GetDefinitionSource(ctx, sid)
			if err != nil {
				slog.DebugContext(ctx, "enrichTriggerSources: definition source not found", "source_id", sid, "err", err)
				continue
			}
			src = ph
			sourceCache[sid] = src
		}
		triggers[i].SourceRepoURL = src.RepoUrl
		triggers[i].SourceBranch = src.Branch
		def, err := s.repo.GetDefinitionBySourceTypeName(ctx, repository.GetDefinitionBySourceTypeNameParams{
			SourceID:       sid,
			DefinitionType: "trigger",
			Name:           triggers[i].Name,
		})
		if err == nil {
			triggers[i].SourcePath = def.Path
		}
	}
}

// ListTriggerRuns returns trigger runs, optionally filtered by trigger name, respecting team scope.
func (s *Service) ListTriggerRuns(ctx context.Context, triggerName string, limit, offset int) ([]TriggerRunInfo, error) {
	scope, scoped := auth.GetScope(ctx)
	clamped := clampListLimit(limit)
	clampedOffset := int32(max(offset, 0))

	if triggerName != "" {
		trigger, err := s.repo.GetTriggerByName(ctx, triggerName)
		if err != nil {
			return nil, fmt.Errorf("trigger %q not found", triggerName)
		}
		if scoped && !scope.Admin && (!trigger.TeamID.Valid || !scope.HasTeam(trigger.TeamID.String)) {
			return nil, fmt.Errorf("trigger %q not found", triggerName)
		}
		rows, err := s.repo.ListTriggerRunsByTrigger(ctx, repository.ListTriggerRunsByTriggerParams{
			TriggerID: trigger.ID,
			Limit:     clamped,
			Offset:    clampedOffset,
		})
		if err != nil {
			return nil, fmt.Errorf("list trigger runs: %w", err)
		}
		out := make([]TriggerRunInfo, len(rows))
		for i, r := range rows {
			out[i] = TriggerRunInfo{
				ID:          r.ID,
				TriggerName: r.TriggerName,
				TaskID:      r.TaskID,
				Status:      r.Status,
				TriggeredAt: r.TriggeredAt,
				CreatedAt:   r.CreatedAt,
			}
		}
		return out, nil
	}

	if scoped && !scope.Admin {
		teamIDs := scope.Teams()
		if len(teamIDs) == 0 {
			return nil, nil
		}
		rows, err := s.repo.ListTriggerRunsByTeams(ctx, repository.ListTriggerRunsByTeamsParams{
			TeamIds: nullStringSlice(teamIDs),
			Limit:   clamped,
			Offset:  clampedOffset,
		})
		if err != nil {
			return nil, fmt.Errorf("list trigger runs: %w", err)
		}
		out := make([]TriggerRunInfo, len(rows))
		for i, r := range rows {
			out[i] = TriggerRunInfo{
				ID:          r.ID,
				TriggerName: r.TriggerName,
				TaskID:      r.TaskID,
				Status:      r.Status,
				TriggeredAt: r.TriggeredAt,
				CreatedAt:   r.CreatedAt,
			}
		}
		return out, nil
	}

	return nil, fmt.Errorf("admin access required to list all trigger runs without a trigger_name filter")
}

// --- Fleet Health ---

// GetRunnerHealth returns fleet health metrics.
func (s *Service) GetRunnerHealth(ctx context.Context, includeTasks bool) (store.RunnerFleetHealth, error) {
	health, err := s.store.GetRunnerFleetHealth(ctx, reaperHealthMaxEventSec, runnerPresenceMaxSec)
	if err != nil {
		return store.RunnerFleetHealth{}, fmt.Errorf("get runner fleet health: %w", err)
	}
	if !includeTasks {
		health.RunningTaskInfos = nil
	}
	return health, nil
}

// --- Token Management ---

// CreateToken creates a new API token for one or more teams and a user. Admin only.
func (s *Service) CreateToken(ctx context.Context, teamNames []string, userName, tokenName string) (CreateTokenOutput, error) {
	if !isAdmin(ctx) {
		return CreateTokenOutput{}, fmt.Errorf("admin access required")
	}
	teamNames = normalizeTeamNames(teamNames)
	if len(teamNames) == 0 {
		return CreateTokenOutput{}, fmt.Errorf("team_names is required")
	}
	if userName == "" {
		return CreateTokenOutput{}, fmt.Errorf("user_name is required")
	}
	if tokenName == "" {
		return CreateTokenOutput{}, fmt.Errorf("token_name is required")
	}
	now := time.Now().UTC()

	userID, err := randomID("user")
	if err != nil {
		return CreateTokenOutput{}, fmt.Errorf("generate user id: %w", err)
	}

	rawToken, err := randomID("chtr")
	if err != nil {
		return CreateTokenOutput{}, fmt.Errorf("generate token: %w", err)
	}
	hash := sha256.Sum256([]byte(rawToken))
	tokenID, err := randomID("tok")
	if err != nil {
		return CreateTokenOutput{}, fmt.Errorf("generate token id: %w", err)
	}

	teams := make([]repository.Team, 0, len(teamNames))
	err = withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
		for _, teamName := range teamNames {
			team, err := q.GetTeamByName(ctx, teamName)
			if err != nil {
				if err != sql.ErrNoRows {
					return fmt.Errorf("look up team %q: %w", teamName, err)
				}
				teamID, err := randomID("team")
				if err != nil {
					return fmt.Errorf("generate team id: %w", err)
				}
				if err := q.CreateTeam(ctx, repository.CreateTeamParams{
					ID:        teamID,
					Name:      teamName,
					CreatedAt: now,
					UpdatedAt: now,
				}); err != nil {
					return fmt.Errorf("create team %q: %w", teamName, err)
				}
				team.ID = teamID
				team.Name = teamName
			}
			teams = append(teams, team)
		}

		if err := q.CreateUser(ctx, repository.CreateUserParams{
			ID:        userID,
			Name:      userName,
			TeamID:    teams[0].ID,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		for _, team := range teams {
			if err := q.AddUserTeamMembership(ctx, repository.AddUserTeamMembershipParams{
				UserID:    userID,
				TeamID:    team.ID,
				Source:    "local",
				CreatedAt: now,
				UpdatedAt: now,
			}); err != nil {
				return fmt.Errorf("add user team membership: %w", err)
			}
		}

		if err := q.CreateToken(ctx, repository.CreateTokenParams{
			ID:        tokenID,
			Name:      tokenName,
			TokenHash: hex.EncodeToString(hash[:]),
			UserID:    userID,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("create token: %w", err)
		}

		for _, team := range teams {
			if err := q.AddTokenTeam(ctx, repository.AddTokenTeamParams{
				TokenID:   tokenID,
				TeamID:    team.ID,
				CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("add token team: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return CreateTokenOutput{}, err
	}

	teamIDs := make([]string, 0, len(teams))
	returnedTeamNames := make([]string, 0, len(teams))
	for _, team := range teams {
		teamIDs = append(teamIDs, team.ID)
		returnedTeamNames = append(returnedTeamNames, team.Name)
	}

	s.auditAsync(ctx, AuditEventParams{
		EventType:  "token_created",
		SourceType: "api",
		TargetType: "token",
		TargetID:   tokenName,
		Detail:     fmt.Sprintf("token %q created for user %q in teams %q", tokenName, userName, strings.Join(returnedTeamNames, ",")),
	})

	return CreateTokenOutput{
		Token:     rawToken,
		TeamID:    teams[0].ID,
		TeamName:  teams[0].Name,
		TeamIDs:   teamIDs,
		TeamNames: returnedTeamNames,
		UserID:    userID,
		UserName:  userName,
	}, nil
}

// ListTokens returns all API tokens. Admin only.
func (s *Service) ListTokens(ctx context.Context) ([]TokenInfo, error) {
	if !isAdmin(ctx) {
		return nil, fmt.Errorf("admin access required")
	}
	rows, err := s.repo.ListTokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	out := make([]TokenInfo, len(rows))
	for i, r := range rows {
		out[i] = TokenInfo{
			Name:      r.Name,
			UserName:  r.UserName,
			TeamName:  r.TeamName,
			TeamNames: splitCSV(r.TeamNames),
			CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

// DeleteToken deletes an API token by name. Admin only.
func (s *Service) DeleteToken(ctx context.Context, name string) error {
	if !isAdmin(ctx) {
		return fmt.Errorf("admin access required")
	}
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if err := s.repo.DeleteToken(ctx, name); err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "token_deleted",
		SourceType: "api",
		TargetType: "token",
		TargetID:   name,
		Detail:     fmt.Sprintf("token %q deleted", name),
	})
	return nil
}

// --- Team Management ---

// CreateTeam creates a new team. Admin only.
func (s *Service) CreateTeam(ctx context.Context, name string) (CreateTeamOutput, error) {
	if !isAdmin(ctx) {
		return CreateTeamOutput{}, fmt.Errorf("admin access required")
	}
	if name == "" {
		return CreateTeamOutput{}, fmt.Errorf("name is required")
	}
	now := time.Now().UTC()
	teamID, err := randomID("team")
	if err != nil {
		return CreateTeamOutput{}, fmt.Errorf("generate team id: %w", err)
	}
	if err := s.repo.CreateTeam(ctx, repository.CreateTeamParams{
		ID:        teamID,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return CreateTeamOutput{}, fmt.Errorf("create team: %w", err)
	}

	s.auditAsync(ctx, AuditEventParams{
		EventType:  "team_created",
		SourceType: "api",
		TargetType: "team",
		TargetID:   name,
		Detail:     fmt.Sprintf("team %q created", name),
	})

	return CreateTeamOutput{
		TeamID:    teamID,
		TeamName:  name,
		CreatedAt: now,
	}, nil
}

// ListTeams returns all teams. Admin only.
func (s *Service) ListTeams(ctx context.Context) ([]TeamInfo, error) {
	if !isAdmin(ctx) {
		return nil, fmt.Errorf("admin access required")
	}
	teams, err := s.repo.ListTeams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	out := make([]TeamInfo, len(teams))
	for i, t := range teams {
		out[i] = TeamInfo{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt}
	}
	return out, nil
}

// DeleteTeam deletes a team and cascades to its users, tokens, tasks, and triggers. Admin only.
func (s *Service) DeleteTeam(ctx context.Context, name string) error {
	if !isAdmin(ctx) {
		return fmt.Errorf("admin access required")
	}
	if name == "" {
		return fmt.Errorf("name is required")
	}
	team, err := s.repo.GetTeamByName(ctx, name)
	if err != nil {
		return fmt.Errorf("team %q not found", name)
	}
	if err := s.repo.DeleteTokensByTeam(ctx, repository.DeleteTokensByTeamParams{TeamID: team.ID, TeamID_2: team.ID}); err != nil {
		return fmt.Errorf("delete tokens for team: %w", err)
	}
	if err := s.repo.DeleteTokenTeamsByTeam(ctx, team.ID); err != nil {
		return fmt.Errorf("delete token team memberships: %w", err)
	}
	if err := s.repo.DeleteUserTeamMembershipsByTeam(ctx, team.ID); err != nil {
		return fmt.Errorf("delete user team memberships: %w", err)
	}
	if err := s.repo.DeleteUsersByTeam(ctx, team.ID); err != nil {
		return fmt.Errorf("delete users for team: %w", err)
	}
	if err := s.repo.DeleteTrigger(ctx, name); err != nil {
		slog.Debug("delete team: trigger not deleted", "team", name, "err", err)
	}
	if err := s.repo.DeleteTeam(ctx, name); err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "team_deleted",
		SourceType: "api",
		TargetType: "team",
		TargetID:   name,
		Detail:     fmt.Sprintf("team %q deleted", name),
	})
	return nil
}

// ListUsers returns all users, optionally filtered by team name. Admin only.
func (s *Service) ListUsers(ctx context.Context, teamName string) ([]UserInfo, error) {
	if !isAdmin(ctx) {
		return nil, fmt.Errorf("admin access required")
	}
	if teamName != "" {
		team, err := s.repo.GetTeamByName(ctx, teamName)
		if err != nil {
			return nil, fmt.Errorf("team %q not found", teamName)
		}
		teamRows, err := s.repo.ListUsersByTeam(ctx, team.ID)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		out := make([]UserInfo, len(teamRows))
		for i, r := range teamRows {
			out[i] = UserInfo{ID: r.ID, Name: r.Name, TeamName: r.TeamName, CreatedAt: r.CreatedAt}
		}
		return out, nil
	}
	allRows, err := s.repo.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	out := make([]UserInfo, len(allRows))
	for i, r := range allRows {
		out[i] = UserInfo{ID: r.ID, Name: r.Name, TeamName: r.TeamName, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// --- Audit & Artifacts ---

// ListAuditEvents returns audit log events with optional filters. Admin only.
func (s *Service) ListAuditEvents(ctx context.Context, filter AuditEventFilterInput) ([]AuditEventRecord, error) {
	if !isAdmin(ctx) {
		return nil, fmt.Errorf("admin access required")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var sinceTime sql.NullTime
	if filter.SinceHours > 0 {
		sinceTime = sql.NullTime{Time: time.Now().UTC().Add(-time.Duration(filter.SinceHours) * time.Hour), Valid: true}
	}

	clampedOffset := int32(max(filter.Offset, 0))
	hasExclusions := len(filter.ExcludeTypes) > 0

	var rows []repository.ListAuditLogRow
	var listErr error
	if hasExclusions && filter.Search != "" {
		rows, listErr = s.searchAuditLogFTS(ctx, filter, int32(limit), clampedOffset, sinceTime)
	} else if hasExclusions {
		rows, listErr = s.listAuditLogRaw(ctx, filter, int32(limit), clampedOffset, sinceTime)
	} else if filter.Search != "" {
		rows, listErr = s.searchAuditLogFTS(ctx, filter, int32(limit), clampedOffset, sinceTime)
	} else {
		baseParams := repository.ListAuditLogParams{
			EventType:  filter.EventType,
			Column2:    filter.EventType,
			SourceType: nullString(filter.SourceType),
			Column4:    filter.SourceType,
			SourceID:   nullString(filter.SourceID),
			Column6:    filter.SourceID,
			TargetType: nullString(filter.TargetType),
			Column8:    filter.TargetType,
			TargetID:   nullString(filter.TargetID),
			Column10:   filter.TargetID,
			Repo:       nullString(filter.Repo),
			Column12:   filter.Repo,
			CreatedAt:  sinceTime.Time,
			Column14:   sinceTime,
			Limit:      int32(limit),
			Offset:     clampedOffset,
		}
		rows, listErr = s.repo.ListAuditLog(ctx, baseParams)
	}
	if listErr != nil {
		return nil, fmt.Errorf("list audit log: %w", listErr)
	}
	out := make([]AuditEventRecord, len(rows))
	for i, r := range rows {
		out[i] = AuditEventRecord{
			ID:               r.ID,
			EventType:        r.EventType,
			CreatedAt:        r.CreatedAt,
			SourceType:       r.SourceType.String,
			SourceID:         r.SourceID.String,
			TargetType:       r.TargetType.String,
			TargetID:         r.TargetID.String,
			Repo:             r.Repo.String,
			GitHubEvent:      r.GithubEvent.String,
			GitHubAction:     r.GithubAction.String,
			GitHubDeliveryID: r.GithubDeliveryID.String,
			ParentEventID:    r.ParentEventID.String,
			Detail:           r.Detail.String,
			TokenID:          r.TokenID.String,
			TokenName:        r.TokenName.String,
		}
	}
	return out, nil
}

// ListTaskArtifacts returns GitHub artifacts created by tasks. Admin only.
func (s *Service) ListTaskArtifacts(ctx context.Context, filter TaskArtifactFilterInput) ([]TaskArtifactRecord, error) {
	if !isAdmin(ctx) {
		return nil, fmt.Errorf("admin access required")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var rows []repository.ListTaskArtifactsRow
	var listErr error
	if filter.Search != "" {
		rows, listErr = s.searchTaskArtifactsFTS(ctx, filter, int32(limit), int32(max(filter.Offset, 0)))
	} else {
		rows, listErr = s.repo.ListTaskArtifacts(ctx, repository.ListTaskArtifactsParams{
			TaskID:         filter.TaskID,
			Column2:        filter.TaskID,
			AgentSessionID: nullString(filter.AgentSessionID),
			Column4:        filter.AgentSessionID,
			ArtifactType:   filter.ArtifactType,
			Column6:        filter.ArtifactType,
			Repo:           filter.Repo,
			Column8:        filter.Repo,
			Limit:          int32(limit),
			Offset:         int32(max(filter.Offset, 0)),
		})
	}
	if listErr != nil {
		return nil, fmt.Errorf("list task artifacts: %w", listErr)
	}
	out := make([]TaskArtifactRecord, len(rows))
	for i, r := range rows {
		out[i] = TaskArtifactRecord{
			ID:              r.ID,
			TaskID:          r.TaskID,
			AgentSessionID:  r.AgentSessionID.String,
			UserPromptID:    r.UserPromptID.String,
			ArtifactType:    r.ArtifactType,
			Repo:            r.Repo,
			Number:          int(r.Number.Int32),
			URL:             r.Url.String,
			Ref:             r.Ref.String,
			SHA:             r.Sha.String,
			CreatedAt:       r.CreatedAt,
			DiscoveredAt:    r.DiscoveredAt,
			DiscoverySource: r.DiscoverySource,
		}
	}
	return out, nil
}

// --- Trigger Lookup ---

func (s *Service) ArcaneIsConfigured() bool {
	return s.arcane != nil && s.arcane.IsConfigured()
}

func (s *Service) GetTriggerByName(ctx context.Context, name string) (repository.ChetterTrigger, error) {
	return s.repo.GetTriggerByName(ctx, name)
}

// --- Arcane Methods ---

func (s *Service) ArcaneScannerStatus(ctx context.Context, envID string) (ArcaneScannerStatusOutput, error) {
	if s.arcane == nil {
		return ArcaneScannerStatusOutput{}, fmt.Errorf("arcane client not configured")
	}
	status, err := s.arcane.GetScannerStatus(ctx, envIDOrDefault(envID))
	if err != nil {
		return ArcaneScannerStatusOutput{}, fmt.Errorf("get scanner status: %w", err)
	}
	return ArcaneScannerStatusOutput{Available: status.Available, Version: status.Version}, nil
}

func (s *Service) ArcaneEnvironmentSummary(ctx context.Context, envID string) (ArcaneEnvironmentSummaryOutput, error) {
	if s.arcane == nil {
		return ArcaneEnvironmentSummaryOutput{}, fmt.Errorf("arcane client not configured")
	}
	summary, err := s.arcane.GetEnvironmentSummary(ctx, envIDOrDefault(envID))
	if err != nil {
		return ArcaneEnvironmentSummaryOutput{}, fmt.Errorf("get environment summary: %w", err)
	}
	return ArcaneEnvironmentSummaryOutput{
		TotalImages:   summary.TotalImages,
		ScannedImages: summary.ScannedImages,
		Summary: SeveritySummary{
			Critical: summary.Summary.Critical,
			High:     summary.Summary.High,
			Medium:   summary.Summary.Medium,
			Low:      summary.Summary.Low,
			Unknown:  summary.Summary.Unknown,
			Total:    summary.Summary.Total,
		},
	}, nil
}

func (s *Service) ArcaneListImages(ctx context.Context, envID string) ([]ImageSummaryItem, error) {
	if s.arcane == nil {
		return nil, fmt.Errorf("arcane client not configured")
	}
	return s.arcane.ListEnvironmentImages(ctx, envIDOrDefault(envID))
}

func (s *Service) ArcaneImageSummary(ctx context.Context, envID, imageID string) (ArcaneImageSummaryOutput, error) {
	if s.arcane == nil {
		return ArcaneImageSummaryOutput{}, fmt.Errorf("arcane client not configured")
	}
	if imageID == "" {
		return ArcaneImageSummaryOutput{}, fmt.Errorf("image_id is required")
	}
	summary, err := s.arcane.GetImageScanSummary(ctx, envIDOrDefault(envID), imageID)
	if err != nil {
		return ArcaneImageSummaryOutput{}, fmt.Errorf("get image summary: %w", err)
	}
	return ArcaneImageSummaryOutput{
		ImageID:  summary.ImageID,
		ScanTime: summary.ScanTime.Format(time.RFC3339),
		Status:   summary.Status,
		Summary: SeveritySummary{
			Critical: summary.Summary.Critical,
			High:     summary.Summary.High,
			Medium:   summary.Summary.Medium,
			Low:      summary.Summary.Low,
			Unknown:  summary.Summary.Unknown,
			Total:    summary.Summary.Total,
		},
	}, nil
}

func (s *Service) ArcaneListVulnerabilities(ctx context.Context, envID, imageID, severity string, page, limit int) ([]Vulnerability, int, error) {
	if s.arcane == nil {
		return nil, 0, fmt.Errorf("arcane client not configured")
	}
	if imageID == "" {
		return nil, 0, fmt.Errorf("image_id is required")
	}
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = 20
	}
	items, total, err := s.arcane.ListVulnerabilities(ctx, envIDOrDefault(envID), imageID, severity, page, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list vulnerabilities: %w", err)
	}
	out := make([]Vulnerability, 0, len(items))
	for _, v := range items {
		out = append(out, Vulnerability{
			VulnerabilityID:  v.VulnerabilityID,
			PkgName:          v.PkgName,
			InstalledVersion: v.InstalledVersion,
			FixedVersion:     v.FixedVersion,
			Severity:         string(v.Severity),
			Title:            v.Title,
			Description:      v.Description,
		})
	}
	return out, total, nil
}

// WhoamiInfo describes the current authenticated session.
type WhoamiTeamInfo struct {
	ID   string
	Name string
}

type WhoamiOutput struct {
	IsAdmin         bool
	PrimaryTeamName string
	Teams           []WhoamiTeamInfo
}

func (s *Service) Whoami(ctx context.Context) (WhoamiOutput, error) {
	scope, ok := auth.GetScope(ctx)

	if !ok {
		return WhoamiOutput{IsAdmin: true}, nil
	}
	if scope.Admin {
		allTeams, err := s.repo.ListTeams(ctx)
		if err != nil {
			return WhoamiOutput{IsAdmin: true}, nil
		}
		teams := make([]WhoamiTeamInfo, 0, len(allTeams))
		for _, t := range allTeams {
			teams = append(teams, WhoamiTeamInfo{ID: t.ID, Name: t.Name})
		}
		return WhoamiOutput{IsAdmin: true, Teams: teams}, nil
	}
	teamIDs := scope.Teams()
	teams := make([]WhoamiTeamInfo, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		team, err := s.repo.GetTeamByID(ctx, teamID)
		if err != nil {
			continue
		}
		teams = append(teams, WhoamiTeamInfo{ID: team.ID, Name: team.Name})
	}
	primaryName := ""
	if len(teams) > 0 {
		primaryName = teams[0].Name
	}
	return WhoamiOutput{
		IsAdmin:         false,
		PrimaryTeamName: primaryName,
		Teams:           teams,
	}, nil
}

func (s *Service) ListRepos(ctx context.Context) ([]string, error) {
	rows, err := s.rawDB.QueryContext(ctx, `SELECT repo FROM chetter_task_artifacts WHERE repo IS NOT NULL AND repo != '' GROUP BY repo ORDER BY repo`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	var repos []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			continue
		}
		repos = append(repos, r)
	}
	return repos, nil
}
