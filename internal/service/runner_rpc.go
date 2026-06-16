package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/repository"
)

const (
	defaultClaimWaitSec = 30
	defaultTaskLeaseSec = 60
	claimPollInterval   = time.Second
	runnerEventSubject  = "connect.runner"
)

var errNoClaimableTask = errors.New("no claimable task")
var errTaskNotClaimed = errors.New("task is not claimed by runner")

type RunnerRPCService struct {
	db    *repository.Queries
	rawDB *sql.DB
}

func NewRunnerRPCService(db *repository.Queries, rawDB *sql.DB) *RunnerRPCService {
	return &RunnerRPCService{db: db, rawDB: rawDB}
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
			return connect.NewResponse(&runnerv1.ClaimTaskResponse{Task: taskToProto(task)}), nil
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

func (s *RunnerRPCService) runnerCommands(ctx context.Context, info *runnerv1.RunnerInfo) ([]*runnerv1.RunnerCommand, error) {
	commands := make([]*runnerv1.RunnerCommand, 0)
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
	err := withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		task, err := q.GetClaimableTaskForUpdate(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errNoClaimableTask
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
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
		task.Status = "running"
		task.RunnerID = nullString(runnerID)
		task.ClaimedAt = sql.NullTime{Time: now, Valid: true}
		task.LeaseExpiresAt = sql.NullTime{Time: now.Add(lease), Valid: true}
		task.StartedAt = sql.NullTime{Time: now, Valid: true}
		task.UpdatedAt = now
		task.LastEventAt = sql.NullTime{Time: now, Valid: true}
		task.Attempt++
		claimed = task
		return nil
	})
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
	lease := sql.NullTime{Time: now.Add(defaultTaskLeaseSec * time.Second), Valid: status == "running"}
	eventInsert := repository.InsertTaskEventParams{
		ID:        eventID,
		TaskID:    event.TaskId,
		Subject:   fmt.Sprintf("%s.%s.%s", runnerEventSubject, runnerID, event.TaskId),
		Status:    status,
		Payload:   payload,
		CreatedAt: now,
	}
	updateParams := repository.UpdateTaskFromRunnerEventParams{
		Status:            status,
		Summary:           nullString(event.Summary),
		Error:             nullString(event.Error),
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
	err = withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		rows, err := q.UpdateTaskFromRunnerEvent(ctx, updateParams)
		if err != nil {
			return err
		}
		if rows == 0 {
			return errTaskNotClaimed
		}
		return q.InsertTaskEvent(ctx, eventInsert)
	})
	if errors.Is(err, errTaskNotClaimed) {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task is not running for runner %s", runnerID))
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

func taskToProto(task repository.ChetterTask) *runnerv1.Task {
	var skills []string
	_ = json.Unmarshal(task.Skills, &skills)
	env := map[string]string{}
	_ = json.Unmarshal(task.Env, &env)
	return &runnerv1.Task{
		TaskId:         task.ID,
		AgentImage:     task.AgentImage.String,
		Prompt:         task.Prompt,
		GitUrl:         task.GitUrl.String,
		GitRef:         task.GitRef.String,
		Agent:          task.Agent.String,
		ProviderId:     task.ProviderID.String,
		ModelId:        task.ModelID.String,
		VariantId:      task.VariantID.String,
		Skills:         skills,
		TimeoutSeconds: task.TimeoutSec,
		MaxMemoryMb:    defaultMaxMemoryMB,
		MaxCpu:         defaultMaxCPU,
		Env:            env,
		Attempt:        task.Attempt,
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
