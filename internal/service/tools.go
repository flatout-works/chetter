package service

import (
	"context"
	"database/sql"
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
	Prompt      string            `json:"prompt" jsonschema:"Task prompt to run in the Chetter runner"`
	GitURL      string            `json:"git_url,omitempty" jsonschema:"Repository URL to clone before running the task"`
	GitRef      string            `json:"git_ref,omitempty" jsonschema:"Branch tag or commit to check out"`
	AgentImage  string            `json:"agent_image,omitempty" jsonschema:"Runner harness image override"`
	Agent       string            `json:"agent,omitempty" jsonschema:"OpenCode agent to use for the task"`
	ProviderID  string            `json:"provider_id,omitempty" jsonschema:"OpenCode provider id for model selection"`
	ModelID     string            `json:"model_id,omitempty" jsonschema:"OpenCode model id, optionally provider-qualified"`
	VariantID   string            `json:"variant_id,omitempty" jsonschema:"OpenCode model variant, such as high or minimal"`
	Skills      []string          `json:"skills,omitempty" jsonschema:"Skill names or hints for the runner"`
	Env         map[string]string `json:"env,omitempty" jsonschema:"Additional non-secret environment variables"`
	Harness     string            `json:"harness,omitempty" jsonschema:"Runner harness to use (opencode, claude-code, pi; empty = runner default)"`
	TimeoutSec  int               `json:"timeout_sec,omitempty" jsonschema:"Task timeout in seconds"`
	SessionMode string            `json:"session_mode,omitempty" jsonschema:"Session mode: none (default) or resumable (requires gVisor)"`
	PauseReason string            `json:"pause_reason,omitempty" jsonschema:"Reason for pausing after run (for resumable sessions)"`
	TTLHours    int               `json:"ttl_hours,omitempty" jsonschema:"Hours before paused session expires (default 72)"`
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

// CreateTriggerInput is the input for chetter_create_trigger.
type CreateTriggerInput struct {
	Name        string   `json:"name" jsonschema:"Unique trigger name"`
	TriggerType string   `json:"trigger_type" jsonschema:"Trigger type: cron, pr_review, or issue"`
	CronExpr    string   `json:"cron_expr,omitempty" jsonschema:"Five-field cron expression or descriptor like @hourly (required for cron)"`
	Repo        string   `json:"repo,omitempty" jsonschema:"Repository to watch (required for pr_review and issue, e.g. flatout-works/chetter)"`
	Event       string   `json:"event,omitempty" jsonschema:"Webhook event to respond to (for issue triggers: opened, comment; optional, defaults to all)"`
	Prompt      string   `json:"prompt,omitempty" jsonschema:"Task prompt to submit when the trigger fires (optional for pr_review; defaults to built-in review template)"`
	GitURL      string   `json:"git_url,omitempty" jsonschema:"Repository URL to clone before running each task"`
	GitRef      string   `json:"git_ref,omitempty" jsonschema:"Branch tag or commit to check out"`
	AgentImage  string   `json:"agent_image,omitempty" jsonschema:"Runner harness image override"`
	Agent       string   `json:"agent,omitempty" jsonschema:"OpenCode agent to use"`
	ProviderID  string   `json:"provider_id,omitempty" jsonschema:"OpenCode provider id for model selection"`
	ModelID     string   `json:"model_id,omitempty" jsonschema:"OpenCode model id, optionally provider-qualified"`
	VariantID   string   `json:"variant_id,omitempty" jsonschema:"OpenCode model variant, such as high or minimal"`
	Skills      []string `json:"skills,omitempty" jsonschema:"Skill names or hints for the runner"`
	Harness     string   `json:"harness,omitempty" jsonschema:"Runner harness to use (opencode, claude-code, pi; empty = runner default)"`
	TimeoutSec  int      `json:"timeout_sec,omitempty" jsonschema:"Task timeout in seconds"`
	SessionMode string   `json:"session_mode,omitempty" jsonschema:"Session mode: none (default) or resumable (requires gVisor)"`
	PauseReason string   `json:"pause_reason,omitempty" jsonschema:"Reason for pausing after run (for resumable sessions)"`
	TTLHours    int      `json:"ttl_hours,omitempty" jsonschema:"Hours before paused session expires (default 72)"`
}

// CreateTriggerOutput is the output for chetter_create_trigger.
type CreateTriggerOutput struct {
	Trigger store.ScheduleRecord `json:"trigger"`
}

// UpdateTriggerInput is the input for chetter_update_trigger.
type UpdateTriggerInput struct {
	Name        string   `json:"name" jsonschema:"Name of the trigger to update"`
	TriggerType string   `json:"trigger_type,omitempty" jsonschema:"Trigger type: cron or pr_review"`
	CronExpr    string   `json:"cron_expr,omitempty" jsonschema:"Five-field cron expression or descriptor like @hourly"`
	Repo        string   `json:"repo,omitempty" jsonschema:"Repository to watch (for pr_review)"`
	Prompt      string   `json:"prompt,omitempty" jsonschema:"Task prompt to submit when the trigger fires"`
	GitURL      string   `json:"git_url,omitempty" jsonschema:"Repository URL to clone before running each task"`
	GitRef      string   `json:"git_ref,omitempty" jsonschema:"Branch tag or commit to check out"`
	AgentImage  string   `json:"agent_image,omitempty" jsonschema:"Runner harness image override"`
	Agent       string   `json:"agent,omitempty" jsonschema:"OpenCode agent to use"`
	ProviderID  string   `json:"provider_id,omitempty" jsonschema:"OpenCode provider id for model selection"`
	ModelID     string   `json:"model_id,omitempty" jsonschema:"OpenCode model id, optionally provider-qualified"`
	VariantID   string   `json:"variant_id,omitempty" jsonschema:"OpenCode model variant, such as high or minimal"`
	Skills      []string `json:"skills,omitempty" jsonschema:"Skill names or hints for the runner"`
	Enabled     *bool    `json:"enabled,omitempty" jsonschema:"Enable or disable the trigger"`
	Harness     string   `json:"harness,omitempty" jsonschema:"Runner harness to use (opencode, claude-code, pi; empty = runner default)"`
	TimeoutSec  int      `json:"timeout_sec,omitempty" jsonschema:"Task timeout in seconds"`
	SessionMode string   `json:"session_mode,omitempty" jsonschema:"Session mode: none (default) or resumable (requires gVisor)"`
	PauseReason string   `json:"pause_reason,omitempty" jsonschema:"Reason for pausing after run (for resumable sessions)"`
	TTLHours    int      `json:"ttl_hours,omitempty" jsonschema:"Hours before paused session expires (default 72)"`
}

// UpdateTriggerOutput is the output for chetter_update_trigger.
type UpdateTriggerOutput struct {
	Trigger store.ScheduleRecord `json:"trigger"`
}

// ListTriggersInput is the input for chetter_list_triggers.
type ListTriggersInput struct {
	EnabledOnly bool   `json:"enabled_only,omitempty" jsonschema:"Only return enabled triggers"`
	TriggerType string `json:"trigger_type,omitempty" jsonschema:"Filter by trigger type (cron, pr_review)"`
}

// ListTriggersOutput is the output for chetter_list_triggers.
type ListTriggersOutput struct {
	Triggers []store.ScheduleRecord `json:"triggers"`
}

// DeleteTriggerInput is the input for chetter_delete_trigger.
type DeleteTriggerInput struct {
	Name string `json:"name" jsonschema:"Name of the trigger to delete"`
}

// DeleteTriggerOutput is the output for chetter_delete_trigger.
type DeleteTriggerOutput struct {
	Deleted bool `json:"deleted"`
}

// RunTriggerInput is the input for chetter_run_trigger.
type RunTriggerInput struct {
	Name string `json:"name" jsonschema:"Name of the cron trigger to run immediately"`
}

// RunTriggerOutput is the output for chetter_run_trigger.
type RunTriggerOutput struct {
	Task TaskToolRecord `json:"task"`
}

// TaskEventsInput is the input for chetter_task_events.
type TaskEventsInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by chetter_submit_task"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum events to return, capped at 500"`
	Offset int    `json:"offset,omitempty" jsonschema:"Number of events to skip for pagination (default 0)"`
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
	Offset int    `json:"offset,omitempty" jsonschema:"Number of entries to skip for pagination (default 0)"`
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
	TeamName  string `json:"team_name" jsonschema:"Name of the team (created if it does not exist)"`
	UserName  string `json:"user_name" jsonschema:"Name of the user (created if it does not exist)"`
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

// --- Team Management Tools ---

// CreateTeamInput is the input for chetter_create_team.
type CreateTeamInput struct {
	Name string `json:"name" jsonschema:"Name of the team to create"`
}

// CreateTeamOutput is the output for chetter_create_team.
type CreateTeamOutput struct {
	TeamID    string    `json:"team_id"`
	TeamName  string    `json:"team_name"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTeamsInput is the input for chetter_list_teams.
type ListTeamsInput struct{}

// TeamInfo is a single team entry in the list.
type TeamInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTeamsOutput is the output for chetter_list_teams.
type ListTeamsOutput struct {
	Teams []TeamInfo `json:"teams"`
}

// DeleteTeamInput is the input for chetter_delete_team.
type DeleteTeamInput struct {
	Name string `json:"name" jsonschema:"Name of the team to delete. Cascades to users, tokens, tasks, and schedules."`
}

// DeleteTeamOutput is the output for chetter_delete_team.
type DeleteTeamOutput struct {
	Deleted bool `json:"deleted"`
}

// ListUsersInput is the input for chetter_list_users.
type ListUsersInput struct {
	TeamName string `json:"team_name,omitempty" jsonschema:"Optional team name to filter users by"`
}

// UserInfo is a single user entry in the list.
type UserInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TeamName  string    `json:"team_name"`
	CreatedAt time.Time `json:"created_at"`
}

// ListUsersOutput is the output for chetter_list_users.
type ListUsersOutput struct {
	Users []UserInfo `json:"users"`
}

// --- Schedule Run Tools ---

// ListScheduleRunsInput is the input for chetter_list_schedule_runs.
type ListScheduleRunsInput struct {
	ScheduleName string `json:"schedule_name,omitempty" jsonschema:"Optional schedule name to filter runs by"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum runs to return, capped at 100"`
}

// ScheduleRunInfo is a single schedule run entry in the list.
type ScheduleRunInfo struct {
	ID           string    `json:"id"`
	ScheduleName string    `json:"schedule_name"`
	TaskID       string    `json:"task_id"`
	Status       string    `json:"status"`
	ScheduledFor time.Time `json:"scheduled_for"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListScheduleRunsOutput is the output for chetter_list_schedule_runs.
type ListScheduleRunsOutput struct {
	Runs []ScheduleRunInfo `json:"runs"`
}

// TaskExportInput is the input for chetter_task_export.
type TaskExportInput struct {
	TaskID string `json:"task_id" jsonschema:"Task identifier returned by chetter_submit_task"`
}

// TaskExportOutput is the output for chetter_task_export.
type TaskExportOutput struct {
	Export string `json:"export"`
}

type ListAgentSessionsInput struct {
	Status string `json:"status,omitempty" jsonschema:"Optional agent session status filter"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum sessions to return, capped at 100"`
}

type AgentSessionRecord struct {
	ID               string     `json:"id"`
	TeamID           string     `json:"team_id,omitempty"`
	Status           string     `json:"status"`
	ResumeMode       string     `json:"resume_mode"`
	PinnedRunnerID   string     `json:"pinned_runner_id,omitempty"`
	CheckpointID     string     `json:"checkpoint_id,omitempty"`
	HarnessSessionID string     `json:"harness_session_id,omitempty"`
	GitURL           string     `json:"git_url,omitempty"`
	GitRef           string     `json:"git_ref,omitempty"`
	AgentImage       string     `json:"agent_image,omitempty"`
	Agent            string     `json:"agent,omitempty"`
	ProviderID       string     `json:"provider_id,omitempty"`
	ModelID          string     `json:"model_id,omitempty"`
	VariantID        string     `json:"variant_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	PausedAt         *time.Time `json:"paused_at,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	PauseReason      string     `json:"pause_reason,omitempty"`
	Error            string     `json:"error,omitempty"`
}

type SessionRunRecord struct {
	ID               string     `json:"id"`
	AgentSessionID   string     `json:"agent_session_id"`
	TaskID           string     `json:"task_id"`
	Status           string     `json:"status"`
	RequiredRunnerID string     `json:"required_runner_id,omitempty"`
	Summary          string     `json:"summary,omitempty"`
	Error            string     `json:"error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
}

type ListAgentSessionsOutput struct {
	Sessions []AgentSessionRecord `json:"sessions"`
}

type AgentSessionStatusInput struct {
	SessionID string `json:"session_id" jsonschema:"Agent session identifier"`
}

type AgentSessionStatusOutput struct {
	Session AgentSessionRecord `json:"session"`
	Runs    []SessionRunRecord `json:"runs"`
}

type ResumeAgentSessionInput struct {
	SessionID  string `json:"session_id" jsonschema:"Agent session identifier to resume"`
	Prompt     string `json:"prompt" jsonschema:"Follow-up prompt for the resumed agent"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"Task timeout in seconds"`
}

type ResumeAgentSessionOutput struct {
	Task TaskToolRecord   `json:"task"`
	Run  SessionRunRecord `json:"run"`
}

// RegisterTools registers chetter MCP tools.
func RegisterTools(server *mcp.Server, svc *Service) {
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_submit_task", Description: "Submit a development task to the Chetter runner fleet with optional OpenCode agent, provider, model ID, and variant selection."}, svc.submitTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_status", Description: "Get current status and result details for a chetter task."}, svc.taskStatusTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_tasks", Description: "List recent chetter tasks, optionally filtered by status."}, svc.listTasksTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_agent_sessions", Description: "List recent chetter agent sessions, optionally filtered by status."}, svc.listAgentSessionsTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_agent_session_status", Description: "Get an agent session with its session runs."}, svc.agentSessionStatusTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_resume_agent_session", Description: "Resume a paused agent session with a follow-up prompt."}, svc.resumeAgentSessionTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_create_trigger", Description: "Create a trigger (cron schedule or PR review webhook)."}, svc.createTriggerTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_update_trigger", Description: "Update a trigger by name. Only provided fields are changed."}, svc.updateTriggerTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_triggers", Description: "List triggers, optionally filtered by type and enabled status."}, svc.listTriggersTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_delete_trigger", Description: "Delete a trigger by name."}, svc.deleteTriggerTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_run_trigger", Description: "Run a cron trigger immediately by name."}, svc.runTriggerTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_events", Description: "Get the full event history for a chetter task."}, svc.taskEventsTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_progress", Description: "Get a distilled progress timeline for a chetter task."}, svc.taskProgressTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_latest_event", Description: "Get the most recent event for a chetter task."}, svc.taskLatestEventTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_runner_health", Description: "Check runner fleet health including running/stale task counts, active runner image versions, and per-task heartbeat age."}, svc.runnerHealthTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_cancel_task", Description: "Cancel a single chetter task by ID. Only works for pending or running tasks."}, svc.cancelTaskTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_task_export", Description: "Get the session export (markdown transcript) for a completed chetter task."}, svc.taskExportTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_clear_queue", Description: "Clear queued chetter tasks by cancelling pending DB-backed tasks. Admin only; requires confirm=true."}, svc.clearQueueTool)
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
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_create_team", Description: "Create a new team. Admin only."}, svc.createTeamTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_teams", Description: "List all teams. Admin only."}, svc.listTeamsTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_delete_team", Description: "Delete a team and cascade to its users, tokens, tasks, and schedules. Admin only."}, svc.deleteTeamTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_users", Description: "List all users, optionally filtered by team name. Admin only."}, svc.listUsersTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_schedule_runs", Description: "List schedule runs for the current team, optionally filtered by schedule name."}, svc.listScheduleRunsTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_audit_events", Description: "List server-side audit log events with optional filters. Admin only."}, svc.listAuditEventsTool)
	mcp.AddTool(server, &mcp.Tool{Name: "chetter_list_task_artifacts", Description: "List GitHub artifacts (issues, PRs, comments) created by chetter tasks. Admin only."}, svc.listTaskArtifactsTool)
}

func (s *Service) submitTaskTool(ctx context.Context, _ *mcp.CallToolRequest, in SubmitTaskInput) (*mcp.CallToolResult, SubmitTaskOutput, error) {
	task, err := s.SubmitTask(ctx, SubmitTaskRequest{
		Prompt:      in.Prompt,
		GitURL:      in.GitURL,
		GitRef:      in.GitRef,
		AgentImage:  in.AgentImage,
		Agent:       in.Agent,
		ProviderID:  in.ProviderID,
		ModelID:     in.ModelID,
		VariantID:   in.VariantID,
		Skills:      in.Skills,
		Env:         in.Env,
		Harness:     in.Harness,
		TimeoutSec:  in.TimeoutSec,
		SessionMode: in.SessionMode,
		PauseReason: in.PauseReason,
		TTLHours:    in.TTLHours,
	})
	if err != nil {
		return nil, SubmitTaskOutput{}, fmt.Errorf("submit task: %w", err)
	}
	return nil, SubmitTaskOutput{Task: taskToolRecord(task)}, nil
}

func (s *Service) taskStatusTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskStatusInput) (*mcp.CallToolResult, TaskStatusOutput, error) {
	task, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		return nil, TaskStatusOutput{}, fmt.Errorf("get task status: %w", err)
	}
	return nil, TaskStatusOutput{Task: task}, nil
}

func (s *Service) taskExportTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskExportInput) (*mcp.CallToolResult, TaskExportOutput, error) {
	export, err := s.ExportTask(ctx, in.TaskID)
	if err != nil {
		return nil, TaskExportOutput{}, err
	}
	return nil, TaskExportOutput{Export: export}, nil
}

func (s *Service) listTasksTool(ctx context.Context, _ *mcp.CallToolRequest, in ListTasksInput) (*mcp.CallToolResult, ListTasksOutput, error) {
	tasks, err := s.ListTasks(ctx, in.Status, in.Limit)
	if err != nil {
		return nil, ListTasksOutput{}, err
	}
	return nil, ListTasksOutput{Tasks: tasks}, nil
}

func (s *Service) listAgentSessionsTool(ctx context.Context, _ *mcp.CallToolRequest, in ListAgentSessionsInput) (*mcp.CallToolResult, ListAgentSessionsOutput, error) {
	sessions, err := s.ListAgentSessions(ctx, in.Status, in.Limit)
	if err != nil {
		return nil, ListAgentSessionsOutput{}, err
	}
	return nil, ListAgentSessionsOutput{Sessions: sessions}, nil
}

func (s *Service) agentSessionStatusTool(ctx context.Context, _ *mcp.CallToolRequest, in AgentSessionStatusInput) (*mcp.CallToolResult, AgentSessionStatusOutput, error) {
	session, runs, err := s.GetAgentSession(ctx, in.SessionID)
	if err != nil {
		return nil, AgentSessionStatusOutput{}, fmt.Errorf("get agent session: %w", err)
	}
	return nil, AgentSessionStatusOutput{Session: session, Runs: runs}, nil
}

func (s *Service) resumeAgentSessionTool(ctx context.Context, _ *mcp.CallToolRequest, in ResumeAgentSessionInput) (*mcp.CallToolResult, ResumeAgentSessionOutput, error) {
	if in.SessionID == "" {
		return nil, ResumeAgentSessionOutput{}, fmt.Errorf("session_id is required")
	}
	if in.Prompt == "" {
		return nil, ResumeAgentSessionOutput{}, fmt.Errorf("prompt is required")
	}
	out, err := s.ResumeAgentSession(ctx, in.SessionID, in.Prompt, in.TimeoutSec)
	if err != nil {
		return nil, ResumeAgentSessionOutput{}, err
	}
	return nil, out, nil
}

func clampListLimit(limit int) int32 {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return int32(limit)
}

func agentSessionRecord(session repository.ChetterAgentSession) AgentSessionRecord {
	return AgentSessionRecord{
		ID:               session.ID,
		TeamID:           session.TeamID.String,
		Status:           session.Status,
		ResumeMode:       session.ResumeMode,
		PinnedRunnerID:   session.PinnedRunnerID.String,
		CheckpointID:     session.CheckpointID.String,
		HarnessSessionID: session.HarnessSessionID.String,
		GitURL:           session.GitUrl.String,
		GitRef:           session.GitRef.String,
		AgentImage:       session.AgentImage.String,
		Agent:            session.Agent.String,
		ProviderID:       session.ProviderID.String,
		ModelID:          session.ModelID.String,
		VariantID:        session.VariantID.String,
		CreatedAt:        session.CreatedAt,
		UpdatedAt:        session.UpdatedAt,
		PausedAt:         nullTimePtr(session.PausedAt),
		ExpiresAt:        nullTimePtr(session.ExpiresAt),
		PauseReason:      session.PauseReason.String,
		Error:            session.Error.String,
	}
}

func sessionRunRecord(run repository.ChetterSessionRun) SessionRunRecord {
	return SessionRunRecord{
		ID:               run.ID,
		AgentSessionID:   run.AgentSessionID,
		TaskID:           run.TaskID,
		Status:           run.Status,
		RequiredRunnerID: run.RequiredRunnerID.String,
		Summary:          run.Summary.String,
		Error:            run.Error.String,
		CreatedAt:        run.CreatedAt,
		UpdatedAt:        run.UpdatedAt,
		StartedAt:        nullTimePtr(run.StartedAt),
		EndedAt:          nullTimePtr(run.EndedAt),
	}
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
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
		StartedAt:  store.NullTimePtr(task.StartedAt),
		EndedAt:    store.NullTimePtr(task.EndedAt),
	}
}

func (s *Service) createTriggerTool(ctx context.Context, _ *mcp.CallToolRequest, in CreateTriggerInput) (*mcp.CallToolResult, CreateTriggerOutput, error) {
	if in.TriggerType == "" {
		return nil, CreateTriggerOutput{}, fmt.Errorf("trigger_type is required (cron, pr_review, or issue)")
	}
	triggerConfig := ""
	switch in.TriggerType {
	case store.TriggerTypePRReview:
		if in.Repo == "" {
			return nil, CreateTriggerOutput{}, fmt.Errorf("repo is required for pr_review triggers")
		}
		if in.Agent == "" {
			return nil, CreateTriggerOutput{}, fmt.Errorf("agent is required for pr_review triggers")
		}
		cfg := store.PRReviewTriggerConfig{Repo: in.Repo}
		data, err := json.Marshal(cfg)
		if err != nil {
			return nil, CreateTriggerOutput{}, fmt.Errorf("marshal trigger config: %w", err)
		}
		triggerConfig = string(data)
	case store.TriggerTypeIssue:
		if in.Repo == "" {
			return nil, CreateTriggerOutput{}, fmt.Errorf("repo is required for issue triggers")
		}
		if in.Agent == "" {
			return nil, CreateTriggerOutput{}, fmt.Errorf("agent is required for issue triggers")
		}
		cfg := map[string]string{"repo": in.Repo}
		if in.Event != "" {
			cfg["event"] = in.Event
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			return nil, CreateTriggerOutput{}, fmt.Errorf("marshal trigger config: %w", err)
		}
		triggerConfig = string(data)
	}
	trigger, err := s.CreateTrigger(ctx, store.ScheduleInput{
		Name:          in.Name,
		TriggerType:   in.TriggerType,
		TriggerConfig: triggerConfig,
		CronExpr:      in.CronExpr,
		Prompt:        in.Prompt,
		GitURL:        in.GitURL,
		GitRef:        in.GitRef,
		AgentImage:    in.AgentImage,
		Agent:         in.Agent,
		ProviderID:    in.ProviderID,
		ModelID:       in.ModelID,
		VariantID:     in.VariantID,
		Harness:       in.Harness,
		Skills:        in.Skills,
		TimeoutSec:    in.TimeoutSec,
	})
	if err != nil {
		return nil, CreateTriggerOutput{}, fmt.Errorf("create trigger: %w", err)
	}
	return nil, CreateTriggerOutput{Trigger: trigger}, nil
}

func (s *Service) runTriggerTool(ctx context.Context, _ *mcp.CallToolRequest, in RunTriggerInput) (*mcp.CallToolResult, RunTriggerOutput, error) {
	task, err := s.RunTriggerNow(ctx, in.Name)
	if err != nil {
		return nil, RunTriggerOutput{}, fmt.Errorf("run trigger: %w", err)
	}
	return nil, RunTriggerOutput{Task: taskToolRecord(task)}, nil
}

func (s *Service) listTriggersTool(ctx context.Context, _ *mcp.CallToolRequest, in ListTriggersInput) (*mcp.CallToolResult, ListTriggersOutput, error) {
	triggers, err := s.ListTriggers(ctx, in.EnabledOnly, in.TriggerType)
	if err != nil {
		return nil, ListTriggersOutput{}, err
	}
	return nil, ListTriggersOutput{Triggers: triggers}, nil
}

func scheduleToStoreRecord(s repository.ChetterSchedule) store.ScheduleRecord {
	var skills []string
	_ = json.Unmarshal(s.Skills, &skills)
	return store.ScheduleRecord{
		ID:            s.ID,
		TeamID:        s.TeamID.String,
		Name:          s.Name,
		TriggerType:   s.TriggerType,
		TriggerConfig: string(s.TriggerConfig),
		CronExpr:      s.CronExpr,
		Prompt:        s.Prompt,
		GitURL:        s.GitUrl.String,
		GitRef:        s.GitRef.String,
		AgentImage:    s.AgentImage.String,
		Agent:         s.Agent.String,
		ProviderID:    s.ProviderID.String,
		ModelID:       s.ModelID.String,
		VariantID:     s.VariantID.String,
		Harness:       s.Harness.String,
		Skills:        skills,
		TimeoutSec:    int(s.TimeoutSec),
		Enabled:       s.Enabled,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
		LastRunAt:     store.NullTimePtr(s.LastRunAt),
		NextRunAt:     store.NullTimePtr(s.NextRunAt),
	}
}

func (s *Service) deleteTriggerTool(ctx context.Context, _ *mcp.CallToolRequest, in DeleteTriggerInput) (*mcp.CallToolResult, DeleteTriggerOutput, error) {
	if in.Name == "" {
		return nil, DeleteTriggerOutput{}, fmt.Errorf("name is required")
	}
	if err := s.DeleteTrigger(ctx, in.Name); err != nil {
		return nil, DeleteTriggerOutput{}, fmt.Errorf("delete trigger: %w", err)
	}
	return nil, DeleteTriggerOutput{Deleted: true}, nil
}

func (s *Service) updateTriggerTool(ctx context.Context, _ *mcp.CallToolRequest, in UpdateTriggerInput) (*mcp.CallToolResult, UpdateTriggerOutput, error) {
	if in.Name == "" {
		return nil, UpdateTriggerOutput{}, fmt.Errorf("name is required")
	}
	existing, err := s.repo.GetScheduleByName(ctx, in.Name)
	if err != nil {
		return nil, UpdateTriggerOutput{}, fmt.Errorf("get trigger %q: %w", in.Name, err)
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	triggerType := store.NonZero(in.TriggerType, existing.TriggerType)
	triggerConfig := existing.TriggerConfig
	if in.Repo != "" {
		var cfg store.PRReviewTriggerConfig
		if len(existing.TriggerConfig) > 0 {
			_ = json.Unmarshal(existing.TriggerConfig, &cfg)
		}
		cfg.Repo = in.Repo
		data, _ := json.Marshal(cfg)
		triggerConfig = data
	}
	merged := store.ScheduleInput{
		Name:          in.Name,
		TriggerType:   triggerType,
		TriggerConfig: string(triggerConfig),
		CronExpr:      store.NonZero(in.CronExpr, existing.CronExpr),
		Prompt:        store.NonZero(in.Prompt, existing.Prompt),
		GitURL:        store.NonZero(in.GitURL, existing.GitUrl.String),
		GitRef:        store.NonZero(in.GitRef, existing.GitRef.String),
		AgentImage:    store.NonZero(in.AgentImage, existing.AgentImage.String),
		Agent:         store.NonZero(in.Agent, existing.Agent.String),
		ProviderID:    store.NonZero(in.ProviderID, existing.ProviderID.String),
		ModelID:       store.NonZero(in.ModelID, existing.ModelID.String),
		VariantID:     store.NonZero(in.VariantID, existing.VariantID.String),
		Harness:       store.NonZero(in.Harness, existing.Harness.String),
		Skills:        store.NonNilSlice(in.Skills, scheduleSkillsToStrings(existing.Skills)),
		TimeoutSec:    store.NonZeroInt(in.TimeoutSec, int(existing.TimeoutSec)),
	}
	trigger, err := s.UpdateTrigger(ctx, in.Name, merged, enabled)
	if err != nil {
		return nil, UpdateTriggerOutput{}, fmt.Errorf("update trigger: %w", err)
	}
	return nil, UpdateTriggerOutput{Trigger: trigger}, nil
}

func scheduleSkillsToStrings(skills json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(skills, &out)
	return out
}

func (s *Service) taskEventsTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskEventsInput) (*mcp.CallToolResult, TaskEventsOutput, error) {
	events, err := s.GetTaskEvents(ctx, in.TaskID, in.Limit, in.Offset)
	if err != nil {
		return nil, TaskEventsOutput{}, err
	}
	return nil, TaskEventsOutput{Events: events}, nil
}

func clampEventLimit(limit int) int32 {
	if limit <= 0 || limit > 500 {
		return 50
	}
	return int32(limit)
}

func (s *Service) taskProgressTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskProgressInput) (*mcp.CallToolResult, TaskProgressOutput, error) {
	entries, err := s.GetTaskProgress(ctx, in.TaskID, in.Limit, in.Offset)
	if err != nil {
		return nil, TaskProgressOutput{}, err
	}
	return nil, TaskProgressOutput{Entries: entries}, nil
}

func (s *Service) taskLatestEventTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskLatestEventInput) (*mcp.CallToolResult, TaskLatestEventOutput, error) {
	out, err := s.GetLatestTaskEvent(ctx, in.TaskID)
	if err != nil {
		return nil, TaskLatestEventOutput{}, err
	}
	return nil, out, nil
}

func (s *Service) runnerHealthTool(ctx context.Context, _ *mcp.CallToolRequest, in RunnerHealthInput) (*mcp.CallToolResult, RunnerHealthOutput, error) {
	health, err := s.GetRunnerHealth(ctx, in.IncludeTasks)
	if err != nil {
		return nil, RunnerHealthOutput{}, err
	}
	return nil, RunnerHealthOutput{Health: health}, nil
}

func (s *Service) cancelTaskTool(ctx context.Context, _ *mcp.CallToolRequest, in CancelTaskInput) (*mcp.CallToolResult, CancelTaskOutput, error) {
	task, err := s.CancelTask(ctx, in.TaskID, in.Reason)
	if err != nil {
		return nil, CancelTaskOutput{}, err
	}
	return nil, CancelTaskOutput{Task: task}, nil
}

func (s *Service) clearQueueTool(ctx context.Context, _ *mcp.CallToolRequest, in ClearQueueInput) (*mcp.CallToolResult, ClearQueueOutput, error) {
	if !in.Confirm {
		return nil, ClearQueueOutput{}, fmt.Errorf("confirm must be true to clear the queue")
	}
	cancelled, err := s.ClearQueue(ctx)
	if err != nil {
		return nil, ClearQueueOutput{}, err
	}
	return nil, ClearQueueOutput{
		Cleared:               true,
		CancelledPendingTasks: cancelled,
	}, nil
}

// --- Token Management Tools ---

func (s *Service) createTokenTool(ctx context.Context, _ *mcp.CallToolRequest, in CreateTokenInput) (*mcp.CallToolResult, CreateTokenOutput, error) {
	out, err := s.CreateToken(ctx, in.TeamName, in.UserName, in.TokenName)
	if err != nil {
		return nil, CreateTokenOutput{}, err
	}
	return nil, out, nil
}

func (s *Service) listTokensTool(ctx context.Context, _ *mcp.CallToolRequest, _ ListTokensInput) (*mcp.CallToolResult, ListTokensOutput, error) {
	tokens, err := s.ListTokens(ctx)
	if err != nil {
		return nil, ListTokensOutput{}, err
	}
	return nil, ListTokensOutput{Tokens: tokens}, nil
}

func (s *Service) deleteTokenTool(ctx context.Context, _ *mcp.CallToolRequest, in DeleteTokenInput) (*mcp.CallToolResult, DeleteTokenOutput, error) {
	if err := s.DeleteToken(ctx, in.Name); err != nil {
		return nil, DeleteTokenOutput{}, err
	}
	return nil, DeleteTokenOutput{Deleted: true}, nil
}

// --- Team Management Tool Handlers ---

func (s *Service) createTeamTool(ctx context.Context, _ *mcp.CallToolRequest, in CreateTeamInput) (*mcp.CallToolResult, CreateTeamOutput, error) {
	out, err := s.CreateTeam(ctx, in.Name)
	if err != nil {
		return nil, CreateTeamOutput{}, err
	}
	return nil, out, nil
}

func (s *Service) listTeamsTool(ctx context.Context, _ *mcp.CallToolRequest, _ ListTeamsInput) (*mcp.CallToolResult, ListTeamsOutput, error) {
	teams, err := s.ListTeams(ctx)
	if err != nil {
		return nil, ListTeamsOutput{}, err
	}
	return nil, ListTeamsOutput{Teams: teams}, nil
}

func (s *Service) deleteTeamTool(ctx context.Context, _ *mcp.CallToolRequest, in DeleteTeamInput) (*mcp.CallToolResult, DeleteTeamOutput, error) {
	if err := s.DeleteTeam(ctx, in.Name); err != nil {
		return nil, DeleteTeamOutput{}, err
	}
	return nil, DeleteTeamOutput{Deleted: true}, nil
}

func (s *Service) listUsersTool(ctx context.Context, _ *mcp.CallToolRequest, in ListUsersInput) (*mcp.CallToolResult, ListUsersOutput, error) {
	users, err := s.ListUsers(ctx, in.TeamName)
	if err != nil {
		return nil, ListUsersOutput{}, err
	}
	return nil, ListUsersOutput{Users: users}, nil
}

// --- Schedule Run Tool Handlers ---

func (s *Service) listScheduleRunsTool(ctx context.Context, _ *mcp.CallToolRequest, in ListScheduleRunsInput) (*mcp.CallToolResult, ListScheduleRunsOutput, error) {
	runs, err := s.ListScheduleRuns(ctx, in.ScheduleName, in.Limit)
	if err != nil {
		return nil, ListScheduleRunsOutput{}, err
	}
	return nil, ListScheduleRunsOutput{Runs: runs}, nil
}

func isAdmin(ctx context.Context) bool {
	scope, ok := auth.GetScope(ctx)
	return ok && scope.Admin
}

func (s *Service) taskForToolAccess(ctx context.Context, taskID string) (repository.ChetterTask, error) {
	task, err := s.repo.GetTaskByID(ctx, taskID)
	if err != nil {
		return repository.ChetterTask{}, err
	}
	if err := authorizeTaskToolAccess(ctx, task); err != nil {
		return repository.ChetterTask{}, err
	}
	return task, nil
}

func authorizeTaskToolAccess(ctx context.Context, task repository.ChetterTask) error {
	scope, scoped := auth.GetScope(ctx)
	if !scoped || scope.Admin {
		return nil
	}
	if scope.TeamID == "" || !task.TeamID.Valid || task.TeamID.String != scope.TeamID {
		return fmt.Errorf("task not found")
	}
	return nil
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

type AuditEventFilterInput struct {
	EventType  string `json:"event_type,omitempty" jsonschema:"Filter by event type (e.g. webhook_received, task_submitted)"`
	SourceType string `json:"source_type,omitempty" jsonschema:"Filter by source type (e.g. webhook, trigger, task)"`
	SourceID   string `json:"source_id,omitempty" jsonschema:"Filter by source ID (e.g. delivery ID, trigger name)"`
	TargetType string `json:"target_type,omitempty" jsonschema:"Filter by target type (e.g. issue, pr, task)"`
	TargetID   string `json:"target_id,omitempty" jsonschema:"Filter by target ID"`
	Repo       string `json:"repo,omitempty" jsonschema:"Filter by repository (e.g. flatout-works/chetter)"`
	SinceHours int    `json:"since_hours,omitempty" jsonschema:"Only return events from the last N hours (default 24)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum events to return (default 100, max 500)"`
}

type AuditEventRecord struct {
	ID               string    `json:"id"`
	EventType        string    `json:"event_type"`
	CreatedAt        time.Time `json:"created_at"`
	SourceType       string    `json:"source_type,omitempty"`
	SourceID         string    `json:"source_id,omitempty"`
	TargetType       string    `json:"target_type,omitempty"`
	TargetID         string    `json:"target_id,omitempty"`
	Repo             string    `json:"repo,omitempty"`
	GitHubEvent      string    `json:"github_event,omitempty"`
	GitHubAction     string    `json:"github_action,omitempty"`
	GitHubDeliveryID string    `json:"github_delivery_id,omitempty"`
	ParentEventID    string    `json:"parent_event_id,omitempty"`
	Detail           string    `json:"detail,omitempty"`
}

type AuditEventsOutput struct {
	Events []AuditEventRecord `json:"events"`
}

func (s *Service) listAuditEventsTool(ctx context.Context, _ *mcp.CallToolRequest, in AuditEventFilterInput) (*mcp.CallToolResult, AuditEventsOutput, error) {
	events, err := s.ListAuditEvents(ctx, in)
	if err != nil {
		return nil, AuditEventsOutput{}, err
	}
	return nil, AuditEventsOutput{Events: events}, nil
}

type TaskArtifactFilterInput struct {
	TaskID         string `json:"task_id,omitempty" jsonschema:"Filter by task ID"`
	AgentSessionID string `json:"agent_session_id,omitempty" jsonschema:"Filter by agent session ID"`
	ArtifactType   string `json:"artifact_type,omitempty" jsonschema:"Filter by artifact type (issue, pr, issue_comment, pr_review)"`
	Repo           string `json:"repo,omitempty" jsonschema:"Filter by repository"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum artifacts to return (default 100, max 500)"`
}

type TaskArtifactRecord struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"task_id"`
	AgentSessionID  string    `json:"agent_session_id,omitempty"`
	SessionRunID    string    `json:"session_run_id,omitempty"`
	ArtifactType    string    `json:"artifact_type"`
	Repo            string    `json:"repo"`
	Number          int       `json:"number,omitempty"`
	URL             string    `json:"url,omitempty"`
	Ref             string    `json:"ref,omitempty"`
	SHA             string    `json:"sha,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	DiscoveredAt    time.Time `json:"discovered_at"`
	DiscoverySource string    `json:"discovery_source"`
}

type TaskArtifactsOutput struct {
	Artifacts []TaskArtifactRecord `json:"artifacts"`
}

func (s *Service) listTaskArtifactsTool(ctx context.Context, _ *mcp.CallToolRequest, in TaskArtifactFilterInput) (*mcp.CallToolResult, TaskArtifactsOutput, error) {
	artifacts, err := s.ListTaskArtifacts(ctx, in)
	if err != nil {
		return nil, TaskArtifactsOutput{}, err
	}
	return nil, TaskArtifactsOutput{Artifacts: artifacts}, nil
}
