package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/flatout-works/devfleet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SubmitTaskInput is the input for devfleet_submit_task.
type SubmitTaskInput struct {
	Prompt     string            `json:"prompt" jsonschema:"Task prompt to run in the dev runner"`
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

// SubmitTaskOutput is the output for devfleet_submit_task.
type SubmitTaskOutput struct {
	Task TaskToolRecord `json:"task"`
}

// TaskStatusInput is the input for devfleet_task_status.
type TaskStatusInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by devfleet_submit_task"`
}

// TaskStatusOutput is the output for devfleet_task_status.
type TaskStatusOutput struct {
	Task TaskToolRecord `json:"task"`
}

// ListTasksInput is the input for devfleet_list_tasks.
type ListTasksInput struct {
	Status string `json:"status,omitempty" jsonschema:"Optional task status filter"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum tasks to return, capped at 100"`
}

// ListTasksOutput is the output for devfleet_list_tasks.
type ListTasksOutput struct {
	Tasks []TaskToolRecord `json:"tasks"`
}

// TaskToolRecord is the stable MCP task response shape. Store-level task
// records may grow internal audit fields without breaking existing MCP clients.
type TaskToolRecord struct {
	ID         string            `json:"id"`
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

// ScheduleTaskInput is the input for devfleet_schedule_task.
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

// ScheduleTaskOutput is the output for devfleet_schedule_task.
type ScheduleTaskOutput struct {
	Schedule store.ScheduleRecord `json:"schedule"`
}

// RunScheduleInput is the input for devfleet_run_schedule.
type RunScheduleInput struct {
	Name string `json:"name" jsonschema:"Name of the schedule to run immediately"`
}

// RunScheduleOutput is the output for devfleet_run_schedule.
type RunScheduleOutput struct {
	Task TaskToolRecord `json:"task"`
}

// ListSchedulesInput is the input for devfleet_list_schedules.
type ListSchedulesInput struct {
	EnabledOnly bool `json:"enabled_only,omitempty" jsonschema:"Only return enabled schedules"`
}

// ListSchedulesOutput is the output for devfleet_list_schedules.
type ListSchedulesOutput struct {
	Schedules []store.ScheduleRecord `json:"schedules"`
}

// DeleteScheduleInput is the input for devfleet_delete_schedule.
type DeleteScheduleInput struct {
	Name string `json:"name" jsonschema:"Name of the schedule to delete"`
}

// DeleteScheduleOutput is the output for devfleet_delete_schedule.
type DeleteScheduleOutput struct {
	Deleted bool `json:"deleted"`
}

// UpdateScheduleInput is the input for devfleet_update_schedule.
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

// UpdateScheduleOutput is the output for devfleet_update_schedule.
type UpdateScheduleOutput struct {
	Schedule store.ScheduleRecord `json:"schedule"`
}

// SyncSchedulesInput is the input for devfleet_sync_schedules.
type SyncSchedulesInput struct {
	Directory string `json:"directory" jsonschema:"Path to a directory containing schedule YAML files"`
	Prune     bool   `json:"prune,omitempty" jsonschema:"Delete DB schedules not present in the YAML files"`
}

// SyncSchedulesOutput is the output for devfleet_sync_schedules.
type SyncSchedulesOutput struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
}

// TaskEventsInput is the input for devfleet_task_events.
type TaskEventsInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by devfleet_submit_task"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum events to return, capped at 500"`
}

// TaskEventsOutput is the output for devfleet_task_events.
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

// TaskProgressInput is the input for devfleet_task_progress.
type TaskProgressInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by devfleet_submit_task"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum progress entries to return"`
}

// TaskProgressOutput is the output for devfleet_task_progress.
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

// TaskLatestEventInput is the input for devfleet_task_latest_event.
type TaskLatestEventInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by devfleet_submit_task"`
}

// TaskLatestEventOutput is the output for devfleet_task_latest_event.
type TaskLatestEventOutput struct {
	Event   TaskEventRecord `json:"event"`
	AgeSec  int             `json:"age_sec"`
	IsStale bool            `json:"is_stale"`
}

// RunnerHealthInput is the input for devfleet_runner_health.
type RunnerHealthInput struct {
	IncludeTasks bool `json:"include_tasks,omitempty" jsonschema:"Include per-task details for currently running tasks"`
}

// RunnerHealthOutput is the output for devfleet_runner_health.
type RunnerHealthOutput struct {
	Health store.RunnerFleetHealth `json:"health"`
}

// CancelTaskInput is the input for devfleet_cancel_task.
type CancelTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier to cancel"`
	Reason string `json:"reason,omitempty" jsonschema:"Optional cancellation reason"`
}

// CancelTaskOutput is the output for devfleet_cancel_task.
type CancelTaskOutput struct {
	Task TaskToolRecord `json:"task"`
}

// ClearQueueInput is the input for devfleet_clear_queue.
type ClearQueueInput struct {
	Confirm          bool `json:"confirm" jsonschema:"Set true to clear queued task messages and cancel pending DB tasks"`
	PreserveConsumer bool `json:"preserve_consumer,omitempty" jsonschema:"Set true to purge messages without resetting the JetStream durable task consumer"`
}

// ClearQueueOutput is the output for devfleet_clear_queue.
type ClearQueueOutput struct {
	Cleared               bool   `json:"cleared"`
	CancelledPendingTasks int    `json:"cancelled_pending_tasks"`
	Stream                string `json:"stream"`
	Subject               string `json:"subject"`
	MessagesBefore        uint64 `json:"messages_before"`
	MessagesAfter         uint64 `json:"messages_after"`
	ConsumerReset         bool   `json:"consumer_reset"`
}

// RegisterTools registers devfleet MCP tools.
func RegisterTools(server *mcp.Server, svc *Service) {
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_submit_task", Description: "Submit a development task to the dev runner fleet with optional OpenCode agent, provider, model ID, and variant selection."}, svc.submitTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_task_status", Description: "Get current status and result details for a devfleet task."}, svc.taskStatusTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_list_tasks", Description: "List recent devfleet tasks, optionally filtered by status."}, svc.listTasksTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_schedule_task", Description: "Create and activate a cron schedule that submits devfleet tasks."}, svc.scheduleTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_run_schedule", Description: "Run a devfleet cron task schedule immediately by name."}, svc.runScheduleTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_list_schedules", Description: "List devfleet cron task schedules."}, svc.listSchedulesTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_delete_schedule", Description: "Delete a devfleet cron task schedule by name."}, svc.deleteScheduleTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_update_schedule", Description: "Update a devfleet cron task schedule by name. Only provided fields are changed."}, svc.updateScheduleTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_sync_schedules", Description: "Load schedule definitions from YAML files in a directory and upsert them into the database."}, svc.syncSchedulesTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_task_events", Description: "Get the full event history for a devfleet task."}, svc.taskEventsTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_task_progress", Description: "Get a distilled progress timeline for a devfleet task."}, svc.taskProgressTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_task_latest_event", Description: "Get the most recent event for a devfleet task."}, svc.taskLatestEventTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_runner_health", Description: "Check runner fleet health including running/stale task counts, active runner image versions, and per-task heartbeat age."}, svc.runnerHealthTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_cancel_task", Description: "Cancel a single devfleet task by ID. Only works for pending or running tasks."}, svc.cancelTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "devfleet_clear_queue", Description: "Clear queued devfleet tasks by purging task messages, resetting the durable task consumer, and cancelling pending DB tasks. Requires confirm=true."}, svc.clearQueueTool)
	if svc != nil && svc.arcane != nil && svc.arcane.IsConfigured() {
		mcp.AddTool(server, &mcp.Tool{Name: "devfleet_arcane_scanner_status", Description: "Check if the Arcane Trivy vulnerability scanner is available and get its version."}, svc.arcaneScannerStatusTool)
		mcp.AddTool(server, &mcp.Tool{Name: "devfleet_arcane_environment_summary", Description: "Get aggregated vulnerability counts across all images in the Arcane environment."}, svc.arcaneEnvironmentSummaryTool)
		mcp.AddTool(server, &mcp.Tool{Name: "devfleet_arcane_list_images", Description: "List all Docker images in the Arcane environment with their IDs and tags."}, svc.arcaneListImagesTool)
		mcp.AddTool(server, &mcp.Tool{Name: "devfleet_arcane_image_summary", Description: "Get vulnerability summary for a specific Docker image by its ID."}, svc.arcaneImageSummaryTool)
		mcp.AddTool(server, &mcp.Tool{Name: "devfleet_arcane_list_vulnerabilities", Description: "List detailed vulnerabilities for an image with optional severity filtering and pagination."}, svc.arcaneListVulnerabilitiesTool)
	}
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
	task, err := s.store.GetTask(ctx, in.TaskID)
	if err != nil {
		return nil, TaskStatusOutput{}, fmt.Errorf("get task status: %w", err)
	}
	return nil, TaskStatusOutput{Task: taskToolRecord(task)}, nil
}

func (s *Service) listTasksTool(ctx context.Context, _ *mcp.CallToolRequest, in ListTasksInput) (*mcp.CallToolResult, ListTasksOutput, error) {
	tasks, err := s.store.ListTasks(ctx, in.Status, in.Limit)
	if err != nil {
		return nil, ListTasksOutput{}, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]TaskToolRecord, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, taskToolRecord(task))
	}
	return nil, ListTasksOutput{Tasks: out}, nil
}

func taskToolRecord(task store.TaskRecord) TaskToolRecord {
	return TaskToolRecord{
		ID:         task.ID,
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
	schedules, err := s.store.ListSchedules(ctx, in.EnabledOnly)
	if err != nil {
		return nil, ListSchedulesOutput{}, fmt.Errorf("list schedules: %w", err)
	}
	return nil, ListSchedulesOutput{Schedules: schedules}, nil
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
	existing, err := s.store.GetScheduleByName(ctx, in.Name)
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
		GitURL:     store.NonZero(in.GitURL, existing.GitURL),
		GitRef:     store.NonZero(in.GitRef, existing.GitRef),
		AgentImage: store.NonZero(in.AgentImage, existing.AgentImage),
		Agent:      store.NonZero(in.Agent, existing.Agent),
		ProviderID: store.NonZero(in.ProviderID, existing.ProviderID),
		ModelID:    store.NonZero(in.ModelID, existing.ModelID),
		VariantID:  store.NonZero(in.VariantID, existing.VariantID),
		Skills:     store.NonNilSlice(in.Skills, existing.Skills),
		TimeoutSec: store.NonZeroInt(in.TimeoutSec, existing.TimeoutSec),
	}
	schedule, err := s.UpdateSchedule(ctx, in.Name, merged, enabled)
	if err != nil {
		return nil, UpdateScheduleOutput{}, fmt.Errorf("update schedule: %w", err)
	}
	return nil, UpdateScheduleOutput{Schedule: schedule}, nil
}

func (s *Service) syncSchedulesTool(ctx context.Context, _ *mcp.CallToolRequest, in SyncSchedulesInput) (*mcp.CallToolResult, SyncSchedulesOutput, error) {
	if in.Directory == "" {
		return nil, SyncSchedulesOutput{}, fmt.Errorf("directory is required")
	}
	result, err := s.SyncSchedules(ctx, in.Directory, in.Prune)
	if err != nil {
		return nil, SyncSchedulesOutput{}, fmt.Errorf("sync schedules: %w", err)
	}
	return nil, SyncSchedulesOutput(result), nil
}

func (s *Service) taskEventsTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskEventsInput) (*mcp.CallToolResult, TaskEventsOutput, error) {
	if in.TaskID == "" {
		return nil, TaskEventsOutput{}, fmt.Errorf("task_id is required")
	}
	events, err := s.store.GetTaskEvents(ctx, in.TaskID, in.Limit)
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

func (s *Service) taskProgressTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskProgressInput) (*mcp.CallToolResult, TaskProgressOutput, error) {
	if in.TaskID == "" {
		return nil, TaskProgressOutput{}, fmt.Errorf("task_id is required")
	}
	events, err := s.store.GetTaskEvents(ctx, in.TaskID, in.Limit)
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
	ev, err := s.store.GetLatestTaskEvent(ctx, in.TaskID)
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
	// Publish cancellation notification to the runner before updating DB.
	if err := s.bus.PublishCancel(in.TaskID); err != nil {
		slog.Warn("publish cancel notification failed (continuing)", "taskID", in.TaskID, "err", err)
	}
	task, err := s.store.CancelTask(ctx, in.TaskID, in.Reason)
	if err != nil {
		return nil, CancelTaskOutput{}, fmt.Errorf("cancel task: %w", err)
	}
	return nil, CancelTaskOutput{Task: taskToolRecord(task)}, nil
}

func (s *Service) clearQueueTool(ctx context.Context, _ *mcp.CallToolRequest, in ClearQueueInput) (*mcp.CallToolResult, ClearQueueOutput, error) {
	if !in.Confirm {
		return nil, ClearQueueOutput{}, fmt.Errorf("confirm must be true to clear the queue")
	}
	queue, err := s.bus.ClearTaskQueue(in.PreserveConsumer)
	if err != nil {
		return nil, ClearQueueOutput{}, fmt.Errorf("clear task messages: %w", err)
	}
	cancelled, err := s.store.ClearPendingTasks(ctx, "cancelled by devfleet_clear_queue")
	if err != nil {
		return nil, ClearQueueOutput{}, fmt.Errorf("cancel pending tasks: %w", err)
	}
	return nil, ClearQueueOutput{
		Cleared:               true,
		CancelledPendingTasks: cancelled,
		Stream:                queue.Stream,
		Subject:               queue.Subject,
		MessagesBefore:        queue.MessagesBefore,
		MessagesAfter:         queue.MessagesAfter,
		ConsumerReset:         queue.ConsumerReset,
	}, nil
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
