package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SubmitTaskInput is the input for chetter_submit_task.
type SubmitTaskInput struct {
	Prompt     string            `json:"prompt" jsonschema:"Task prompt to run in the Chetter runner"`
	GitURL     string            `json:"git_url,omitempty" jsonschema:"Repository URL to clone before running the task"`
	GitRef     string            `json:"git_ref,omitempty" jsonschema:"Branch tag or commit to check out"`
	AgentImage string            `json:"agent_image,omitempty" jsonschema:"Runner harness image override"`
	Agent      string            `json:"agent,omitempty" jsonschema:"OpenCode agent to use for the task"`
	ProviderID string            `json:"provider_id,omitempty" jsonschema:"OpenCode provider id for model selection"`
	ModelID    string            `json:"model_id,omitempty" jsonschema:"OpenCode model id, optionally provider-qualified"`
	VariantID  string            `json:"variant_id,omitempty" jsonschema:"OpenCode model variant, such as high or minimal"`
	Skills     []string          `json:"skills,omitempty" jsonschema:"Skill names or hints for the runner"`
	Env        map[string]string `json:"env,omitempty" jsonschema:"Additional non-secret environment variables"`
	TimeoutSec int               `json:"timeout_sec,omitempty" jsonschema:"Task timeout in seconds"`
}

// SubmitTaskOutput is the output for chetter_submit_task.
type SubmitTaskOutput struct {
	Task TaskToolRecord `json:"task"`
}

// TaskStatusInput is the input for chetter_task_status.
type TaskStatusInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by chetter_submit_task"`
}

// TaskStatusOutput is the output for chetter_task_status.
type TaskStatusOutput struct {
	Task TaskToolRecord `json:"task"`
}

// ListTasksInput is the input for chetter_list_tasks.
type ListTasksInput struct {
	Status string `json:"status,omitempty" jsonschema:"Optional task status filter"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum tasks to return, capped at 100"`
}

// ListTasksOutput is the output for chetter_list_tasks.
type ListTasksOutput struct {
	Tasks []TaskToolRecord `json:"tasks"`
}

// TaskToolRecord is the stable MCP task response shape. Store-level task
// records may grow internal audit fields without breaking existing MCP clients.
type TaskToolRecord struct {
	ID         string            `json:"id"`
	TeamID     string            `json:"team_id,omitempty"`
	Status     string            `json:"status"`
	Prompt     string            `json:"prompt"`
	GitURL     string            `json:"git_url,omitempty"`
	GitRef     string            `json:"git_ref,omitempty"`
	AgentImage string            `json:"agent_image,omitempty"`
	Agent      string            `json:"agent,omitempty"`
	ProviderID string            `json:"provider_id,omitempty"`
	ModelID    string            `json:"model_id,omitempty"`
	VariantID  string            `json:"variant_id,omitempty"`
	Skills     []string          `json:"skills"`
	Env        map[string]string `json:"env"`
	TimeoutSec int               `json:"timeout_sec"`
	Summary    string            `json:"summary,omitempty"`
	Error      string            `json:"error,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	StartedAt  *time.Time        `json:"started_at,omitempty"`
	EndedAt    *time.Time        `json:"ended_at,omitempty"`
}

// ScheduleTaskInput is the input for chetter_schedule_task.
type ScheduleTaskInput struct {
	Name       string   `json:"name" jsonschema:"Unique schedule name"`
	CronExpr   string   `json:"cron_expr" jsonschema:"Five-field cron expression or descriptor like @hourly"`
	Prompt     string   `json:"prompt" jsonschema:"Task prompt to submit on each cron fire"`
	GitURL     string   `json:"git_url,omitempty" jsonschema:"Repository URL to clone before running each task"`
	GitRef     string   `json:"git_ref,omitempty" jsonschema:"Branch tag or commit to check out"`
	AgentImage string   `json:"agent_image,omitempty" jsonschema:"Runner harness image override"`
	Agent      string   `json:"agent,omitempty" jsonschema:"OpenCode agent to use for each task"`
	ProviderID string   `json:"provider_id,omitempty" jsonschema:"OpenCode provider id for model selection"`
	ModelID    string   `json:"model_id,omitempty" jsonschema:"OpenCode model id, optionally provider-qualified"`
	VariantID  string   `json:"variant_id,omitempty" jsonschema:"OpenCode model variant, such as high or minimal"`
	Skills     []string `json:"skills,omitempty" jsonschema:"Skill names or hints for the runner"`
	TimeoutSec int      `json:"timeout_sec,omitempty" jsonschema:"Task timeout in seconds"`
}

// ScheduleTaskOutput is the output for chetter_schedule_task.
type ScheduleTaskOutput struct {
	Schedule store.ScheduleRecord `json:"schedule"`
}

// RunScheduleInput is the input for chetter_run_schedule.
type RunScheduleInput struct {
	Name string `json:"name" jsonschema:"Name of the schedule to run immediately"`
}

// RunScheduleOutput is the output for chetter_run_schedule.
type RunScheduleOutput struct {
	Task TaskToolRecord `json:"task"`
}

// ListSchedulesInput is the input for chetter_list_schedules.
type ListSchedulesInput struct {
	EnabledOnly bool `json:"enabled_only,omitempty" jsonschema:"Only return enabled schedules"`
}

// ListSchedulesOutput is the output for chetter_list_schedules.
type ListSchedulesOutput struct {
	Schedules []store.ScheduleRecord `json:"schedules"`
}

// DeleteScheduleInput is the input for chetter_delete_schedule.
type DeleteScheduleInput struct {
	Name string `json:"name" jsonschema:"Name of the schedule to delete"`
}

// DeleteScheduleOutput is the output for chetter_delete_schedule.
type DeleteScheduleOutput struct {
	Deleted bool `json:"deleted"`
}

// UpdateScheduleInput is the input for chetter_update_schedule.
type UpdateScheduleInput struct {
	Name       string   `json:"name" jsonschema:"Name of the schedule to update"`
	CronExpr   string   `json:"cron_expr,omitempty" jsonschema:"Five-field cron expression or descriptor like @hourly"`
	Prompt     string   `json:"prompt,omitempty" jsonschema:"Task prompt to submit on each cron fire"`
	GitURL     string   `json:"git_url,omitempty" jsonschema:"Repository URL to clone before running each task"`
	GitRef     string   `json:"git_ref,omitempty" jsonschema:"Branch tag or commit to check out"`
	AgentImage string   `json:"agent_image,omitempty" jsonschema:"Runner harness image override"`
	Agent      string   `json:"agent,omitempty" jsonschema:"OpenCode agent to use for each task"`
	ProviderID string   `json:"provider_id,omitempty" jsonschema:"OpenCode provider id for model selection"`
	ModelID    string   `json:"model_id,omitempty" jsonschema:"OpenCode model id, optionally provider-qualified"`
	VariantID  string   `json:"variant_id,omitempty" jsonschema:"OpenCode model variant, such as high or minimal"`
	Skills     []string `json:"skills,omitempty" jsonschema:"Skill names or hints for the runner"`
	Enabled    *bool    `json:"enabled,omitempty" jsonschema:"Enable or disable the schedule"`
	TimeoutSec int      `json:"timeout_sec,omitempty" jsonschema:"Task timeout in seconds"`
}

// UpdateScheduleOutput is the output for chetter_update_schedule.
type UpdateScheduleOutput struct {
	Schedule store.ScheduleRecord `json:"schedule"`
}

// TaskEventsInput is the input for chetter_task_events.
type TaskEventsInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by chetter_submit_task"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum events to return, capped at 500"`
}

// TaskEventsOutput is the output for chetter_task_events.
type TaskEventsOutput struct {
	Events []TaskEventRecord `json:"events"`
}

// TaskEventRecord is a single persisted runner event.
type TaskEventRecord struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	Status    string    `json:"status"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskProgressInput is the input for chetter_task_progress.
type TaskProgressInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by chetter_submit_task"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum progress entries to return"`
}

// TaskProgressOutput is the output for chetter_task_progress.
type TaskProgressOutput struct {
	Entries []TaskProgressRecord `json:"entries"`
}

// TaskProgressRecord is a distilled status + summary entry.
type TaskProgressRecord struct {
	Time    time.Time `json:"time"`
	Status  string    `json:"status"`
	Summary string    `json:"summary,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// TaskLatestEventInput is the input for chetter_task_latest_event.
type TaskLatestEventInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by chetter_submit_task"`
}

// TaskLatestEventOutput is the output for chetter_task_latest_event.
type TaskLatestEventOutput struct {
	Event   TaskEventRecord `json:"event"`
	AgeSec  int             `json:"age_sec"`
	IsStale bool            `json:"is_stale"`
}

// RunnerHealthInput is the input for chetter_runner_health.
type RunnerHealthInput struct {
	IncludeTasks bool `json:"include_tasks,omitempty" jsonschema:"Include per-task details for currently running tasks"`
}

// RunnerHealthOutput is the output for chetter_runner_health.
type RunnerHealthOutput struct {
	Health store.RunnerFleetHealth `json:"health"`
}

// CancelTaskInput is the input for chetter_cancel_task.
type CancelTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier to cancel"`
	Reason string `json:"reason,omitempty" jsonschema:"Optional cancellation reason"`
}

// CancelTaskOutput is the output for chetter_cancel_task.
type CancelTaskOutput struct {
	Task TaskToolRecord `json:"task"`
}

// ClearQueueInput is the input for chetter_clear_queue.
type ClearQueueInput struct {
	Confirm bool `json:"confirm" jsonschema:"Set true to cancel pending DB-backed tasks"`
}

// ClearQueueOutput is the output for chetter_clear_queue.
type ClearQueueOutput struct {
	Cleared               bool `json:"cleared"`
	CancelledPendingTasks int  `json:"cancelled_pending_tasks"`
}

// CreateTokenInput is the input for chetter_create_token.
type CreateTokenInput struct {
	TeamName string `json:"team_name" jsonschema:"Name of the team (created if it does not exist)"`
	UserName string `json:"user_name" jsonschema:"Name of the user (created if it does not exist)"`
	TokenName string `json:"token_name" jsonschema:"A short name for the token (e.g. 'alice-cli')"`
}

// CreateTokenOutput is the output for chetter_create_token.
type CreateTokenOutput struct {
	Token    string `json:"token"`
	TeamID   string `json:"team_id"`
	TeamName string `json:"team_name"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
}

// ListTokensInput is the input for chetter_list_tokens.
type ListTokensInput struct{}

// TokenInfo is a single token entry in the list.
type TokenInfo struct {
	Name      string    `json:"name"`
	UserName  string    `json:"user_name"`
	TeamName  string    `json:"team_name"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTokensOutput is the output for chetter_list_tokens.
type ListTokensOutput struct {
	Tokens []TokenInfo `json:"tokens"`
}

// DeleteTokenInput is the input for chetter_delete_token.
type DeleteTokenInput struct {
	Name string `json:"name" jsonschema:"Name of the token to delete"`
}

// DeleteTokenOutput is the output for chetter_delete_token.
type DeleteTokenOutput struct {
	Deleted bool `json:"deleted"`
}

// RegisterTools registers chetter MCP tools.
func RegisterTools(server *mcp.Server, svc *Service) {
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_submit_task", Description: "Submit a development task to the Chetter runner fleet with optional OpenCode agent, provider, model ID, and variant selection."}, svc.submitTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_status", Description: "Get current status and result details for a chetter task."}, svc.taskStatusTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_tasks", Description: "List recent chetter tasks, optionally filtered by status."}, svc.listTasksTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_schedule_task", Description: "Create and activate a cron schedule that submits chetter tasks."}, svc.scheduleTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_run_schedule", Description: "Run a chetter cron task schedule immediately by name."}, svc.runScheduleTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_schedules", Description: "List chetter cron task schedules."}, svc.listSchedulesTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_delete_schedule", Description: "Delete a chetter cron task schedule by name."}, svc.deleteScheduleTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_update_schedule", Description: "Update a chetter cron task schedule by name. Only provided fields are changed."}, svc.updateScheduleTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_events", Description: "Get the full event history for a chetter task."}, svc.taskEventsTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_progress", Description: "Get a distilled progress timeline for a chetter task."}, svc.taskProgressTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_latest_event", Description: "Get the most recent event for a chetter task."}, svc.taskLatestEventTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_runner_health", Description: "Check runner fleet health including running/stale task counts, active runner image versions, and per-task heartbeat age."}, svc.runnerHealthTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_cancel_task", Description: "Cancel a single chetter task by ID. Only works for pending or running tasks."}, svc.cancelTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_clear_queue", Description: "Clear queued chetter tasks by cancelling pending DB-backed tasks. Requires confirm=true."}, svc.clearQueueTool)
	if svc != nil && svc.arcane != nil && svc.arcane.IsConfigured() {
		mcp.AddTool(server, &mcp.Tool{Name: "chetter_arcane_scanner_status", Description: "Check if the Arcane Trivy vulnerability scanner is available and get its version."}, svc.arcaneScannerStatusTool)
		mcp.AddTool(server, &mcp.Tool{Name: "chetter_arcane_environment_summary", Description: "Get aggregated vulnerability counts across all images in the Arcane environment."}, svc.arcaneEnvironmentSummaryTool)
		mcp.AddTool(server, &mcp.Tool{Name: "chetter_arcane_list_images", Description: "List all Docker images in the Arcane environment with their IDs and tags."}, svc.arcaneListImagesTool)
		mcp.AddTool(server, &mcp.Tool{Name: "chetter_arcane_image_summary", Description: "Get vulnerability summary for a specific Docker image by its ID."}, svc.arcaneImageSummaryTool)
		mcp.AddTool(server, &mcp.Tool{Name: "chetter_arcane_list_vulnerabilities", Description: "List detailed vulnerabilities for an image with optional severity filtering and pagination."}, svc.arcaneListVulnerabilitiesTool)
	}
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_create_token", Description: "Create a new API token for a team and user. Admin only."}, svc.createTokenTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_tokens", Description: "List all API tokens with user and team info. Admin only."}, svc.listTokensTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_delete_token", Description: "Delete an API token by name. Admin only."}, svc.deleteTokenTool)
}

func (s *Service) submitTaskTool(ctx context.Context, _ *mcp.CallToolRequest, in SubmitTaskInput) (*mcp.CallToolResult, SubmitTaskOutput, error) {
	task, err := s.SubmitTask(ctx, SubmitTaskRequest(in))
	if err != nil {
		return nil, SubmitTaskOutput{}, fmt.Errorf("submit task: %w", err)
	}
	return nil, SubmitTaskOutput{Task: taskToolRecord(task)}, nil
}

func (s *Service) taskStatusTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskStatusInput) (*mcp.CallToolResult, TaskStatusOutput, error) {
	if in.TaskID == "" {
		return nil, TaskStatusOutput{}, fmt.Errorf("task_id is required")
	}
	task, err := s.repo.GetTaskByID(ctx, in.TaskID)
	if err != nil {
		return nil, TaskStatusOutput{}, fmt.Errorf("get task status: %w", err)
	}
	return nil, TaskStatusOutput{Task: repoTaskToToolRecord(task)}, nil
}

func (s *Service) listTasksTool(ctx context.Context, _ *mcp.CallToolRequest, in ListTasksInput) (*mcp.CallToolResult, ListTasksOutput, error) {
	scope, scoped := auth.GetScope(ctx)
	limit := clampListLimit(in.Limit)
	var tasks []repository.ChetterTask
	var err error
	if scoped && !scope.Admin && scope.TeamID != "" {
		tasks, err = s.repo.ListTasksByStatusAndTeam(ctx, repository.ListTasksByStatusAndTeamParams{
			TeamID:       sql.NullString{String: scope.TeamID, Valid: true},
			StatusFilter: in.Status,
			Limit:        limit,
		})
	} else {
		tasks, err = s.repo.ListTasksByStatus(ctx, repository.ListTasksByStatusParams{
			StatusFilter: in.Status,
			Limit:        limit,
		})
	}
	if err != nil {
		return nil, ListTasksOutput{}, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]TaskToolRecord, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, repoTaskToToolRecord(task))
	}
	return nil, ListTasksOutput{Tasks: out}, nil
}

func clampListLimit(limit int) int32 {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return int32(limit)
}

func taskToolRecord(task store.TaskRecord) TaskToolRecord {
	return TaskToolRecord{
		ID:         task.ID,
		TeamID:     task.TeamID,
		Status:     task.Status,
		Prompt:     task.Prompt,
		GitURL:     task.GitURL,
		GitRef:     task.GitRef,
		AgentImage: task.AgentImage,
		Agent:      task.Agent,
		ProviderID: task.ProviderID,
		ModelID:    task.ModelID,
		VariantID:  task.VariantID,
		Skills:     task.Skills,
		Env:        task.Env,
		TimeoutSec: task.TimeoutSec,
		Summary:    task.Summary,
		Error:      task.Error,
		CreatedAt:  task.CreatedAt,
		UpdatedAt:  task.UpdatedAt,
		StartedAt:  task.StartedAt,
		EndedAt:    task.EndedAt,
	}
}

func repoTaskToToolRecord(task repository.ChetterTask) TaskToolRecord {
	var skills []string
	_ = json.Unmarshal(task.Skills, &skills)
	env := map[string]string{}
	_ = json.Unmarshal(task.Env, &env)
	return TaskToolRecord{
		ID:         task.ID,
		TeamID:     task.TeamID.String,
		Status:     task.Status,
		Prompt:     task.Prompt,
		GitURL:     task.GitUrl.String,
		GitRef:     task.GitRef.String,
		AgentImage: task.AgentImage.String,
		Agent:      task.Agent.String,
		ProviderID: task.ProviderID.String,
		ModelID:    task.ModelID.String,
		VariantID:  task.VariantID.String,
		Skills:     skills,
		Env:        env,
		TimeoutSec: int(task.TimeoutSec),
		Summary:    task.Summary.String,
		Error:      task.Error.String,
		CreatedAt:  task.CreatedAt,
		UpdatedAt:  task.UpdatedAt,
		StartedAt:  nullTimePtr(task.StartedAt),
		EndedAt:    nullTimePtr(task.EndedAt),
	}
}

func nullTimePtr(nt sql.NullTime) *time.Time {
	if nt.Valid {
		return &nt.Time
	}
	return nil
}

func (s *Service) scheduleTaskTool(ctx context.Context, _ *mcp.CallToolRequest, in ScheduleTaskInput) (*mcp.CallToolResult, ScheduleTaskOutput, error) {
	schedule, err := s.CreateSchedule(ctx, store.ScheduleInput{
		Name:       in.Name,
		CronExpr:   in.CronExpr,
		Prompt:     in.Prompt,
		GitURL:     in.GitURL,
		GitRef:     in.GitRef,
		AgentImage: in.AgentImage,
		Agent:      in.Agent,
		ProviderID: in.ProviderID,
		ModelID:    in.ModelID,
		VariantID:  in.VariantID,
		Skills:     in.Skills,
		TimeoutSec: in.TimeoutSec,
	})
	if err != nil {
		return nil, ScheduleTaskOutput{}, fmt.Errorf("create schedule: %w", err)
	}
	return nil, ScheduleTaskOutput{Schedule: schedule}, nil
}

func (s *Service) runScheduleTool(ctx context.Context, _ *mcp.CallToolRequest, in RunScheduleInput) (*mcp.CallToolResult, RunScheduleOutput, error) {
	task, err := s.RunScheduleNow(ctx, in.Name)
	if err != nil {
		return nil, RunScheduleOutput{}, fmt.Errorf("run schedule: %w", err)
	}
	return nil, RunScheduleOutput{Task: taskToolRecord(task)}, nil
}

func (s *Service) listSchedulesTool(ctx context.Context, _ *mcp.CallToolRequest, in ListSchedulesInput) (*mcp.CallToolResult, ListSchedulesOutput, error) {
	scope, scoped := auth.GetScope(ctx)
	var repoRecords []repository.ChetterSchedule
	var err error
	if scoped && !scope.Admin && scope.TeamID != "" {
		teamID := sql.NullString{String: scope.TeamID, Valid: true}
		if in.EnabledOnly {
			repoRecords, err = s.repo.ListEnabledSchedulesByTeam(ctx, teamID)
		} else {
			repoRecords, err = s.repo.ListSchedulesByTeam(ctx, teamID)
		}
	} else {
		if in.EnabledOnly {
			repoRecords, err = s.repo.ListEnabledSchedules(ctx)
		} else {
			repoRecords, err = s.repo.ListSchedules(ctx)
		}
	}
	if err != nil {
		return nil, ListSchedulesOutput{}, fmt.Errorf("list schedules: %w", err)
	}
	schedules := make([]store.ScheduleRecord, len(repoRecords))
	for i, r := range repoRecords {
		schedules[i] = scheduleToStoreRecord(r)
	}
	return nil, ListSchedulesOutput{Schedules: schedules}, nil
}

func scheduleToStoreRecord(s repository.ChetterSchedule) store.ScheduleRecord {
	var skills []string
	_ = json.Unmarshal(s.Skills, &skills)
	return store.ScheduleRecord{
		ID:         s.ID,
		TeamID:     s.TeamID.String,
		Name:       s.Name,
		CronExpr:   s.CronExpr,
		Prompt:     s.Prompt,
		GitURL:     s.GitUrl.String,
		GitRef:     s.GitRef.String,
		AgentImage: s.AgentImage.String,
		Agent:      s.Agent.String,
		ProviderID: s.ProviderID.String,
		ModelID:    s.ModelID.String,
		VariantID:  s.VariantID.String,
		Skills:     skills,
		TimeoutSec: int(s.TimeoutSec),
		Enabled:    s.Enabled,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
		LastRunAt:  nullTimePtr(s.LastRunAt),
		NextRunAt:  nullTimePtr(s.NextRunAt),
	}
}

func (s *Service) deleteScheduleTool(ctx context.Context, _ *mcp.CallToolRequest, in DeleteScheduleInput) (*mcp.CallToolResult, DeleteScheduleOutput, error) {
	if in.Name == "" {
		return nil, DeleteScheduleOutput{}, fmt.Errorf("name is required")
	}
	if err := s.DeleteSchedule(ctx, in.Name); err != nil {
		return nil, DeleteScheduleOutput{}, fmt.Errorf("delete schedule: %w", err)
	}
	return nil, DeleteScheduleOutput{Deleted: true}, nil
}

func (s *Service) updateScheduleTool(ctx context.Context, _ *mcp.CallToolRequest, in UpdateScheduleInput) (*mcp.CallToolResult, UpdateScheduleOutput, error) {
	if in.Name == "" {
		return nil, UpdateScheduleOutput{}, fmt.Errorf("name is required")
	}
	existing, err := s.repo.GetScheduleByName(ctx, in.Name)
	if err != nil {
		return nil, UpdateScheduleOutput{}, fmt.Errorf("get schedule %q: %w", in.Name, err)
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	merged := store.ScheduleInput{
		Name:       in.Name,
		CronExpr:   store.NonZero(in.CronExpr, existing.CronExpr),
		Prompt:     store.NonZero(in.Prompt, existing.Prompt),
		GitURL:     store.NonZero(in.GitURL, existing.GitUrl.String),
		GitRef:     store.NonZero(in.GitRef, existing.GitRef.String),
		AgentImage: store.NonZero(in.AgentImage, existing.AgentImage.String),
		Agent:      store.NonZero(in.Agent, existing.Agent.String),
		ProviderID: store.NonZero(in.ProviderID, existing.ProviderID.String),
		ModelID:    store.NonZero(in.ModelID, existing.ModelID.String),
		VariantID:  store.NonZero(in.VariantID, existing.VariantID.String),
		Skills:     store.NonNilSlice(in.Skills, scheduleSkillsToStrings(existing.Skills)),
		TimeoutSec: store.NonZeroInt(in.TimeoutSec, int(existing.TimeoutSec)),
	}
	schedule, err := s.UpdateSchedule(ctx, in.Name, merged, enabled)
	if err != nil {
		return nil, UpdateScheduleOutput{}, fmt.Errorf("update schedule: %w", err)
	}
	return nil, UpdateScheduleOutput{Schedule: schedule}, nil
}

func scheduleSkillsToStrings(skills json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(skills, &out)
	return out
}

func (s *Service) taskEventsTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskEventsInput) (*mcp.CallToolResult, TaskEventsOutput, error) {
	if in.TaskID == "" {
		return nil, TaskEventsOutput{}, fmt.Errorf("task_id is required")
	}
	events, err := s.repo.ListTaskEvents(ctx, repository.ListTaskEventsParams{
		TaskID: in.TaskID,
		Limit:  clampEventLimit(in.Limit),
	})
	if err != nil {
		return nil, TaskEventsOutput{}, fmt.Errorf("get events: %w", err)
	}
	out := make([]TaskEventRecord, len(events))
	for i, ev := range events {
		out[i] = TaskEventRecord{
			ID:        ev.ID,
			Subject:   ev.Subject,
			Status:    ev.Status,
			Payload:   string(ev.Payload),
			CreatedAt: ev.CreatedAt,
		}
	}
	return nil, TaskEventsOutput{Events: out}, nil
}

func clampEventLimit(limit int) int32 {
	if limit <= 0 || limit > 500 {
		return 50
	}
	return int32(limit)
}

func (s *Service) taskProgressTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskProgressInput) (*mcp.CallToolResult, TaskProgressOutput, error) {
	if in.TaskID == "" {
		return nil, TaskProgressOutput{}, fmt.Errorf("task_id is required")
	}
	events, err := s.repo.ListTaskEvents(ctx, repository.ListTaskEventsParams{
		TaskID: in.TaskID,
		Limit:  clampEventLimit(in.Limit),
	})
	if err != nil {
		return nil, TaskProgressOutput{}, fmt.Errorf("get events: %w", err)
	}
	var out []TaskProgressRecord
	var lastStatus string
	for _, ev := range events {
		var resp store.TaskResponse
		_ = json.Unmarshal(ev.Payload, &resp)
		entry := TaskProgressRecord{
			Time:    ev.CreatedAt,
			Status:  ev.Status,
			Summary: resp.Summary,
			Error:   resp.Error,
		}
		if ev.Status != lastStatus || entry.Summary != "" || entry.Error != "" {
			out = append(out, entry)
			lastStatus = ev.Status
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return nil, TaskProgressOutput{Entries: out}, nil
}

func (s *Service) taskLatestEventTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskLatestEventInput) (*mcp.CallToolResult, TaskLatestEventOutput, error) {
	if in.TaskID == "" {
		return nil, TaskLatestEventOutput{}, fmt.Errorf("task_id is required")
	}
	ev, err := s.repo.GetLatestTaskEvent(ctx, in.TaskID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, TaskLatestEventOutput{}, fmt.Errorf("no events found for task %s", in.TaskID)
		}
		return nil, TaskLatestEventOutput{}, fmt.Errorf("get latest event: %w", err)
	}
	ageSec := int(time.Since(ev.CreatedAt).Seconds())
	return nil, TaskLatestEventOutput{
		Event: TaskEventRecord{
			ID:        ev.ID,
			Subject:   ev.Subject,
			Status:    ev.Status,
			Payload:   string(ev.Payload),
			CreatedAt: ev.CreatedAt,
		},
		AgeSec:  ageSec,
		IsStale: ageSec > 120,
	}, nil
}

func (s *Service) runnerHealthTool(ctx context.Context, _ *mcp.CallToolRequest, in RunnerHealthInput) (*mcp.CallToolResult, RunnerHealthOutput, error) {
	health, err := s.store.GetRunnerFleetHealth(ctx, reaperHealthMaxEventSec, runnerPresenceMaxSec)
	if err != nil {
		return nil, RunnerHealthOutput{}, fmt.Errorf("get runner fleet health: %w", err)
	}
	if !in.IncludeTasks {
		health.RunningTaskInfos = nil
	}
	return nil, RunnerHealthOutput{Health: health}, nil
}

func (s *Service) cancelTaskTool(ctx context.Context, _ *mcp.CallToolRequest, in CancelTaskInput) (*mcp.CallToolResult, CancelTaskOutput, error) {
	if in.TaskID == "" {
		return nil, CancelTaskOutput{}, fmt.Errorf("task_id is required")
	}
	if in.Reason == "" {
		in.Reason = "cancelled by operator"
	}
	now := time.Now().UTC()
	rows, err := s.repo.CancelTask(ctx, repository.CancelTaskParams{
		Error:     sql.NullString{String: in.Reason, Valid: true},
		EndedAt:   sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
		ID:        in.TaskID,
	})
	if err != nil {
		return nil, CancelTaskOutput{}, fmt.Errorf("cancel task: %w", err)
	}
	if rows == 0 {
		return nil, CancelTaskOutput{}, fmt.Errorf("task %s is not pending or running", in.TaskID)
	}
	task, err := s.repo.GetTaskByID(ctx, in.TaskID)
	if err != nil {
		return nil, CancelTaskOutput{}, fmt.Errorf("get task after cancel: %w", err)
	}
	return nil, CancelTaskOutput{Task: repoTaskToToolRecord(task)}, nil
}

func (s *Service) clearQueueTool(ctx context.Context, _ *mcp.CallToolRequest, in ClearQueueInput) (*mcp.CallToolResult, ClearQueueOutput, error) {
	if !in.Confirm {
		return nil, ClearQueueOutput{}, fmt.Errorf("confirm must be true to clear the queue")
	}
	now := time.Now().UTC()
	cancelled, err := s.repo.ClearPendingTasks(ctx, repository.ClearPendingTasksParams{
		Error:     sql.NullString{String: "cancelled by chetter_clear_queue", Valid: true},
		EndedAt:   sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
	})
	if err != nil {
		return nil, ClearQueueOutput{}, fmt.Errorf("cancel pending tasks: %w", err)
	}
	return nil, ClearQueueOutput{
		Cleared:               true,
		CancelledPendingTasks: int(cancelled),
	}, nil
}

// --- Token Management Tools ---

func (s *Service) createTokenTool(ctx context.Context, _ *mcp.CallToolRequest, in CreateTokenInput) (*mcp.CallToolResult, CreateTokenOutput, error) {
	if !isAdmin(ctx) {
		return nil, CreateTokenOutput{}, fmt.Errorf("admin access required")
	}
	if in.TeamName == "" {
		return nil, CreateTokenOutput{}, fmt.Errorf("team_name is required")
	}
	if in.UserName == "" {
		return nil, CreateTokenOutput{}, fmt.Errorf("user_name is required")
	}
	if in.TokenName == "" {
		return nil, CreateTokenOutput{}, fmt.Errorf("token_name is required")
	}
	now := time.Now().UTC()

	team, err := s.repo.GetTeamByName(ctx, in.TeamName)
	if err != nil {
		if err == sql.ErrNoRows {
			teamID, err := randomID("team")
			if err != nil {
				return nil, CreateTokenOutput{}, fmt.Errorf("generate team id: %w", err)
			}
			if err := s.repo.CreateTeam(ctx, repository.CreateTeamParams{
				ID:        teamID,
				Name:      in.TeamName,
				CreatedAt: now,
				UpdatedAt: now,
			}); err != nil {
				return nil, CreateTokenOutput{}, fmt.Errorf("create team: %w", err)
			}
			team.ID = teamID
			team.Name = in.TeamName
		} else {
			return nil, CreateTokenOutput{}, fmt.Errorf("look up team: %w", err)
		}
	}

	userID, err := randomID("user")
	if err != nil {
		return nil, CreateTokenOutput{}, fmt.Errorf("generate user id: %w", err)
	}
	if err := s.repo.CreateUser(ctx, repository.CreateUserParams{
		ID:        userID,
		Name:      in.UserName,
		TeamID:    team.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return nil, CreateTokenOutput{}, fmt.Errorf("create user: %w", err)
	}

	rawToken, err := randomID("chtr")
	if err != nil {
		return nil, CreateTokenOutput{}, fmt.Errorf("generate token: %w", err)
	}
	hash := sha256.Sum256([]byte(rawToken))
	tokenID, err := randomID("tok")
	if err != nil {
		return nil, CreateTokenOutput{}, fmt.Errorf("generate token id: %w", err)
	}
	if err := s.repo.CreateToken(ctx, repository.CreateTokenParams{
		ID:        tokenID,
		Name:      in.TokenName,
		TokenHash: hex.EncodeToString(hash[:]),
		UserID:    userID,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return nil, CreateTokenOutput{}, fmt.Errorf("create token: %w", err)
	}

	return nil, CreateTokenOutput{
		Token:    rawToken,
		TeamID:   team.ID,
		TeamName: team.Name,
		UserID:   userID,
		UserName: in.UserName,
	}, nil
}

func (s *Service) listTokensTool(ctx context.Context, _ *mcp.CallToolRequest, _ ListTokensInput) (*mcp.CallToolResult, ListTokensOutput, error) {
	if !isAdmin(ctx) {
		return nil, ListTokensOutput{}, fmt.Errorf("admin access required")
	}
	rows, err := s.repo.ListTokens(ctx)
	if err != nil {
		return nil, ListTokensOutput{}, fmt.Errorf("list tokens: %w", err)
	}
	out := make([]TokenInfo, len(rows))
	for i, r := range rows {
		out[i] = TokenInfo{
			Name:      r.Name,
			UserName:  r.UserName,
			TeamName:  r.TeamName,
			CreatedAt: r.CreatedAt,
		}
	}
	return nil, ListTokensOutput{Tokens: out}, nil
}

func (s *Service) deleteTokenTool(ctx context.Context, _ *mcp.CallToolRequest, in DeleteTokenInput) (*mcp.CallToolResult, DeleteTokenOutput, error) {
	if !isAdmin(ctx) {
		return nil, DeleteTokenOutput{}, fmt.Errorf("admin access required")
	}
	if in.Name == "" {
		return nil, DeleteTokenOutput{}, fmt.Errorf("name is required")
	}
	if err := s.repo.DeleteToken(ctx, in.Name); err != nil {
		return nil, DeleteTokenOutput{}, fmt.Errorf("delete token: %w", err)
	}
	return nil, DeleteTokenOutput{Deleted: true}, nil
}

func isAdmin(ctx context.Context) bool {
	scope, ok := auth.GetScope(ctx)
	return ok && scope.Admin
}

// --- Arcane Vulnerability Scan Tools ---

type ArcaneScannerStatusInput struct {
	EnvironmentID string `json:"environment_id,omitempty" jsonschema:"Arcane environment ID (default: 0)"`
}

type ArcaneScannerStatusOutput struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
}

type ArcaneEnvironmentSummaryInput struct {
	EnvironmentID string `json:"environment_id,omitempty" jsonschema:"Arcane environment ID (default: 0)"`
}

type ArcaneEnvironmentSummaryOutput struct {
	TotalImages   int             `json:"total_images"`
	ScannedImages int             `json:"scanned_images"`
	Summary       SeveritySummary `json:"summary"`
}

type ArcaneImageSummaryInput struct {
	EnvironmentID string `json:"environment_id,omitempty" jsonschema:"Arcane environment ID (default: 0)"`
	ImageID       string `json:"image_id" jsonschema:"Docker image ID (sha256:...)"`
}

type ArcaneImageSummaryOutput struct {
	ImageID  string          `json:"image_id"`
	ScanTime string          `json:"scan_time"`
	Status   string          `json:"status"`
	Summary  SeveritySummary `json:"summary"`
}

type ArcaneListVulnerabilitiesInput struct {
	EnvironmentID string `json:"environment_id,omitempty" jsonschema:"Arcane environment ID (default: 0)"`
	ImageID       string `json:"image_id" jsonschema:"Docker image ID (sha256:...)"`
	Severity      string `json:"severity,omitempty" jsonschema:"Filter by severity: CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN"`
	Page          int    `json:"page,omitempty" jsonschema:"Page number (default: 1)"`
	Limit         int    `json:"limit,omitempty" jsonschema:"Items per page (default: 20)"`
}

type ArcaneListVulnerabilitiesOutput struct {
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
	TotalItems      int             `json:"total_items"`
}

type ArcaneListImagesInput struct {
	EnvironmentID string `json:"environment_id,omitempty" jsonschema:"Arcane environment ID (default: 0)"`
}

type ArcaneListImagesOutput struct {
	Images []ImageSummaryItem `json:"images"`
}

func envIDOrDefault(id string) string {
	if id == "" {
		return "0"
	}
	return id
}

func (s *Service) arcaneScannerStatusTool(ctx context.Context, _ *mcp.CallToolRequest, in ArcaneScannerStatusInput) (*mcp.CallToolResult, ArcaneScannerStatusOutput, error) {
	if s.arcane == nil {
		return nil, ArcaneScannerStatusOutput{}, fmt.Errorf("arcane client not configured")
	}
	status, err := s.arcane.GetScannerStatus(ctx, envIDOrDefault(in.EnvironmentID))
	if err != nil {
		return nil, ArcaneScannerStatusOutput{}, fmt.Errorf("get scanner status: %w", err)
	}
	return nil, ArcaneScannerStatusOutput{Available: status.Available, Version: status.Version}, nil
}

func (s *Service) arcaneEnvironmentSummaryTool(ctx context.Context, _ *mcp.CallToolRequest, in ArcaneEnvironmentSummaryInput) (*mcp.CallToolResult, ArcaneEnvironmentSummaryOutput, error) {
	if s.arcane == nil {
		return nil, ArcaneEnvironmentSummaryOutput{}, fmt.Errorf("arcane client not configured")
	}
	summary, err := s.arcane.GetEnvironmentSummary(ctx, envIDOrDefault(in.EnvironmentID))
	if err != nil {
		return nil, ArcaneEnvironmentSummaryOutput{}, fmt.Errorf("get environment summary: %w", err)
	}
	return nil, ArcaneEnvironmentSummaryOutput{
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

func (s *Service) arcaneListImagesTool(ctx context.Context, _ *mcp.CallToolRequest, in ArcaneListImagesInput) (*mcp.CallToolResult, ArcaneListImagesOutput, error) {
	if s.arcane == nil {
		return nil, ArcaneListImagesOutput{}, fmt.Errorf("arcane client not configured")
	}
	images, err := s.arcane.ListEnvironmentImages(ctx, envIDOrDefault(in.EnvironmentID))
	if err != nil {
		return nil, ArcaneListImagesOutput{}, fmt.Errorf("list images: %w", err)
	}
	return nil, ArcaneListImagesOutput{Images: images}, nil
}

func (s *Service) arcaneImageSummaryTool(ctx context.Context, _ *mcp.CallToolRequest, in ArcaneImageSummaryInput) (*mcp.CallToolResult, ArcaneImageSummaryOutput, error) {
	if s.arcane == nil {
		return nil, ArcaneImageSummaryOutput{}, fmt.Errorf("arcane client not configured")
	}
	if in.ImageID == "" {
		return nil, ArcaneImageSummaryOutput{}, fmt.Errorf("image_id is required")
	}
	summary, err := s.arcane.GetImageScanSummary(ctx, envIDOrDefault(in.EnvironmentID), in.ImageID)
	if err != nil {
		return nil, ArcaneImageSummaryOutput{}, fmt.Errorf("get image summary: %w", err)
	}
	return nil, ArcaneImageSummaryOutput{
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

func (s *Service) arcaneListVulnerabilitiesTool(ctx context.Context, _ *mcp.CallToolRequest, in ArcaneListVulnerabilitiesInput) (*mcp.CallToolResult, ArcaneListVulnerabilitiesOutput, error) {
	if s.arcane == nil {
		return nil, ArcaneListVulnerabilitiesOutput{}, fmt.Errorf("arcane client not configured")
	}
	if in.ImageID == "" {
		return nil, ArcaneListVulnerabilitiesOutput{}, fmt.Errorf("image_id is required")
	}
	page := in.Page
	if page == 0 {
		page = 1
	}
	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	items, total, err := s.arcane.ListVulnerabilities(ctx, envIDOrDefault(in.EnvironmentID), in.ImageID, in.Severity, page, limit)
	if err != nil {
		return nil, ArcaneListVulnerabilitiesOutput{}, fmt.Errorf("list vulnerabilities: %w", err)
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
	return nil, ArcaneListVulnerabilitiesOutput{Vulnerabilities: out, TotalItems: total}, nil
}
