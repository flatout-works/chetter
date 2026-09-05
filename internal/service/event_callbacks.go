package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/githubrepo"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/ssrf"
)

const (
	EventCallbackActionCreateTask = "create_task"
	EventCallbackActionWebhook    = "webhook"
	EventCallbackActionSlack      = "slack"

	// eventCallbackRecursionError is the error recorded in task_events (and
	// the audit log) when a create_task callback would spawn a task deeper
	// than the configured callback-depth limit (issue #312).
	eventCallbackRecursionError = "event_callback_recursion_limit"
	// eventCallbackRejectedEvent is the task_events event_type used to record
	// a rejected callback spawn on the source (parent) task's event stream.
	eventCallbackRejectedEvent = "task.callback_rejected"
)

type EventCallbackInput struct {
	TeamID       string
	TeamName     string
	Name         string
	EventType    string
	ActionType   string
	ActionConfig json.RawMessage
	Enabled      bool
}

type EventCallbackRecord struct {
	ID           string          `json:"id"`
	TeamID       string          `json:"team_id,omitempty"`
	Name         string          `json:"name"`
	EventType    string          `json:"event_type"`
	ActionType   string          `json:"action_type"`
	ActionConfig json.RawMessage `json:"action_config"`
	Enabled      bool            `json:"enabled"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type callbackCreateTaskConfig struct {
	Prompt     string            `json:"prompt"`
	GitURL     string            `json:"git_url"`
	GitRef     string            `json:"git_ref"`
	AgentImage string            `json:"agent_image"`
	Agent      string            `json:"agent"`
	ProviderID string            `json:"provider_id"`
	ModelID    string            `json:"model_id"`
	VariantID  string            `json:"variant_id"`
	Harness    string            `json:"harness"`
	Skills     []string          `json:"skills"`
	Env        map[string]string `json:"env"`
	TimeoutSec int               `json:"timeout_sec"`
}

type callbackWebhookConfig struct {
	URL      string            `json:"url"`
	Method   string            `json:"method"`
	Headers  map[string]string `json:"headers"`
	Template string            `json:"template"`
	Text     string            `json:"text"`
}

type eventCallbackTemplateData struct {
	ID            string          `json:"id"`
	TaskID        string          `json:"task_id"`
	TeamID        string          `json:"team_id"`
	Subject       string          `json:"subject"`
	Status        string          `json:"status"`
	EventType     string          `json:"event_type"`
	Summary       string          `json:"summary"`
	Error         string          `json:"error"`
	ErrorCategory string          `json:"error_category"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     string          `json:"created_at"`
}

func (s *Service) CreateEventCallback(ctx context.Context, in EventCallbackInput) (EventCallbackRecord, error) {
	if err := validateEventCallbackInput(in); err != nil {
		return EventCallbackRecord{}, err
	}
	// SSRF-safe destination policy (issue #337): webhook/slack destinations
	// are checked up front so a disallowed URL is rejected with a clear error
	// at create time instead of failing (or worse, firing) at delivery time.
	if err := validateEventCallbackDestination(in, s.cfg.WebhookDestinationPolicy()); err != nil {
		s.auditWebhookDestinationRejection(ctx, in.Name, in.ActionType, err)
		return EventCallbackRecord{}, err
	}
	teamID, err := s.resolveOwnerTeamID(ctx, in.TeamID, in.TeamName)
	if err != nil {
		return EventCallbackRecord{}, err
	}
	id, err := randomID("ecb")
	if err != nil {
		return EventCallbackRecord{}, fmt.Errorf("generate event callback id: %w", err)
	}
	now := time.Now().UTC()
	if err := s.repo.InsertEventCallback(ctx, repository.InsertEventCallbackParams{
		ID:           id,
		TeamID:       nullString(teamID),
		Name:         in.Name,
		EventType:    in.EventType,
		ActionType:   in.ActionType,
		ActionConfig: in.ActionConfig,
		Enabled:      in.Enabled,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return EventCallbackRecord{}, fmt.Errorf("insert event callback: %w", err)
	}
	row, err := s.repo.GetEventCallbackByID(ctx, id)
	if err != nil {
		return EventCallbackRecord{}, fmt.Errorf("get event callback: %w", err)
	}
	return eventCallbackRecord(row), nil
}

func (s *Service) UpdateEventCallback(ctx context.Context, name string, in EventCallbackInput, enabled *bool) (EventCallbackRecord, error) {
	if name == "" {
		return EventCallbackRecord{}, fmt.Errorf("name is required")
	}
	teamID, err := s.resolveOwnerTeamID(ctx, in.TeamID, in.TeamName)
	if err != nil {
		return EventCallbackRecord{}, err
	}
	existing, err := s.repo.GetEventCallbackByName(ctx, repository.GetEventCallbackByNameParams{
		Name:   name,
		TeamID: nullString(teamID),
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return EventCallbackRecord{}, fmt.Errorf("event callback %q not found", name)
		}
		return EventCallbackRecord{}, fmt.Errorf("get event callback: %w", err)
	}
	updated := EventCallbackInput{
		Name:         existing.Name,
		EventType:    existing.EventType,
		ActionType:   existing.ActionType,
		ActionConfig: existing.ActionConfig,
		Enabled:      existing.Enabled,
	}
	if in.EventType != "" {
		updated.EventType = in.EventType
	}
	if in.ActionType != "" {
		updated.ActionType = in.ActionType
	}
	if len(in.ActionConfig) > 0 {
		updated.ActionConfig = in.ActionConfig
	}
	if enabled != nil {
		updated.Enabled = *enabled
	}
	if err := validateEventCallbackInput(updated); err != nil {
		return EventCallbackRecord{}, err
	}
	// SSRF-safe destination policy (issue #337): re-validate only when this
	// update actually changes the action config or type. Toggling enabled is
	// always allowed so an operator can disable (or later fix) a callback
	// whose stored destination predates the policy.
	if in.ActionType != "" || len(in.ActionConfig) > 0 {
		if err := validateEventCallbackDestination(updated, s.cfg.WebhookDestinationPolicy()); err != nil {
			s.auditWebhookDestinationRejection(ctx, name, updated.ActionType, err)
			return EventCallbackRecord{}, err
		}
	}
	rows, err := s.repo.UpdateEventCallback(ctx, repository.UpdateEventCallbackParams{
		EventType:    updated.EventType,
		ActionType:   updated.ActionType,
		ActionConfig: updated.ActionConfig,
		Enabled:      updated.Enabled,
		UpdatedAt:    time.Now().UTC(),
		Name:         name,
		TeamID:       nullString(teamID),
	})
	if err != nil {
		return EventCallbackRecord{}, fmt.Errorf("update event callback: %w", err)
	}
	if rows == 0 {
		return EventCallbackRecord{}, fmt.Errorf("event callback %q not found", name)
	}
	row, err := s.repo.GetEventCallbackByName(ctx, repository.GetEventCallbackByNameParams{Name: name, TeamID: nullString(teamID)})
	if err != nil {
		return EventCallbackRecord{}, fmt.Errorf("get event callback: %w", err)
	}
	return eventCallbackRecord(row), nil
}

func (s *Service) ListEventCallbacks(ctx context.Context, enabledOnly bool, eventType string, limit, offset int) ([]EventCallbackRecord, error) {
	scope, scoped := auth.GetScope(ctx)
	includeGlobal := !scoped || scope.Admin
	var rows []repository.EventCallback
	var err error
	if scoped && !scope.Admin {
		teamIDs := scope.Teams()
		if len(teamIDs) == 0 {
			return nil, nil
		}
		if len(teamIDs) > 1 {
			rows, err = s.repo.ListEventCallbacksByTeams(ctx, repository.ListEventCallbacksByTeamsParams{
				EnabledOnly:     enabledOnly,
				EventTypeFilter: eventType,
				TeamIds:         nullStringSlice(teamIDs),
				Limit:           clampListLimit(limit),
				Offset:          int32(max(offset, 0)),
			})
		} else {
			rows, err = s.repo.ListEventCallbacks(ctx, repository.ListEventCallbacksParams{
				EnabledOnly:     enabledOnly,
				EventTypeFilter: eventType,
				IncludeGlobal:   false,
				TeamID:          nullString(teamIDs[0]),
				Limit:           clampListLimit(limit),
				Offset:          int32(max(offset, 0)),
			})
		}
	} else {
		rows, err = s.repo.ListEventCallbacks(ctx, repository.ListEventCallbacksParams{
			EnabledOnly:     enabledOnly,
			EventTypeFilter: eventType,
			IncludeGlobal:   includeGlobal,
			TeamID:          sql.NullString{},
			Limit:           clampListLimit(limit),
			Offset:          int32(max(offset, 0)),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("list event callbacks: %w", err)
	}
	out := make([]EventCallbackRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventCallbackRecord(row))
	}
	return out, nil
}

func (s *Service) DeleteEventCallback(ctx context.Context, name, teamIDInput, teamName string) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("name is required")
	}
	teamID, err := s.resolveOwnerTeamID(ctx, teamIDInput, teamName)
	if err != nil {
		return false, err
	}
	rows, err := s.repo.DeleteEventCallback(ctx, repository.DeleteEventCallbackParams{
		Name:   name,
		TeamID: nullString(teamID),
	})
	if err != nil {
		return false, fmt.Errorf("delete event callback: %w", err)
	}
	return rows > 0, nil
}

func (s *Service) DispatchTaskEventCallbacks(ctx context.Context, event TaskEventCallbackContext) {
	callbacks, err := s.repo.ListEnabledEventCallbacksForEvent(ctx, repository.ListEnabledEventCallbacksForEventParams{
		TeamID:    nullString(event.TeamID),
		EventType: event.EventType,
	})
	if err != nil {
		slog.Warn("list event callbacks failed", "event_type", event.EventType, "task_id", event.TaskID, "error", err)
		return
	}
	for _, callback := range callbacks {
		if err := s.runEventCallbackAction(ctx, event, callback); err != nil {
			slog.Warn("event callback failed", "callback", callback.Name, "event_type", event.EventType, "task_id", event.TaskID, "error", err)
		}
	}
}

func (s *Service) runEventCallbackAction(ctx context.Context, event TaskEventCallbackContext, callback repository.EventCallback) error {
	switch callback.ActionType {
	case EventCallbackActionCreateTask:
		return s.runCreateTaskCallback(ctx, event, callback)
	case EventCallbackActionWebhook, EventCallbackActionSlack:
		return s.runWebhookCallback(ctx, event, callback)
	default:
		return fmt.Errorf("unsupported action_type %q", callback.ActionType)
	}
}

func (s *Service) runCreateTaskCallback(ctx context.Context, event TaskEventCallbackContext, callback repository.EventCallback) error {
	var cfg callbackCreateTaskConfig
	if err := json.Unmarshal(callback.ActionConfig, &cfg); err != nil {
		return fmt.Errorf("parse action_config: %w", err)
	}
	if cfg.Prompt == "" {
		return fmt.Errorf("create_task action_config.prompt is required")
	}
	prompt, err := renderEventTemplate(cfg.Prompt, event)
	if err != nil {
		return fmt.Errorf("render prompt: %w", err)
	}
	env := map[string]string{}
	for k, v := range cfg.Env {
		env[k] = v
	}
	env["CHETTER_EVENT_ID"] = event.ID
	env["CHETTER_EVENT_TYPE"] = event.EventType
	env["CHETTER_EVENT_TASK_ID"] = event.TaskID
	env["CHETTER_EVENT_CALLBACK"] = callback.Name
	sourceTask, err := s.repo.GetTaskByID(ctx, event.TaskID)
	if err != nil {
		return fmt.Errorf("load callback source task: %w", err)
	}
	// Provenance chain (issue #312): the spawned task records its parent and
	// its depth in the chain; the depth is enforced before the task is
	// created so a misconfigured task.completed -> create_task loop cannot
	// grow the queue unboundedly. Only the specific chain is stopped — the
	// callback itself stays enabled for unrelated tasks.
	childDepth := sourceTask.CallbackDepth + 1
	if s.cfg.CallbackMaxDepth > 0 && childDepth > int32(s.cfg.CallbackMaxDepth) {
		return s.rejectCallbackTaskSpawn(ctx, event, callback, sourceTask, childDepth)
	}
	env["CHETTER_EVENT_CALLBACK_DEPTH"] = strconv.Itoa(int(childDepth))
	env["CHETTER_EVENT_PARENT_TASK_ID"] = event.TaskID
	githubRepo, githubInstallationID := callbackTaskGitHubMetadata(sourceTask, cfg.GitURL)
	_, err = s.SubmitTask(ctx, SubmitTaskRequest{
		TeamID:               event.TeamID,
		Prompt:               prompt,
		GitURL:               cfg.GitURL,
		GitRef:               cfg.GitRef,
		GitHubRepo:           githubRepo,
		GitHubInstallationID: githubInstallationID,
		AgentImage:           cfg.AgentImage,
		Agent:                cfg.Agent,
		ProviderID:           cfg.ProviderID,
		ModelID:              cfg.ModelID,
		VariantID:            cfg.VariantID,
		Harness:              cfg.Harness,
		Skills:               cfg.Skills,
		Env:                  env,
		TimeoutSec:           cfg.TimeoutSec,
		TriggerName:          callback.Name,
		TriggerType:          "event_callback",
		SubmissionSource:     "event_callback",
		CallbackParentTaskID: event.TaskID,
		CallbackDepth:        int(childDepth),
	})
	return err
}

// rejectCallbackTaskSpawn records a rejected create_task callback spawn on
// the source (parent) task's event stream and in the audit log, then returns
// an error so the chain stops here. The callback itself is left enabled;
// only this recursive chain is refused (issue #312).
func (s *Service) rejectCallbackTaskSpawn(ctx context.Context, event TaskEventCallbackContext, callback repository.EventCallback, sourceTask repository.Task, childDepth int32) error {
	now := time.Now().UTC()
	payload := mustMarshalJSON(map[string]any{
		"task_id":           event.TaskID,
		"parent_task_id":    event.TaskID,
		"callback":          callback.Name,
		"callback_depth":    childDepth,
		"max_depth":         s.cfg.CallbackMaxDepth,
		"event_type":        event.EventType,
		"error":             eventCallbackRecursionError,
		"rejected_at":       now,
		"source_submission": sourceTask.SubmissionSource,
	})
	eventID, err := randomID("evt")
	if err != nil {
		return fmt.Errorf("generate callback rejection event id: %w", err)
	}
	subject := fmt.Sprintf("control.event_callback.%s", event.TaskID)
	if err := s.repo.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
		ID:        eventID,
		TaskID:    event.TaskID,
		Subject:   subject,
		Status:    "error",
		EventType: eventCallbackRejectedEvent,
		Payload:   payload,
		CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("record callback recursion rejection: %w", err)
	}
	detail := fmt.Sprintf("event callback %q (event %s) rejected: callback depth %d exceeds limit %d", callback.Name, event.EventType, childDepth, s.cfg.CallbackMaxDepth)
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "event_callback_recursion_limit",
		SourceType: "event_callback",
		SourceID:   callback.Name,
		TargetType: "task",
		TargetID:   event.TaskID,
		Detail:     detail,
		Payload:    payload,
	})
	slog.Warn("event callback recursion limit hit; spawn rejected", "callback", callback.Name, "event_type", event.EventType, "task_id", event.TaskID, "depth", childDepth, "max_depth", s.cfg.CallbackMaxDepth)
	return fmt.Errorf("%s: callback %q would create task at depth %d (limit %d)", eventCallbackRecursionError, callback.Name, childDepth, s.cfg.CallbackMaxDepth)
}

func callbackTaskGitHubMetadata(source repository.Task, gitURL string) (string, int64) {
	if strings.TrimSpace(gitURL) == "" {
		return source.GithubRepo.String, source.GithubInstallationID.Int64
	}
	repo, err := githubrepo.Parse(gitURL)
	if err != nil {
		return "", 0
	}
	installationID := int64(0)
	if source.GithubRepo.Valid && githubrepo.Same(source.GithubRepo.String, repo.FullName()) {
		installationID = source.GithubInstallationID.Int64
	}
	return repo.FullName(), installationID
}

func (s *Service) runWebhookCallback(ctx context.Context, event TaskEventCallbackContext, callback repository.EventCallback) error {
	var cfg callbackWebhookConfig
	if err := json.Unmarshal(callback.ActionConfig, &cfg); err != nil {
		return fmt.Errorf("parse action_config: %w", err)
	}
	if cfg.URL == "" {
		return fmt.Errorf("webhook action_config.url is required")
	}
	// Defense in depth (issue #337): re-check the destination before any
	// request even when the callback predates the policy, and audit any
	// rejection. The client additionally enforces the policy on every address
	// actually dialed, so a DNS rebinding cannot reach a blocked destination.
	if err := validateWebhookDestination(cfg.URL, s.cfg.WebhookDestinationPolicy()); err != nil {
		s.auditWebhookDestinationRejection(ctx, callback.Name, callback.ActionType, err)
		return err
	}
	body, err := renderWebhookBody(callback.ActionType, cfg, event)
	if err != nil {
		return err
	}
	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.webhookHTTPClient().Do(req)
	if err != nil {
		var polErr *ssrf.Error
		if errors.As(err, &polErr) {
			s.auditWebhookDestinationRejection(ctx, callback.Name, callback.ActionType, polErr)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func renderWebhookBody(actionType string, cfg callbackWebhookConfig, event TaskEventCallbackContext) ([]byte, error) {
	if cfg.Template != "" {
		rendered, err := renderEventTemplate(cfg.Template, event)
		if err != nil {
			return nil, fmt.Errorf("render webhook template: %w", err)
		}
		return []byte(rendered), nil
	}
	if actionType == EventCallbackActionSlack {
		text := cfg.Text
		if text == "" {
			text = fmt.Sprintf("Chetter event %s for task %s", event.EventType, event.TaskID)
		}
		rendered, err := renderEventTemplate(text, event)
		if err != nil {
			return nil, fmt.Errorf("render slack text: %w", err)
		}
		return json.Marshal(map[string]string{"text": rendered})
	}
	return json.Marshal(templateData(event))
}

func renderEventTemplate(tmpl string, event TaskEventCallbackContext) (string, error) {
	parsed, err := template.New("event_callback").Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := parsed.Execute(&out, templateData(event)); err != nil {
		return "", err
	}
	return out.String(), nil
}

func templateData(event TaskEventCallbackContext) eventCallbackTemplateData {
	return eventCallbackTemplateData{
		ID:            event.ID,
		TaskID:        event.TaskID,
		TeamID:        event.TeamID,
		Subject:       event.Subject,
		Status:        event.Status,
		EventType:     event.EventType,
		Summary:       event.Summary,
		Error:         event.Error,
		ErrorCategory: event.ErrorCategory,
		Payload:       event.Payload,
		CreatedAt:     event.CreatedAt.Format(time.RFC3339),
	}
}

func validateEventCallbackInput(in EventCallbackInput) error {
	if in.Name == "" {
		return fmt.Errorf("name is required")
	}
	if in.EventType == "" {
		return fmt.Errorf("event_type is required")
	}
	if in.ActionType != EventCallbackActionCreateTask && in.ActionType != EventCallbackActionWebhook && in.ActionType != EventCallbackActionSlack {
		return fmt.Errorf("action_type must be create_task, webhook, or slack")
	}
	if len(in.ActionConfig) == 0 || !json.Valid(in.ActionConfig) {
		return fmt.Errorf("action_config must be valid JSON")
	}
	return nil
}

// validateEventCallbackDestination applies the SSRF-safe destination policy to
// webhook/slack callback action configs (issue #337). It is a pure function so
// it is unit-testable without a database; the Create/Update paths audit a
// rejection before returning the error. create_task callbacks are unaffected.
func validateEventCallbackDestination(in EventCallbackInput, pol ssrf.Policy) error {
	if in.ActionType != EventCallbackActionWebhook && in.ActionType != EventCallbackActionSlack {
		return nil
	}
	var cfg callbackWebhookConfig
	if err := json.Unmarshal(in.ActionConfig, &cfg); err != nil {
		return fmt.Errorf("parse action_config: %w", err)
	}
	return validateWebhookDestination(cfg.URL, pol)
}

// validateWebhookDestination checks a single webhook/slack destination URL
// against the destination policy. An empty URL is left to the delivery-time
// error (existing semantics) rather than treated as a policy violation.
func validateWebhookDestination(rawURL string, pol ssrf.Policy) error {
	if rawURL == "" {
		return nil
	}
	return ssrf.ValidateDestination(rawURL, pol)
}

// auditWebhookDestinationRejection records a rejected webhook destination in
// the audit log. Detail carries only the policy error (scheme/host/IP), never
// credentials, headers, or query strings.
func (s *Service) auditWebhookDestinationRejection(ctx context.Context, callbackName, actionType string, err error) {
	detail := fmt.Sprintf("event callback %q (%s) destination rejected: %v", callbackName, actionType, err)
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "event_callback_destination_rejected",
		SourceType: "event_callback",
		SourceID:   callbackName,
		Detail:     detail,
	})
	slog.Warn("event callback destination rejected by SSRF-safe destination policy", "callback", callbackName, "action_type", actionType, "error", err)
}

// webhookHTTPClient returns the SSRF-safe client used for all outbound
// webhook/slack callback delivery (issue #337). It is built lazily on first
// use so configuration set after Service construction is honored, and it is
// never http.DefaultClient.
func (s *Service) webhookHTTPClient() *http.Client {
	s.webhookClientOnce.Do(func() {
		s.webhookClient = ssrf.NewClient(s.cfg.WebhookDestinationPolicy())
	})
	return s.webhookClient
}

func eventCallbackRecord(row repository.EventCallback) EventCallbackRecord {
	return EventCallbackRecord{
		ID:           row.ID,
		TeamID:       row.TeamID.String,
		Name:         row.Name,
		EventType:    row.EventType,
		ActionType:   row.ActionType,
		ActionConfig: row.ActionConfig,
		Enabled:      row.Enabled,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}
