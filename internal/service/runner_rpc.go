package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

const (
	defaultClaimWaitSec       = 30
	defaultTaskLeaseSec       = 60
	claimPollInterval         = time.Second
	runnerEventSubject        = "connect.runner"
	heartbeatEventMinInterval = 60 * time.Second
)

var errNoClaimableTask = errors.New("no claimable task")
var errTaskNotClaimed = errors.New("task is not claimed by runner")

type RunnerRPCService struct {
	db            *repository.Queries
	rawDB         *sql.DB
	heartbeatSeen sync.Map
	drainRequests sync.Map // map[string]bool — runner ID → drain requested
	eventBus      TaskEventPublisher
	callbacks     TaskEventCallbackDispatcher
}

// TaskEventPublisher fans out task events to streaming subscribers.
// Implemented by webapi.EventBus.
type TaskEventPublisher interface {
	PublishTaskEvent(taskID, eventID, status, eventType, summary, payload, createdAt string)
}

type TaskEventCallbackDispatcher interface {
	DispatchTaskEventCallbacks(ctx context.Context, event TaskEventCallbackContext)
}

type TaskEventCallbackContext struct {
	ID            string
	TaskID        string
	TeamID        string
	Subject       string
	Status        string
	EventType     string
	Summary       string
	Error         string
	ErrorCategory string
	Payload       json.RawMessage
	CreatedAt     time.Time
}

func NewRunnerRPCService(db *repository.Queries, rawDB *sql.DB) *RunnerRPCService {
	return &RunnerRPCService{db: db, rawDB: rawDB}
}

func (s *RunnerRPCService) WithEventBus(bus TaskEventPublisher) *RunnerRPCService {
	s.eventBus = bus
	return s
}

func (s *RunnerRPCService) WithEventCallbacks(callbacks TaskEventCallbackDispatcher) *RunnerRPCService {
	s.callbacks = callbacks
	return s
}

func (s *RunnerRPCService) RegisterRunner(ctx context.Context, req *connect.Request[runnerv1.RegisterRunnerRequest]) (*connect.Response[runnerv1.RegisterRunnerResponse], error) {
	if err := s.upsertRunner(ctx, req.Msg.Runner); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.RegisterRunnerResponse{}), nil
}

func (s *RunnerRPCService) Heartbeat(ctx context.Context, req *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	if err := s.upsertRunner(ctx, req.Msg.Runner); err != nil {
		return nil, err
	}
	commands, err := s.runnerCommands(ctx, req.Msg.Runner)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.HeartbeatResponse{Commands: commands}), nil
}

func (s *RunnerRPCService) ClaimTask(ctx context.Context, req *connect.Request[runnerv1.ClaimTaskRequest]) (*connect.Response[runnerv1.ClaimTaskResponse], error) {
	waitSec := req.Msg.WaitSeconds
	if waitSec == 0 {
		waitSec = defaultClaimWaitSec
	}
	if waitSec < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("wait_seconds must be non-negative"))
	}
	if waitSec > defaultClaimWaitSec {
		waitSec = defaultClaimWaitSec
	}
	leaseSec := req.Msg.LeaseSeconds
	if leaseSec == 0 {
		leaseSec = defaultTaskLeaseSec
	}
	if leaseSec < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("lease_seconds must be non-negative"))
	}
	if leaseSec > 3600 {
		leaseSec = 3600
	}
	deadline := time.Now().Add(time.Duration(waitSec) * time.Second)

	for {
		task, err := s.claimOnce(ctx, req.Msg.RunnerId, time.Duration(leaseSec)*time.Second)
		if err == nil {
			resumeCheckpointPath := ""
			resumeWorkspacePath := ""
			if task.RequiredRunnerID.Valid {
				chk, chkErr := s.db.GetLatestAgentSessionCheckpointByTaskID(ctx, task.ID)
				if chkErr == nil && chk.Status == "ready" {
					resumeCheckpointPath = chk.CheckpointPath
					resumeWorkspacePath = chk.WorkspacePath
				}
			}
			protoTask := taskToProto(task, resumeCheckpointPath, resumeWorkspacePath)
			s.resolveTaskModel(ctx, protoTask)
			return connect.NewResponse(&runnerv1.ClaimTaskResponse{Task: protoTask}), nil
		}
		if !errors.Is(err, errNoClaimableTask) {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if waitSec == 0 || time.Now().After(deadline) {
			return connect.NewResponse(&runnerv1.ClaimTaskResponse{}), nil
		}
		select {
		case <-ctx.Done():
			return nil, connect.NewError(connect.CodeCanceled, ctx.Err())
		case <-time.After(claimPollInterval):
		}
	}
}

type resolvedModelConfig struct {
	ProviderID        string
	ModelID           string
	ProviderName      string
	ProviderBaseURL   string
	ProviderAPIKeyEnv string
}

func (s *RunnerRPCService) resolveTaskModel(ctx context.Context, task *runnerv1.Task) {
	if task == nil {
		return
	}
	if task.Harness == "" {
		task.Harness = "opencode"
	}
	catalog := modelcatalog.Default()
	row, err := s.db.GetActiveModelCatalog(ctx)
	if err == nil {
		if parsed, parseErr := modelcatalog.ParseYAML([]byte(row.Yaml)); parseErr == nil {
			catalog = parsed
		} else {
			slog.Warn("invalid active model catalog; using default", "err", parseErr)
		}
	} else if err != sql.ErrNoRows {
		slog.Warn("load active model catalog; using default", "err", err)
	}
	resolved := resolveModelForTask(catalog, task)
	task.ProviderId = resolved.ProviderID
	task.ModelId = resolved.ModelID
	task.ProviderName = resolved.ProviderName
	task.ProviderBaseUrl = resolved.ProviderBaseURL
	task.ProviderApiKeyEnv = resolved.ProviderAPIKeyEnv
}

func resolveModelForTask(catalog *modelcatalog.Catalog, task *runnerv1.Task) resolvedModelConfig {
	if catalog == nil {
		catalog = modelcatalog.Default()
	}
	harness := catalogHarnessName(task.Harness)
	providerID := strings.TrimSpace(task.ProviderId)
	modelID := strings.TrimSpace(task.ModelId)
	if providerID == "" && strings.Contains(modelID, "/") {
		parts := strings.SplitN(modelID, "/", 2)
		providerID = parts[0]
		modelID = parts[1]
	}
	if task.Env != nil {
		if providerID == "" {
			providerID = strings.TrimSpace(firstNonEmpty(task.Env["LLM_PROVIDER"], task.Env["PI_PROVIDER"]))
		}
		if modelID == "" {
			modelID = strings.TrimSpace(firstNonEmpty(task.Env["LLM_MODEL_CODER"], task.Env["PI_MODEL"], task.Env["ANTHROPIC_MODEL"]))
		}
	}
	defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
	if providerID == "" {
		providerID = defaultProvider
	}
	if modelID == "" {
		modelID = defaultModel
	}
	return catalogProviderModelConfig(catalog, harness, providerID, modelID)
}

func catalogDefaultForHarness(catalog *modelcatalog.Catalog, harness string) (providerID, modelID string) {
	providerID = catalog.DefaultProvider
	modelID = catalog.DefaultModel
	if def, ok := catalog.Defaults[harness]; ok {
		if def.Provider != "" {
			providerID = def.Provider
		}
		if def.Model != "" {
			modelID = def.Model
		}
	}
	if providerID == "" {
		providerID = "synthetic"
	}
	if modelID == "" {
		modelID = "hf:zai-org/GLM-5.2"
	}
	return providerID, modelID
}

func catalogProviderModelConfig(catalog *modelcatalog.Catalog, harness, providerID, modelID string) resolvedModelConfig {
	provider, ok := catalog.Providers[providerID]
	if !ok {
		return resolvedModelConfig{ProviderID: providerID, ModelID: modelID}
	}
	if hp, ok := provider.Harnesses[harness]; ok && hp.Disabled {
		defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
		return catalogProviderModelConfig(catalog, harness, defaultProvider, defaultModel)
	}
	resolved := resolvedModelConfig{ProviderID: providerID, ModelID: modelID}
	if hp, ok := provider.Harnesses[harness]; ok && !hp.Disabled {
		if hp.ID != "" {
			resolved.ProviderID = hp.ID
		}
		resolved.ProviderName = firstNonEmpty(hp.Name, provider.Name, resolved.ProviderID)
		resolved.ProviderBaseURL = firstNonEmpty(hp.BaseURL, provider.BaseURL)
		resolved.ProviderAPIKeyEnv = firstNonEmpty(hp.APIKeyEnv, provider.APIKeyEnv)
	} else {
		resolved.ProviderName = firstNonEmpty(provider.Name, resolved.ProviderID)
		resolved.ProviderBaseURL = provider.BaseURL
		resolved.ProviderAPIKeyEnv = provider.APIKeyEnv
	}
	for _, model := range provider.Models {
		if model.ID != modelID {
			continue
		}
		if hm, ok := model.Harnesses[harness]; ok {
			if hm.Disabled {
				defaultProvider, defaultModel := catalogDefaultForHarness(catalog, harness)
				return catalogProviderModelConfig(catalog, harness, defaultProvider, defaultModel)
			}
			if hm.ID != "" {
				resolved.ModelID = hm.ID
			}
		}
		break
	}
	return resolved
}

func catalogHarnessName(harness string) string {
	switch strings.TrimSpace(harness) {
	case "", "codex":
		return "opencode"
	default:
		return strings.TrimSpace(harness)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *RunnerRPCService) ReportTaskEvents(ctx context.Context, req *connect.Request[runnerv1.ReportTaskEventsRequest]) (*connect.Response[runnerv1.ReportTaskEventsResponse], error) {
	if req.Msg.RunnerId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	if len(req.Msg.Events) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("events are required"))
	}
	for _, event := range req.Msg.Events {
		if err := s.recordTaskEvent(ctx, req.Msg.RunnerId, event); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&runnerv1.ReportTaskEventsResponse{}), nil
}

func (s *RunnerRPCService) upsertRunner(ctx context.Context, info *runnerv1.RunnerInfo) error {
	if info == nil || info.RunnerId == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	now := time.Now().UTC()
	status := info.Status
	if status == "" {
		status = "active"
	}
	metadata, err := json.Marshal(info)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	startedAt := parseOptionalTime(info.StartedAt)
	if err := s.db.UpsertRunnerHeartbeat(ctx, repository.UpsertRunnerHeartbeatParams{
		ID:             info.RunnerId,
		Status:         status,
		ImageRef:       nullString(info.ImageRef),
		ImageDigest:    nullString(info.ImageDigest),
		Version:        nullString(info.Version),
		MaxConcurrent:  info.MaxConcurrent,
		RunningTasks:   info.RunningTasks,
		AvailableSlots: info.AvailableSlots,
		TotalStarted:   info.TotalStarted,
		TotalCompleted: info.TotalCompleted,
		TotalErrors:    info.TotalErrors,
		StartedAt:      startedAt,
		FirstSeenAt:    now,
		LastSeenAt:     now,
		UpdatedAt:      now,
		Metadata:       metadata,
	}); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

func (s *RunnerRPCService) RequestDrain(runnerID string) {
	s.drainRequests.Store(runnerID, true)
}

func (s *RunnerRPCService) runnerCommands(ctx context.Context, info *runnerv1.RunnerInfo) ([]*runnerv1.RunnerCommand, error) {
	commands := make([]*runnerv1.RunnerCommand, 0)
	if info != nil {
		if _, draining := s.drainRequests.LoadAndDelete(info.RunnerId); draining {
			commands = append(commands, &runnerv1.RunnerCommand{Type: "drain"})
		}
	}
	if info == nil || len(info.CurrentTaskIds) == 0 {
		return commands, nil
	}
	now := time.Now().UTC()
	lease := sql.NullTime{Time: now.Add(defaultTaskLeaseSec * time.Second), Valid: true}
	rows, err := s.db.ListHeartbeatTasks(ctx, repository.ListHeartbeatTasksParams{
		RunnerID: sql.NullString{String: info.RunnerId, Valid: true},
		Ids:      info.CurrentTaskIds,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	statusByID := make(map[string]repository.ListHeartbeatTasksRow, len(rows))
	for _, row := range rows {
		statusByID[row.ID] = row
	}
	runningIDs := make([]string, 0, len(statusByID))
	for _, taskID := range info.CurrentTaskIds {
		row, ok := statusByID[taskID]
		if !ok {
			continue
		}
		switch row.Status {
		case "cancelled":
			reason := row.Error.String
			if reason == "" {
				reason = "cancelled by operator"
			}
			commands = append(commands, &runnerv1.RunnerCommand{Type: "cancel", TaskId: row.ID, Reason: reason})
		case "pending":
			commands = append(commands, &runnerv1.RunnerCommand{Type: "cancel", TaskId: row.ID, Reason: "lease reclaimed; please stop"})
		case "running":
			runningIDs = append(runningIDs, row.ID)
		}
	}
	if len(runningIDs) > 0 {
		if _, err := s.db.RenewRunningTaskLeases(ctx, repository.RenewRunningTaskLeasesParams{
			LeaseExpiresAt: lease,
			UpdatedAt:      now,
			LastEventAt:    sql.NullTime{Time: now, Valid: true},
			RunnerID:       sql.NullString{String: info.RunnerId, Valid: true},
			Ids:            runningIDs,
		}); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return commands, nil
}

func (s *RunnerRPCService) claimOnce(ctx context.Context, runnerID string, lease time.Duration) (repository.ChetterTask, error) {
	if runnerID == "" {
		return repository.ChetterTask{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	var claimed repository.ChetterTask
	var eventID string
	var eventPayload json.RawMessage
	var eventCreatedAt time.Time
	eventID, err := randomID("evt")
	if err != nil {
		return repository.ChetterTask{}, connect.NewError(connect.CodeInternal, err)
	}
	err = withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		task, err := q.GetClaimableTaskForUpdate(ctx, sql.NullString{String: runnerID, Valid: true})
		if errors.Is(err, sql.ErrNoRows) {
			return errNoClaimableTask
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		eventCreatedAt = now
		rows, err := q.MarkTaskClaimed(ctx, repository.MarkTaskClaimedParams{
			RunnerID:       nullString(runnerID),
			ClaimedAt:      sql.NullTime{Time: now, Valid: true},
			LeaseExpiresAt: sql.NullTime{Time: now.Add(lease), Valid: true},
			StartedAt:      sql.NullTime{Time: now, Valid: true},
			UpdatedAt:      now,
			LastEventAt:    sql.NullTime{Time: now, Valid: true},
			ID:             task.ID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return errNoClaimableTask
		}
		if _, err := q.MarkSessionRunRunningByTask(ctx, repository.MarkSessionRunRunningByTaskParams{
			StartedAt: sql.NullTime{Time: now, Valid: true},
			UpdatedAt: now,
			TaskID:    task.ID,
		}); err != nil {
			return err
		}
		task.Status = "running"
		task.RunnerID = nullString(runnerID)
		task.ClaimedAt = sql.NullTime{Time: now, Valid: true}
		task.LeaseExpiresAt = sql.NullTime{Time: now.Add(lease), Valid: true}
		task.StartedAt = sql.NullTime{Time: now, Valid: true}
		task.UpdatedAt = now
		task.LastEventAt = sql.NullTime{Time: now, Valid: true}
		task.Attempt++
		eventPayload, _ = json.Marshal(map[string]any{
			"task_id":   task.ID,
			"runner_id": runnerID,
			"status":    "running",
			"summary":   "Task claimed by runner",
		})
		if err := q.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
			ID:        eventID,
			TaskID:    task.ID,
			Subject:   fmt.Sprintf("%s.%s.%s", runnerEventSubject, runnerID, task.ID),
			Status:    "running",
			EventType: "task.claimed",
			Payload:   eventPayload,
			CreatedAt: now,
		}); err != nil {
			return err
		}
		claimed = task
		return nil
	})
	if err == nil {
		if s.eventBus != nil {
			s.eventBus.PublishTaskEvent(claimed.ID, eventID, "running", "task.claimed", "Task claimed by runner", string(eventPayload), eventCreatedAt.Format(time.RFC3339))
		}
		if s.callbacks != nil {
			dispatch := TaskEventCallbackContext{
				ID:        eventID,
				TaskID:    claimed.ID,
				TeamID:    claimed.TeamID.String,
				Subject:   fmt.Sprintf("%s.%s.%s", runnerEventSubject, runnerID, claimed.ID),
				Status:    "running",
				EventType: "task.claimed",
				Summary:   "Task claimed by runner",
				Payload:   eventPayload,
				CreatedAt: eventCreatedAt,
			}
			go func() {
				callbackCtx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
				defer cancel()
				s.callbacks.DispatchTaskEventCallbacks(callbackCtx, dispatch)
			}()
		}
	}
	return claimed, err
}

func (s *RunnerRPCService) recordTaskEvent(ctx context.Context, runnerID string, event *runnerv1.TaskEvent) error {
	if runnerID == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("runner_id is required"))
	}
	if event == nil || event.TaskId == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task_id is required"))
	}
	now := time.Now().UTC()
	payload := json.RawMessage(event.PayloadJson)
	if len(payload) == 0 || !json.Valid(payload) {
		data, err := json.Marshal(event)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		payload = data
	}
	eventID, err := randomID("evt")
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	status := event.Status
	if status == "" {
		status = "running"
	}
	errorCategory := normalizeErrorCategory(event.ErrorCategory)
	if errorCategory == "" && statusIsErrorCategoryCandidate(status) {
		errorCategory = classifyTaskErrorCategory(status, event.Error)
	}
	isHeartbeat := status == "running" && isHeartbeatSummary(event.Summary)
	eventType := taskEventType(status, errorCategory, isHeartbeat)
	lease := sql.NullTime{Time: now.Add(defaultTaskLeaseSec * time.Second), Valid: status == "running"}
	eventInsert := repository.InsertTaskEventParams{
		ID:        eventID,
		TaskID:    event.TaskId,
		Subject:   fmt.Sprintf("%s.%s.%s", runnerEventSubject, runnerID, event.TaskId),
		Status:    status,
		EventType: eventType,
		Payload:   payload,
		CreatedAt: now,
	}
	updateParams := repository.UpdateTaskFromRunnerEventParams{
		Status:            status,
		Summary:           nullString(event.Summary),
		Error:             nullString(event.Error),
		ErrorCategory:     errorCategory,
		SessionExport:     nullString(event.SessionExport),
		ProviderID:        event.ProviderId,
		ModelID:           event.ModelId,
		VariantID:         event.VariantId,
		OpencodeSessionID: event.OpencodeSessionId,
		RunnerImageDigest: event.RunnerImageDigest,
		LeaseExpiresAt:    lease,
		StartedAt:         parseOptionalTime(event.StartedAt),
		EndedAt:           parseOptionalTime(event.EndedAt),
		UpdatedAt:         now,
		LastEventAt:       sql.NullTime{Time: now, Valid: true},
		ID:                event.TaskId,
		RunnerID:          sql.NullString{String: runnerID, Valid: true},
	}
	skipEventRow := isHeartbeat && !s.shouldStoreHeartbeat(event.TaskId)
	err = withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		rows, err := q.UpdateTaskFromRunnerEvent(ctx, updateParams)
		if err != nil {
			return err
		}
		if rows == 0 {
			return errTaskNotClaimed
		}
		if terminalRunStatus, terminalSessionStatus, ok := sessionTerminalStatuses(status); ok {
			startedAt := parseOptionalTime(event.StartedAt)
			endedAt := parseOptionalTime(event.EndedAt)
			if !endedAt.Valid {
				endedAt = sql.NullTime{Time: now, Valid: true}
			}
			if _, err := q.MarkSessionRunTerminalByTask(ctx, repository.MarkSessionRunTerminalByTaskParams{
				Status:        terminalRunStatus,
				Summary:       nullString(event.Summary),
				Error:         nullString(event.Error),
				SessionExport: nullString(event.SessionExport),
				StartedAt:     startedAt,
				EndedAt:       endedAt,
				UpdatedAt:     now,
				TaskID:        event.TaskId,
			}); err != nil {
				return err
			}

			sessionStatus := terminalSessionStatus
			if terminalSessionStatus == "completed" && event.CheckpointPath != "" && event.WorkspacePath != "" {
				var session repository.ChetterAgentSession
				if session, err = q.GetAgentSessionByTaskID(ctx, event.TaskId); err == nil && session.ResumeMode == "gvisor_checkpoint" {
					chkID, _ := randomID("chk")
					containerName := "chetter-task-" + event.TaskId
					if err := q.InsertAgentSessionCheckpoint(ctx, repository.InsertAgentSessionCheckpointParams{
						ID:             chkID,
						AgentSessionID: session.ID,
						RunnerID:       runnerID,
						CheckpointPath: event.CheckpointPath,
						WorkspacePath:  event.WorkspacePath,
						ContainerName:  nullString(containerName),
						Status:         "ready",
						CreatedAt:      now,
						UpdatedAt:      now,
					}); err != nil {
						return err
					}
					if _, err := q.PauseAgentSessionByTaskID(ctx, repository.PauseAgentSessionByTaskIDParams{
						Status:         "paused_waiting_review",
						PinnedRunnerID: nullString(runnerID),
						CheckpointID:   nullString(chkID),
						WorkspacePath:  nullString(event.WorkspacePath),
						ContainerName:  nullString(containerName),
						PausedAt:       sql.NullTime{Time: now, Valid: true},
						UpdatedAt:      now,
						TaskID:         event.TaskId,
					}); err != nil {
						return err
					}
					slog.Info("agent session paused with checkpoint", "session_id", session.ID, "checkpoint_id", chkID, "runner_id", runnerID)
					sessionStatus = ""
				}
			}

			if sessionStatus != "" {
				if _, err := q.MarkAgentSessionTerminalByTask(ctx, repository.MarkAgentSessionTerminalByTaskParams{
					Status:           sessionStatus,
					HarnessSessionID: event.OpencodeSessionId,
					Error:            nullString(event.Error),
					UpdatedAt:        now,
					TaskID:           event.TaskId,
				}); err != nil {
					return err
				}
			}
		}
		if !skipEventRow {
			return q.InsertTaskEvent(ctx, eventInsert)
		}
		return nil
	})
	if errors.Is(err, errTaskNotClaimed) {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task is not running for runner %s", runnerID))
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if s.eventBus != nil && !skipEventRow {
		s.eventBus.PublishTaskEvent(event.TaskId, eventID, status, eventType, event.Summary, string(payload), now.Format(time.RFC3339))
	}
	if s.callbacks != nil && !skipEventRow {
		if task, err := s.db.GetTaskByID(ctx, event.TaskId); err == nil {
			dispatch := TaskEventCallbackContext{
				ID:            eventID,
				TaskID:        event.TaskId,
				TeamID:        task.TeamID.String,
				Subject:       eventInsert.Subject,
				Status:        status,
				EventType:     eventType,
				Summary:       event.Summary,
				Error:         event.Error,
				ErrorCategory: errorCategory,
				Payload:       payload,
				CreatedAt:     now,
			}
			go func() {
				callbackCtx, cancel := context.WithTimeout(context.Background(), eventHandlerTimeout)
				defer cancel()
				s.callbacks.DispatchTaskEventCallbacks(callbackCtx, dispatch)
			}()
		} else {
			slog.Warn("could not load task for event callbacks", "task_id", event.TaskId, "error", err)
		}
	}
	return nil
}

func sessionTerminalStatuses(taskStatus string) (runStatus, sessionStatus string, ok bool) {
	switch taskStatus {
	case "done", "completed":
		return "completed", "completed", true
	case "error":
		return "failed", "error", true
	case "cancelled":
		return "cancelled", "error", true
	default:
		return "", "", false
	}
}

func statusIsErrorCategoryCandidate(status string) bool {
	return status == "error" || status == "cancelled"
}

func normalizeErrorCategory(category string) string {
	switch category {
	case "budget_exceeded", "model_error", "runtime_error", "timeout", "stuck", "cancelled", "unknown":
		return category
	default:
		return ""
	}
}

func classifyTaskErrorCategory(status, message string) string {
	if status == "cancelled" {
		return "cancelled"
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "budget"), strings.Contains(lower, "cost limit"), strings.Contains(lower, "max budget"):
		return "budget_exceeded"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "context deadline"), strings.Contains(lower, "lease expired"):
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

func taskEventType(status, errorCategory string, heartbeat bool) string {
	if heartbeat {
		return "task.heartbeat"
	}
	switch status {
	case "done", "completed":
		return "task.completed"
	case "error":
		if errorCategory == "" {
			errorCategory = "unknown"
		}
		return "task.failed." + errorCategory
	case "cancelled":
		return "task.cancelled"
	case "running":
		return "task.progress"
	default:
		return "task." + sanitizeEventTypePart(status)
	}
}

func sanitizeEventTypePart(part string) string {
	if part == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(part) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func taskToProto(task repository.ChetterTask, resumeCheckpointPath, resumeWorkspacePath string) *runnerv1.Task {
	var skills []string
	_ = json.Unmarshal(task.Skills, &skills)
	env := map[string]string{}
	_ = json.Unmarshal(task.Env, &env)
	harness := env["__chetter_harness"]
	delete(env, "__chetter_harness")
	return &runnerv1.Task{
		TaskId:                 task.ID,
		AgentImage:             task.AgentImage.String,
		Prompt:                 task.Prompt,
		GitUrl:                 task.GitUrl.String,
		GitRef:                 task.GitRef.String,
		Agent:                  task.Agent.String,
		ProviderId:             task.ProviderID.String,
		ModelId:                task.ModelID.String,
		VariantId:              task.VariantID.String,
		Skills:                 skills,
		TimeoutSeconds:         task.TimeoutSec,
		MaxMemoryMb:            defaultMaxMemoryMB,
		MaxCpu:                 defaultMaxCPU,
		Env:                    env,
		Attempt:                task.Attempt,
		CheckpointAfterSuccess: task.CheckpointAfterSuccess,
		ResumeCheckpointPath:   resumeCheckpointPath,
		ResumeWorkspacePath:    resumeWorkspacePath,
		Harness:                harness,
	}
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func parseOptionalTime(value string) sql.NullTime {
	if value == "" {
		return sql.NullTime{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: parsed.UTC(), Valid: true}
}

func isHeartbeatSummary(summary string) bool {
	return strings.Contains(summary, "server.heartbeat")
}

func (s *RunnerRPCService) shouldStoreHeartbeat(taskID string) bool {
	now := time.Now()
	if v, ok := s.heartbeatSeen.Load(taskID); ok {
		if last, ok := v.(time.Time); ok && now.Sub(last) < heartbeatEventMinInterval {
			return false
		}
	}
	s.heartbeatSeen.Store(taskID, now)
	return true
}
