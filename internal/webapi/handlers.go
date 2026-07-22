package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/service"
	"github.com/flatout-works/chetter/internal/store"
)

// --- Conversion helpers ---

func protoTask(t service.TaskToolRecord) *apiv1.Task {
	return &apiv1.Task{
		Id:               t.ID,
		TeamId:           t.TeamID,
		Status:           t.Status,
		Prompt:           t.Prompt,
		GitUrl:           t.GitURL,
		GitRef:           t.GitRef,
		AgentImage:       t.AgentImage,
		Agent:            t.Agent,
		ProviderId:       t.ProviderID,
		ModelId:          t.ModelID,
		VariantId:        t.VariantID,
		ExecutionId:      t.ExecutionID,
		Skills:           t.Skills,
		McpEndpoints:     t.McpEndpoints,
		Env:              t.Env,
		TimeoutSec:       int32(t.TimeoutSec),
		Summary:          t.Summary,
		Error:            t.Error,
		ErrorCategory:    t.ErrorCategory,
		CreatedAt:        t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        t.UpdatedAt.Format(time.RFC3339),
		StartedAt:        optTimeStr(t.StartedAt),
		EndedAt:          optTimeStr(t.EndedAt),
		AgentSessionId:   t.AgentSessionID,
		TriggerName:      t.TriggerName,
		TriggerType:      t.TriggerType,
		SubmissionSource: t.SubmissionSource,
		GitIdentityId:    t.GitIdentityID,
		TokenUsage: &apiv1.TokenUsage{
			InputTokens:      t.TotalInputTokens,
			OutputTokens:     t.TotalOutputTokens,
			CacheReadTokens:  t.TotalCacheReadTokens,
			CacheWriteTokens: t.TotalCacheWriteTokens,
			ReasoningTokens:  t.TotalReasoningTokens,
			CostCents:        t.CostCents,
		},
	}
}

func optTimeStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timeStrPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		slog.Debug("parseTime: invalid timestamp", "input", s, "error", err)
	}
	return t
}

func protoSession(s service.AgentSessionRecord) *apiv1.AgentSession {
	return &apiv1.AgentSession{
		Id:               s.ID,
		TeamId:           s.TeamID,
		Status:           s.Status,
		ResumeMode:       s.ResumeMode,
		PinnedRunnerId:   s.PinnedRunnerID,
		CheckpointId:     s.CheckpointID,
		HarnessSessionId: s.HarnessSessionID,
		GitUrl:           s.GitURL,
		GitRef:           s.GitRef,
		AgentImage:       s.AgentImage,
		Agent:            s.Agent,
		ProviderId:       s.ProviderID,
		ModelId:          s.ModelID,
		VariantId:        s.VariantID,
		CreatedAt:        s.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        s.UpdatedAt.Format(time.RFC3339),
		PausedAt:         optTimeStr(s.PausedAt),
		ExpiresAt:        optTimeStr(s.ExpiresAt),
		PauseReason:      s.PauseReason,
		Error:            s.Error,
		PromptCount:      s.PromptCount,
		TaskId:           s.TaskID,
		Sequence:         s.Sequence,
	}
}

func protoPrompt(r service.UserPromptRecord) *apiv1.UserPrompt {
	attempts := make([]*apiv1.ExecutionAttempt, len(r.Attempts))
	for i, attempt := range r.Attempts {
		attempts[i] = protoExecutionAttempt(attempt)
	}
	return &apiv1.UserPrompt{
		Id:                 r.ID,
		AgentSessionId:     r.AgentSessionID,
		TaskId:             r.TaskID,
		Status:             r.Status,
		RequiredRunnerId:   r.RequiredRunnerID,
		Summary:            r.Summary,
		Error:              r.Error,
		Prompt:             r.Prompt,
		Sequence:           r.Sequence,
		SourceUserPromptId: r.SourceUserPromptID,
		Attempts:           attempts,
		CreatedAt:          r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          r.UpdatedAt.Format(time.RFC3339),
		StartedAt:          optTimeStr(r.StartedAt),
		EndedAt:            optTimeStr(r.EndedAt),
	}
}

func protoExecutionAttempt(a service.ExecutionAttemptRecord) *apiv1.ExecutionAttempt {
	return &apiv1.ExecutionAttempt{
		Id: a.ID, UserPromptId: a.UserPromptID, Sequence: a.Sequence, Status: a.Status,
		RunnerId: a.RunnerID, RequiredRunnerId: a.RequiredRunnerID,
		ClaimedAt: optTimeStr(a.ClaimedAt), LeaseExpiresAt: optTimeStr(a.LeaseExpiresAt),
		StartedAt: optTimeStr(a.StartedAt), EndedAt: optTimeStr(a.EndedAt),
		WorkspacePath: a.WorkspacePath, ContainerName: a.ContainerName,
		HarnessExecutionId: a.HarnessExecutionID, Summary: a.Summary, Error: a.Error,
		ErrorCategory: a.ErrorCategory, CreatedAt: a.CreatedAt.Format(time.RFC3339), UpdatedAt: a.UpdatedAt.Format(time.RFC3339),
		TokenUsage: &apiv1.TokenUsage{InputTokens: a.TotalInputTokens, OutputTokens: a.TotalOutputTokens,
			CacheReadTokens: a.TotalCacheReadTokens, CacheWriteTokens: a.TotalCacheWriteTokens,
			ReasoningTokens: a.TotalReasoningTokens, CostCents: a.CostCents},
	}
}

func protoTrigger(t store.TriggerRecord) *apiv1.Trigger {
	return &apiv1.Trigger{
		Id:            t.ID,
		TeamId:        t.TeamID,
		Name:          t.Name,
		TriggerType:   t.TriggerType,
		TriggerConfig: t.TriggerConfig,
		CronExpr:      t.CronExpr,
		Prompt:        t.Prompt,
		GitUrl:        t.GitURL,
		GitRef:        t.GitRef,
		AgentImage:    t.AgentImage,
		Agent:         t.Agent,
		ProviderId:    t.ProviderID,
		ModelId:       t.ModelID,
		VariantId:     t.VariantID,
		Harness:       t.Harness,
		Skills:        t.Skills,
		TimeoutSec:    int32(t.TimeoutSec),
		Enabled:       t.Enabled,
		CreatedAt:     t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     t.UpdatedAt.Format(time.RFC3339),
		LastRunAt:     optTimeStr(t.LastRunAt),
		NextRunAt:     optTimeStr(t.NextRunAt),
		SourceId:      optStr(t.SourceID),
		SourceRepoUrl: optStr(t.SourceRepoURL),
		SourceBranch:  optStr(t.SourceBranch),
		SourcePath:    optStr(t.SourcePath),
	}
}

func protoEvent(e service.TaskEventRecord) *apiv1.TaskEvent {
	return &apiv1.TaskEvent{
		Id:          e.ID,
		TaskId:      "",
		Subject:     e.Subject,
		Status:      e.Status,
		EventType:   e.EventType,
		ExecutionId: e.ExecutionID,
		Payload:     e.Payload,
		CreatedAt:   e.CreatedAt.Format(time.RFC3339),
	}
}

func protoFleetHealth(h store.RunnerFleetHealth) *apiv1.RunnerFleetHealth {
	out := &apiv1.RunnerFleetHealth{
		TotalTasks:   int32(h.TotalTasks),
		PendingTasks: int32(h.PendingTasks),
		RunningTasks: int32(h.RunningTasks),
		StaleTasks:   int32(h.StaleTasks),
		DoneTasks:    int32(h.DoneTasks),
		ErrorTasks:   int32(h.ErrorTasks),
		FleetActive:  h.FleetActive,
		GeneratedAt:  h.GeneratedAt.Format(time.RFC3339),
	}
	for _, img := range h.RunnerImages {
		out.RunnerImages = append(out.RunnerImages, &apiv1.RunnerImageInfo{
			Image:     img.ImageRef,
			Count:     int32(img.RunnerCount),
			RunnerIds: nil,
		})
	}
	for _, r := range h.Runners {
		out.Runners = append(out.Runners, protoRunnerInfo(r))
	}
	for _, t := range h.RunningTaskInfos {
		out.RunningTaskInfos = append(out.RunningTaskInfos, protoRunningTaskInfo(t))
	}
	return out
}

func protoRunnerInfo(r store.RunnerInfo) *apiv1.RunnerInfo {
	return &apiv1.RunnerInfo{
		RunnerId:       r.ID,
		Status:         r.Status,
		ImageRef:       r.ImageRef,
		ImageDigest:    r.ImageDigest,
		Version:        r.Version,
		MaxConcurrent:  int32(r.MaxConcurrent),
		RunningTasks:   int32(r.RunningTasks),
		AvailableSlots: int32(r.AvailableSlots),
		TotalStarted:   r.TotalStarted,
		TotalCompleted: r.TotalCompleted,
		TotalErrors:    r.TotalErrors,
		CurrentTaskIds: r.CurrentTaskIDs,
		StartedAt:      timeStrPtr(r.StartedAt),
		LastHeartbeat:  r.LastSeenAt.Format(time.RFC3339),
	}
}

func protoRunningTaskInfo(t store.RunningTaskInfo) *apiv1.RunningTaskInfo {
	return &apiv1.RunningTaskInfo{
		TaskId:     t.TaskID,
		Summary:    t.Summary,
		AgentImage: t.ImageDigest,
		StartedAt:  timeStrPtr(t.StartedAt),
		IsStale:    t.IsStale,
	}
}

// --- TaskServiceHandler ---

type taskHandler struct {
	svc *service.Service
	bus *EventBus
}

func (h *taskHandler) SubmitTask(ctx context.Context, req *connect.Request[apiv1.SubmitTaskRequest]) (*connect.Response[apiv1.SubmitTaskResponse], error) {
	task, err := h.svc.SubmitTask(ctx, service.SubmitTaskRequest{
		Prompt:           req.Msg.Prompt,
		GitURL:           req.Msg.GitUrl,
		GitRef:           req.Msg.GitRef,
		AgentImage:       req.Msg.AgentImage,
		Agent:            req.Msg.Agent,
		ProviderID:       req.Msg.ProviderId,
		ModelID:          req.Msg.ModelId,
		VariantID:        req.Msg.VariantId,
		Skills:           req.Msg.Skills,
		McpEndpoints:     req.Msg.McpEndpoints,
		Env:              req.Msg.Env,
		Harness:          req.Msg.Harness,
		TimeoutSec:       int(req.Msg.TimeoutSec),
		SessionMode:      req.Msg.SessionMode,
		PauseReason:      req.Msg.PauseReason,
		TTLHours:         int(req.Msg.TtlHours),
		SubmissionSource: "ui",
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.SubmitTaskResponse{Task: protoTask(service.TaskToolRecord{
		ID: task.ID, TeamID: task.TeamID, Status: task.Status, Prompt: task.Prompt,
		GitURL: task.GitURL, GitRef: task.GitRef, AgentImage: task.AgentImage,
		Agent: task.Agent, ProviderID: task.ProviderID, ModelID: task.ModelID,
		VariantID: task.VariantID, Skills: task.Skills, McpEndpoints: task.McpEndpoints, Env: task.Env,
		TimeoutSec: task.TimeoutSec, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
		StartedAt: task.StartedAt, EndedAt: task.EndedAt,
		TriggerName: task.TriggerName, TriggerType: task.TriggerType, SubmissionSource: task.SubmissionSource,
	})}), nil
}

func (h *taskHandler) GetTask(ctx context.Context, req *connect.Request[apiv1.GetTaskRequest]) (*connect.Response[apiv1.GetTaskResponse], error) {
	task, err := h.svc.GetTask(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&apiv1.GetTaskResponse{Task: protoTask(task)}), nil
}

func (h *taskHandler) ListTasks(ctx context.Context, req *connect.Request[apiv1.ListTasksRequest]) (*connect.Response[apiv1.ListTasksResponse], error) {
	tasks, err := h.svc.ListTasks(ctx, req.Msg.Status, int(req.Msg.Limit), int(req.Msg.Offset), req.Msg.Search, req.Msg.TeamIds, req.Msg.Repos)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.Task, len(tasks))
	for i, t := range tasks {
		out[i] = protoTask(t)
	}
	return connect.NewResponse(&apiv1.ListTasksResponse{Tasks: out}), nil
}

func (h *taskHandler) CancelTask(ctx context.Context, req *connect.Request[apiv1.CancelTaskRequest]) (*connect.Response[apiv1.CancelTaskResponse], error) {
	task, err := h.svc.CancelTask(ctx, req.Msg.TaskId, req.Msg.Reason)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.CancelTaskResponse{Task: protoTask(task)}), nil
}

func (h *taskHandler) ExtendTask(ctx context.Context, req *connect.Request[apiv1.ExtendTaskRequest]) (*connect.Response[apiv1.ExtendTaskResponse], error) {
	task, err := h.svc.ExtendTaskTimeout(ctx, req.Msg.TaskId, int(req.Msg.ExtensionSec))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&apiv1.ExtendTaskResponse{Task: protoTask(task)}), nil
}

func (h *taskHandler) ExportTask(ctx context.Context, req *connect.Request[apiv1.ExportTaskRequest]) (*connect.Response[apiv1.ExportTaskResponse], error) {
	export, err := h.svc.ExportTask(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&apiv1.ExportTaskResponse{Export: export}), nil
}

func (h *taskHandler) RecoverTask(ctx context.Context, req *connect.Request[apiv1.RecoverTaskRequest]) (*connect.Response[apiv1.RecoverTaskResponse], error) {
	task, err := h.svc.RecoverTask(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.RecoverTaskResponse{Task: protoTask(task)}), nil
}

func (h *taskHandler) ClearQueue(ctx context.Context, req *connect.Request[apiv1.ClearQueueRequest]) (*connect.Response[apiv1.ClearQueueResponse], error) {
	if !req.Msg.Confirm {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("confirm must be true"))
	}
	cancelled, err := h.svc.ClearQueue(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return connect.NewResponse(&apiv1.ClearQueueResponse{
		Cleared:               true,
		CancelledPendingTasks: int32(cancelled),
	}), nil
}

func (h *taskHandler) Whoami(ctx context.Context, req *connect.Request[apiv1.WhoamiRequest]) (*connect.Response[apiv1.WhoamiResponse], error) {
	out, err := h.svc.Whoami(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	protoTeams := make([]*apiv1.WhoamiTeamInfo, len(out.Teams))
	for i, t := range out.Teams {
		protoTeams[i] = &apiv1.WhoamiTeamInfo{Id: t.ID, Name: t.Name}
	}
	return connect.NewResponse(&apiv1.WhoamiResponse{
		IsAdmin:         out.IsAdmin,
		PrimaryTeamName: out.PrimaryTeamName,
		Teams:           protoTeams,
	}), nil
}

// --- EventServiceHandler ---

type eventHandler struct {
	svc *service.Service
}

func (h *eventHandler) GetTaskEvents(ctx context.Context, req *connect.Request[apiv1.GetTaskEventsRequest]) (*connect.Response[apiv1.GetTaskEventsResponse], error) {
	events, err := h.svc.GetTaskEvents(ctx, req.Msg.TaskId, int(req.Msg.Limit), int(req.Msg.Offset))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.TaskEvent, len(events))
	for i, e := range events {
		ev := protoEvent(e)
		ev.TaskId = req.Msg.TaskId
		out[i] = ev
	}
	return connect.NewResponse(&apiv1.GetTaskEventsResponse{Events: out}), nil
}

func (h *eventHandler) GetTaskProgress(ctx context.Context, req *connect.Request[apiv1.GetTaskProgressRequest]) (*connect.Response[apiv1.GetTaskProgressResponse], error) {
	page, err := h.svc.GetTaskProgress(ctx, req.Msg.TaskId, int(req.Msg.Limit), int(req.Msg.Offset))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.TaskProgressEntry, len(page.Entries))
	for i, e := range page.Entries {
		out[i] = &apiv1.TaskProgressEntry{
			Time:    e.Time.Format(time.RFC3339Nano),
			Status:  e.Status,
			Summary: e.Summary,
			Error:   e.Error,
		}
	}
	return connect.NewResponse(&apiv1.GetTaskProgressResponse{
		Entries:    out,
		HasMore:    page.HasMore,
		NextOffset: int32(page.NextOffset),
	}), nil
}

func (h *eventHandler) GetLatestTaskEvent(ctx context.Context, req *connect.Request[apiv1.GetLatestTaskEventRequest]) (*connect.Response[apiv1.GetLatestTaskEventResponse], error) {
	out, err := h.svc.GetLatestTaskEvent(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	ev := protoEvent(out.Event)
	ev.TaskId = req.Msg.TaskId
	return connect.NewResponse(&apiv1.GetLatestTaskEventResponse{
		Event:   ev,
		AgeSec:  int32(out.AgeSec),
		IsStale: out.IsStale,
	}), nil
}

// --- SessionServiceHandler ---

type sessionHandler struct {
	svc *service.Service
}

func (h *sessionHandler) ListSessions(ctx context.Context, req *connect.Request[apiv1.ListSessionsRequest]) (*connect.Response[apiv1.ListSessionsResponse], error) {
	sessions, err := h.svc.ListAgentSessions(ctx, req.Msg.Status, int(req.Msg.Limit), int(req.Msg.Offset), req.Msg.Search, req.Msg.TeamIds, req.Msg.Repos)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.AgentSession, len(sessions))
	for i, s := range sessions {
		out[i] = protoSession(s)
	}
	return connect.NewResponse(&apiv1.ListSessionsResponse{Sessions: out}), nil
}

func (h *sessionHandler) GetSession(ctx context.Context, req *connect.Request[apiv1.GetSessionRequest]) (*connect.Response[apiv1.GetSessionResponse], error) {
	session, prompts, err := h.svc.GetAgentSession(ctx, req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	protoPrompts := make([]*apiv1.UserPrompt, len(prompts))
	for i, prompt := range prompts {
		protoPrompts[i] = protoPrompt(prompt)
	}
	return connect.NewResponse(&apiv1.GetSessionResponse{
		Session: protoSession(session),
		Prompts: protoPrompts,
	}), nil
}

func (h *sessionHandler) ResumeSession(ctx context.Context, req *connect.Request[apiv1.ResumeSessionRequest]) (*connect.Response[apiv1.ResumeSessionResponse], error) {
	out, err := h.svc.ResumeAgentSession(ctx, req.Msg.SessionId, req.Msg.Prompt, int(req.Msg.TimeoutSec))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.ResumeSessionResponse{
		Task:   protoTask(out.Task),
		Prompt: protoPrompt(out.Prompt),
	}), nil
}

// --- TriggerServiceHandler ---

type triggerHandler struct {
	svc *service.Service
}

func (h *triggerHandler) CreateTrigger(ctx context.Context, req *connect.Request[apiv1.CreateTriggerRequest]) (*connect.Response[apiv1.CreateTriggerResponse], error) {
	triggerConfig := buildTriggerConfig(req.Msg.TriggerType, req.Msg.Repo, req.Msg.Event, req.Msg.MatchLabels, req.Msg.SessionMode, req.Msg.PauseReason, int(req.Msg.TtlHours))
	trigger, err := h.svc.CreateTrigger(ctx, store.TriggerInput{
		Name:          req.Msg.Name,
		TriggerType:   req.Msg.TriggerType,
		TriggerConfig: triggerConfig,
		CronExpr:      req.Msg.CronExpr,
		Prompt:        req.Msg.Prompt,
		GitURL:        req.Msg.GitUrl,
		GitRef:        req.Msg.GitRef,
		AgentImage:    req.Msg.AgentImage,
		Agent:         req.Msg.Agent,
		ProviderID:    req.Msg.ProviderId,
		ModelID:       req.Msg.ModelId,
		VariantID:     req.Msg.VariantId,
		Skills:        req.Msg.Skills,
		Harness:       req.Msg.Harness,
		TimeoutSec:    int(req.Msg.TimeoutSec),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.CreateTriggerResponse{Trigger: protoTrigger(trigger)}), nil
}

func (h *triggerHandler) UpdateTrigger(ctx context.Context, req *connect.Request[apiv1.UpdateTriggerRequest]) (*connect.Response[apiv1.UpdateTriggerResponse], error) {
	existing, err := h.svc.GetTriggerByName(ctx, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("get trigger %q: %w", req.Msg.Name, err))
	}
	enabled := existing.Enabled
	if req.Msg.Enabled != nil {
		enabled = *req.Msg.Enabled
	}
	triggerType := store.NonZero(req.Msg.TriggerType, existing.TriggerType)
	triggerConfig := service.MergeTriggerConfig(existing.TriggerConfig, req.Msg.Repo, req.Msg.Event, req.Msg.MatchLabels, req.Msg.SessionMode, req.Msg.PauseReason, int(req.Msg.TtlHours))
	merged := store.TriggerInput{
		Name:          req.Msg.Name,
		TriggerType:   triggerType,
		TriggerConfig: triggerConfig,
		CronExpr:      store.NonZero(req.Msg.CronExpr, existing.CronExpr),
		Prompt:        store.NonZero(req.Msg.Prompt, existing.Prompt),
		GitURL:        store.NonZero(req.Msg.GitUrl, existing.GitUrl.String),
		GitRef:        store.NonZero(req.Msg.GitRef, existing.GitRef.String),
		AgentImage:    store.NonZero(req.Msg.AgentImage, existing.AgentImage.String),
		Agent:         store.NonZero(req.Msg.Agent, existing.Agent.String),
		ProviderID:    store.NonZero(req.Msg.ProviderId, existing.ProviderID.String),
		ModelID:       store.NonZero(req.Msg.ModelId, existing.ModelID.String),
		VariantID:     store.NonZero(req.Msg.VariantId, existing.VariantID.String),
		Harness:       store.NonZero(req.Msg.Harness, existing.Harness.String),
		Skills:        store.NonNilSlice(req.Msg.Skills, triggerSkillsToStrings(existing.Skills)),
		TimeoutSec:    store.NonZeroInt(int(req.Msg.TimeoutSec), int(existing.TimeoutSec)),
	}
	trigger, err := h.svc.UpdateTrigger(ctx, req.Msg.Name, merged, enabled)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.UpdateTriggerResponse{Trigger: protoTrigger(trigger)}), nil
}

func (h *triggerHandler) ListTriggers(ctx context.Context, req *connect.Request[apiv1.ListTriggersRequest]) (*connect.Response[apiv1.ListTriggersResponse], error) {
	triggers, err := h.svc.ListTriggers(ctx, req.Msg.EnabledOnly, req.Msg.TriggerType, req.Msg.TeamIds, req.Msg.Repos)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.Trigger, len(triggers))
	for i, t := range triggers {
		out[i] = protoTrigger(t)
	}
	return connect.NewResponse(&apiv1.ListTriggersResponse{Triggers: out}), nil
}

func (h *triggerHandler) DeleteTrigger(ctx context.Context, req *connect.Request[apiv1.DeleteTriggerRequest]) (*connect.Response[apiv1.DeleteTriggerResponse], error) {
	if err := h.svc.DeleteTrigger(ctx, req.Msg.Name); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.DeleteTriggerResponse{Deleted: true}), nil
}

func (h *triggerHandler) RunTrigger(ctx context.Context, req *connect.Request[apiv1.RunTriggerRequest]) (*connect.Response[apiv1.RunTriggerResponse], error) {
	task, err := h.svc.RunTriggerNow(ctx, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.RunTriggerResponse{Task: protoTask(service.TaskToolRecord{
		ID: task.ID, TeamID: task.TeamID, Status: task.Status, Prompt: task.Prompt,
		GitURL: task.GitURL, GitRef: task.GitRef, AgentImage: task.AgentImage,
		Agent: task.Agent, ProviderID: task.ProviderID, ModelID: task.ModelID,
		VariantID: task.VariantID, Skills: task.Skills, McpEndpoints: task.McpEndpoints, Env: task.Env,
		TimeoutSec: task.TimeoutSec, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
		TriggerName: task.TriggerName, TriggerType: task.TriggerType, SubmissionSource: task.SubmissionSource,
	})}), nil
}

func (h *triggerHandler) ListTriggerRuns(ctx context.Context, req *connect.Request[apiv1.ListTriggerRunsRequest]) (*connect.Response[apiv1.ListTriggerRunsResponse], error) {
	runs, err := h.svc.ListTriggerRuns(ctx, req.Msg.TriggerName, int(req.Msg.Limit), int(req.Msg.Offset))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.TriggerRun, len(runs))
	for i, r := range runs {
		out[i] = &apiv1.TriggerRun{
			Id:          r.ID,
			TriggerName: r.TriggerName,
			TaskId:      r.TaskID,
			Status:      r.Status,
			TriggeredAt: r.TriggeredAt.Format(time.RFC3339),
			CreatedAt:   r.CreatedAt.Format(time.RFC3339),
		}
	}
	return connect.NewResponse(&apiv1.ListTriggerRunsResponse{Runs: out}), nil
}

// --- FleetServiceHandler (unary only; streaming in streaming.go) ---

type fleetHandler struct {
	svc *service.Service
	bus *EventBus
}

func (h *fleetHandler) GetRunnerHealth(ctx context.Context, req *connect.Request[apiv1.GetRunnerHealthRequest]) (*connect.Response[apiv1.GetRunnerHealthResponse], error) {
	health, err := h.svc.GetRunnerHealth(ctx, req.Msg.IncludeTasks)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.GetRunnerHealthResponse{Health: protoFleetHealth(health)}), nil
}

// --- AdminServiceHandler ---

type adminHandler struct {
	svc *service.Service
}

func protoGitIdentity(identity service.GitIdentityRecord) *apiv1.GitIdentity {
	return &apiv1.GitIdentity{
		Id:             identity.ID,
		TeamId:         identity.TeamID,
		Name:           identity.Name,
		GitAuthorName:  identity.GitAuthorName,
		GitAuthorEmail: identity.GitAuthorEmail,
		CredentialType: identity.CredentialType,
		IsDefault:      identity.IsDefault,
		CreatedAt:      identity.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      identity.UpdatedAt.Format(time.RFC3339),
	}
}

func (h *adminHandler) CreateToken(ctx context.Context, req *connect.Request[apiv1.CreateTokenRequest]) (*connect.Response[apiv1.CreateTokenResponse], error) {
	teamNames := req.Msg.TeamNames
	if len(teamNames) == 0 && req.Msg.TeamName != "" {
		teamNames = []string{req.Msg.TeamName}
	}
	out, err := h.svc.CreateToken(ctx, teamNames, req.Msg.UserName, req.Msg.TokenName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.CreateTokenResponse{
		Token:     out.Token,
		TeamId:    out.TeamID,
		TeamName:  out.TeamName,
		UserId:    out.UserID,
		UserName:  out.UserName,
		TeamIds:   out.TeamIDs,
		TeamNames: out.TeamNames,
	}), nil
}

func (h *adminHandler) ListTokens(ctx context.Context, req *connect.Request[apiv1.ListTokensRequest]) (*connect.Response[apiv1.ListTokensResponse], error) {
	tokens, err := h.svc.ListTokens(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.TokenInfo, len(tokens))
	for i, t := range tokens {
		out[i] = &apiv1.TokenInfo{
			Name:      t.Name,
			UserName:  t.UserName,
			TeamName:  t.TeamName,
			TeamNames: t.TeamNames,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
	}
	return connect.NewResponse(&apiv1.ListTokensResponse{Tokens: out}), nil
}

func (h *adminHandler) DeleteToken(ctx context.Context, req *connect.Request[apiv1.DeleteTokenRequest]) (*connect.Response[apiv1.DeleteTokenResponse], error) {
	if err := h.svc.DeleteToken(ctx, req.Msg.Name); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.DeleteTokenResponse{Deleted: true}), nil
}

func (h *adminHandler) CreateTeam(ctx context.Context, req *connect.Request[apiv1.CreateTeamRequest]) (*connect.Response[apiv1.CreateTeamResponse], error) {
	out, err := h.svc.CreateTeam(ctx, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.CreateTeamResponse{
		TeamId:    out.TeamID,
		TeamName:  out.TeamName,
		CreatedAt: out.CreatedAt.Format(time.RFC3339),
	}), nil
}

func (h *adminHandler) ListTeams(ctx context.Context, req *connect.Request[apiv1.ListTeamsRequest]) (*connect.Response[apiv1.ListTeamsResponse], error) {
	teams, err := h.svc.ListTeams(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.TeamInfo, len(teams))
	for i, t := range teams {
		out[i] = &apiv1.TeamInfo{
			Id:        t.ID,
			Name:      t.Name,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
	}
	return connect.NewResponse(&apiv1.ListTeamsResponse{Teams: out}), nil
}

func (h *adminHandler) DeleteTeam(ctx context.Context, req *connect.Request[apiv1.DeleteTeamRequest]) (*connect.Response[apiv1.DeleteTeamResponse], error) {
	if err := h.svc.DeleteTeam(ctx, req.Msg.Name); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.DeleteTeamResponse{Deleted: true}), nil
}

func (h *adminHandler) ListUsers(ctx context.Context, req *connect.Request[apiv1.ListUsersRequest]) (*connect.Response[apiv1.ListUsersResponse], error) {
	users, err := h.svc.ListUsers(ctx, req.Msg.TeamName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.UserInfo, len(users))
	for i, u := range users {
		out[i] = &apiv1.UserInfo{
			Id:        u.ID,
			Name:      u.Name,
			TeamName:  u.TeamName,
			CreatedAt: u.CreatedAt.Format(time.RFC3339),
		}
	}
	return connect.NewResponse(&apiv1.ListUsersResponse{Users: out}), nil
}

func (h *adminHandler) ListAuditEvents(ctx context.Context, req *connect.Request[apiv1.ListAuditEventsRequest]) (*connect.Response[apiv1.ListAuditEventsResponse], error) {
	events, err := h.svc.ListAuditEvents(ctx, service.AuditEventFilterInput{
		EventType:    req.Msg.EventType,
		SourceType:   req.Msg.SourceType,
		SourceID:     req.Msg.SourceId,
		TargetType:   req.Msg.TargetType,
		TargetID:     req.Msg.TargetId,
		Repo:         req.Msg.Repo,
		Search:       req.Msg.Search,
		SinceHours:   int(req.Msg.SinceHours),
		Limit:        int(req.Msg.Limit),
		Offset:       int(req.Msg.Offset),
		ExcludeTypes: req.Msg.ExcludeTypes,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.AuditEvent, len(events))
	for i, e := range events {
		out[i] = &apiv1.AuditEvent{
			Id:               e.ID,
			EventType:        e.EventType,
			CreatedAt:        e.CreatedAt.Format(time.RFC3339),
			SourceType:       e.SourceType,
			SourceId:         e.SourceID,
			TargetType:       e.TargetType,
			TargetId:         e.TargetID,
			Repo:             e.Repo,
			GithubEvent:      e.GitHubEvent,
			GithubAction:     e.GitHubAction,
			GithubDeliveryId: e.GitHubDeliveryID,
			ParentEventId:    e.ParentEventID,
			Detail:           e.Detail,
			TokenId:          e.TokenID,
			TokenName:        e.TokenName,
		}
	}
	return connect.NewResponse(&apiv1.ListAuditEventsResponse{Events: out}), nil
}

func (h *adminHandler) ListTaskArtifacts(ctx context.Context, req *connect.Request[apiv1.ListTaskArtifactsRequest]) (*connect.Response[apiv1.ListTaskArtifactsResponse], error) {
	artifacts, err := h.svc.ListTaskArtifacts(ctx, service.TaskArtifactFilterInput{
		TaskID:         req.Msg.TaskId,
		AgentSessionID: req.Msg.AgentSessionId,
		ArtifactType:   req.Msg.ArtifactType,
		Repo:           req.Msg.Repo,
		Search:         req.Msg.Search,
		Limit:          int(req.Msg.Limit),
		Offset:         int(req.Msg.Offset),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.TaskArtifact, len(artifacts))
	for i, a := range artifacts {
		out[i] = &apiv1.TaskArtifact{
			Id:              a.ID,
			TaskId:          a.TaskID,
			AgentSessionId:  a.AgentSessionID,
			UserPromptId:    a.UserPromptID,
			ArtifactType:    a.ArtifactType,
			Repo:            a.Repo,
			Number:          int32(a.Number),
			Url:             a.URL,
			Ref:             a.Ref,
			Sha:             a.SHA,
			CreatedAt:       a.CreatedAt.Format(time.RFC3339),
			DiscoveredAt:    a.DiscoveredAt.Format(time.RFC3339),
			DiscoverySource: a.DiscoverySource,
		}
	}
	return connect.NewResponse(&apiv1.ListTaskArtifactsResponse{Artifacts: out}), nil
}

func (h *adminHandler) ListRepos(ctx context.Context, _ *connect.Request[apiv1.ListReposRequest]) (*connect.Response[apiv1.ListReposResponse], error) {
	repos, err := h.svc.ListRepos(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.ListReposResponse{Repos: repos}), nil
}

func (h *adminHandler) CreateGitIdentity(ctx context.Context, req *connect.Request[apiv1.CreateGitIdentityRequest]) (*connect.Response[apiv1.CreateGitIdentityResponse], error) {
	record, err := h.svc.CreateGitIdentity(ctx, service.GitIdentityInput{TeamID: req.Msg.TeamId, TeamName: req.Msg.TeamName, Name: req.Msg.Name, GitAuthorName: req.Msg.GitAuthorName, GitAuthorEmail: req.Msg.GitAuthorEmail, CredentialType: req.Msg.CredentialType})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&apiv1.CreateGitIdentityResponse{Identity: protoGitIdentity(record)}), nil
}

func (h *adminHandler) ListGitIdentities(ctx context.Context, _ *connect.Request[apiv1.ListGitIdentitiesRequest]) (*connect.Response[apiv1.ListGitIdentitiesResponse], error) {
	records, err := h.svc.ListGitIdentities(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	identities := make([]*apiv1.GitIdentity, len(records))
	for i, record := range records {
		identities[i] = protoGitIdentity(record)
	}
	return connect.NewResponse(&apiv1.ListGitIdentitiesResponse{Identities: identities}), nil
}

func (h *adminHandler) UpdateGitIdentity(ctx context.Context, req *connect.Request[apiv1.UpdateGitIdentityRequest]) (*connect.Response[apiv1.UpdateGitIdentityResponse], error) {
	record, err := h.svc.UpdateGitIdentity(ctx, service.GitIdentityInput{TeamID: req.Msg.TeamId, TeamName: req.Msg.TeamName, Name: req.Msg.Name, GitAuthorName: req.Msg.GitAuthorName, GitAuthorEmail: req.Msg.GitAuthorEmail, CredentialType: req.Msg.CredentialType})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&apiv1.UpdateGitIdentityResponse{Identity: protoGitIdentity(record)}), nil
}

func (h *adminHandler) DeleteGitIdentity(ctx context.Context, req *connect.Request[apiv1.DeleteGitIdentityRequest]) (*connect.Response[apiv1.DeleteGitIdentityResponse], error) {
	if err := h.svc.DeleteGitIdentity(ctx, req.Msg.TeamId, req.Msg.TeamName, req.Msg.Name); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&apiv1.DeleteGitIdentityResponse{Deleted: true}), nil
}

func (h *adminHandler) SetGitIdentityDefault(ctx context.Context, req *connect.Request[apiv1.SetGitIdentityDefaultRequest]) (*connect.Response[apiv1.SetGitIdentityDefaultResponse], error) {
	record, err := h.svc.SetDefaultGitIdentity(ctx, req.Msg.TeamId, req.Msg.TeamName, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&apiv1.SetGitIdentityDefaultResponse{Identity: protoGitIdentity(record)}), nil
}

func (h *adminHandler) HandleListRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repos, err := h.svc.ListRepos(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "list repos failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if repos == nil {
		repos = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"repos": repos}); err != nil {
		slog.ErrorContext(r.Context(), "encode repos response", "err", err)
	}
}

// --- ArcaneServiceHandler ---

type arcaneHandler struct {
	svc *service.Service
}

func (h *arcaneHandler) GetScannerStatus(ctx context.Context, req *connect.Request[apiv1.ArcaneScannerStatusRequest]) (*connect.Response[apiv1.ArcaneScannerStatusResponse], error) {
	out, err := h.svc.ArcaneScannerStatus(ctx, req.Msg.EnvironmentId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.ArcaneScannerStatusResponse{
		Available: out.Available,
		Version:   out.Version,
	}), nil
}

func (h *arcaneHandler) GetEnvironmentSummary(ctx context.Context, req *connect.Request[apiv1.ArcaneEnvironmentSummaryRequest]) (*connect.Response[apiv1.ArcaneEnvironmentSummaryResponse], error) {
	out, err := h.svc.ArcaneEnvironmentSummary(ctx, req.Msg.EnvironmentId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.ArcaneEnvironmentSummaryResponse{
		TotalImages:   int32(out.TotalImages),
		ScannedImages: int32(out.ScannedImages),
		Summary:       protoSeverity(out.Summary),
	}), nil
}

func (h *arcaneHandler) ListImages(ctx context.Context, req *connect.Request[apiv1.ArcaneListImagesRequest]) (*connect.Response[apiv1.ArcaneListImagesResponse], error) {
	images, err := h.svc.ArcaneListImages(ctx, req.Msg.EnvironmentId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.ImageSummaryItem, len(images))
	for i, img := range images {
		out[i] = &apiv1.ImageSummaryItem{
			Id:       img.ID,
			RepoTags: img.RepoTags,
			Repo:     img.Repo,
			Tag:      img.Tag,
			InUse:    img.InUse,
		}
	}
	return connect.NewResponse(&apiv1.ArcaneListImagesResponse{Images: out}), nil
}

func (h *arcaneHandler) GetImageSummary(ctx context.Context, req *connect.Request[apiv1.ArcaneImageSummaryRequest]) (*connect.Response[apiv1.ArcaneImageSummaryResponse], error) {
	out, err := h.svc.ArcaneImageSummary(ctx, req.Msg.EnvironmentId, req.Msg.ImageId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.ArcaneImageSummaryResponse{
		ImageId:  out.ImageID,
		ScanTime: out.ScanTime,
		Status:   out.Status,
		Summary:  protoSeverity(out.Summary),
	}), nil
}

func (h *arcaneHandler) ListVulnerabilities(ctx context.Context, req *connect.Request[apiv1.ArcaneListVulnerabilitiesRequest]) (*connect.Response[apiv1.ArcaneListVulnerabilitiesResponse], error) {
	vulns, total, err := h.svc.ArcaneListVulnerabilities(ctx, req.Msg.EnvironmentId, req.Msg.ImageId, req.Msg.Severity, int(req.Msg.Page), int(req.Msg.Limit))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*apiv1.Vulnerability, len(vulns))
	for i, v := range vulns {
		out[i] = &apiv1.Vulnerability{
			VulnerabilityId:  v.VulnerabilityID,
			PkgName:          v.PkgName,
			InstalledVersion: v.InstalledVersion,
			FixedVersion:     v.FixedVersion,
			Severity:         v.Severity,
			Title:            v.Title,
			Description:      v.Description,
		}
	}
	return connect.NewResponse(&apiv1.ArcaneListVulnerabilitiesResponse{
		Vulnerabilities: out,
		TotalItems:      int32(total),
	}), nil
}

func protoSeverity(s service.SeveritySummary) *apiv1.SeveritySummary {
	return &apiv1.SeveritySummary{
		Critical: int32(s.Critical),
		High:     int32(s.High),
		Medium:   int32(s.Medium),
		Low:      int32(s.Low),
		Unknown:  int32(s.Unknown),
		Total:    int32(s.Total),
	}
}

// --- Helpers ---

func buildTriggerConfig(triggerType, repo, event string, matchLabels []string, sessionMode, pauseReason string, ttlHours int) string {
	cfg := map[string]any{}
	applyWebTriggerRuntimeConfig(cfg, sessionMode, pauseReason, ttlHours)
	switch triggerType {
	case store.TriggerTypePRReview:
		if repo == "" {
			return ""
		}
		cfg["repo"] = repo
		data, _ := json.Marshal(cfg)
		return string(data)
	case store.TriggerTypeIssue:
		if repo == "" {
			return ""
		}
		cfg["repo"] = repo
		if event != "" {
			cfg["event"] = event
		}
		if len(matchLabels) > 0 {
			cfg["match_labels"] = matchLabels
		}
		data, _ := json.Marshal(cfg)
		return string(data)
	}
	if len(cfg) > 0 {
		data, _ := json.Marshal(cfg)
		return string(data)
	}
	return ""
}

func applyWebTriggerRuntimeConfig(cfg map[string]any, sessionMode, pauseReason string, ttlHours int) {
	if sessionMode != "" {
		cfg["session_mode"] = sessionMode
	}
	if pauseReason != "" {
		cfg["pause_reason"] = pauseReason
	}
	if ttlHours > 0 {
		cfg["ttl_hours"] = ttlHours
	}
}

func triggerSkillsToStrings(skills json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(skills, &out)
	return out
}

// Ensure service has the trigger lookup method we need for UpdateTrigger.
// If not present, this is a compile-time check.
var _ repository.ChetterTrigger

// GetTriggerByName is a helper that delegates to the service's repo.
// We need to expose this from service if not already available.

// --- CatalogServiceHandler ---

type catalogHandler struct {
	svc *service.Service
}

func (h *catalogHandler) GetModelCatalog(ctx context.Context, _ *connect.Request[apiv1.GetModelCatalogRequest]) (*connect.Response[apiv1.GetModelCatalogResponse], error) {
	catalog, err := h.svc.GetModelCatalog(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	providers := make([]*apiv1.CatalogProvider, 0, len(catalog.Providers))
	for id, p := range catalog.Providers {
		models := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, m.ID)
		}
		providers = append(providers, &apiv1.CatalogProvider{
			Id:        id,
			Name:      p.Name,
			Kind:      p.Kind,
			BaseUrl:   p.BaseURL,
			ApiKeyEnv: p.APIKeyEnv,
			Models:    models,
		})
	}

	defaults := make([]*apiv1.CatalogHarnessDefault, 0, len(catalog.Defaults))
	for harness, d := range catalog.Defaults {
		defaults = append(defaults, &apiv1.CatalogHarnessDefault{
			Harness:  harness,
			Provider: d.Provider,
			Model:    d.Model,
		})
	}

	return connect.NewResponse(&apiv1.GetModelCatalogResponse{
		DefaultProvider: catalog.DefaultProvider,
		DefaultModel:    catalog.DefaultModel,
		Defaults:        defaults,
		Providers:       providers,
	}), nil
}
