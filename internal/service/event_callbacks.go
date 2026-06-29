package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/repository"
)

const (
	EventCallbackActionCreateTask = "create_task"
	EventCallbackActionWebhook    = "webhook"
	EventCallbackActionSlack      = "slack"
)

type EventCallbackInput struct {
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
	Prompt      string            `json:"prompt"`
	GitURL      string            `json:"git_url"`
	GitRef      string            `json:"git_ref"`
	AgentImage  string            `json:"agent_image"`
	Agent       string            `json:"agent"`
	ProviderID  string            `json:"provider_id"`
	ModelID     string            `json:"model_id"`
	VariantID   string            `json:"variant_id"`
	Harness     string            `json:"harness"`
	Skills      []string          `json:"skills"`
	MCPProfiles []string          `json:"mcp_profiles"`
	Env         map[string]string `json:"env"`
	TimeoutSec  int               `json:"timeout_sec"`
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
	if err := validateEventCallbackInputForContext(ctx, in); err != nil {
		return EventCallbackRecord{}, err
	}
	teamID := callbackTeamID(ctx)
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
	teamID := callbackTeamID(ctx)
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
	if err := validateEventCallbackInputForContext(ctx, updated); err != nil {
		return EventCallbackRecord{}, err
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
	teamID := callbackTeamID(ctx)
	includeGlobal := !scoped || scope.Admin
	rows, err := s.repo.ListEventCallbacks(ctx, repository.ListEventCallbacksParams{
		EnabledOnly:     enabledOnly,
		EventTypeFilter: eventType,
		IncludeGlobal:   includeGlobal,
		TeamID:          nullString(teamID),
		Limit:           clampListLimit(limit),
		Offset:          int32(max(offset, 0)),
	})
	if err != nil {
		return nil, fmt.Errorf("list event callbacks: %w", err)
	}
	out := make([]EventCallbackRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventCallbackRecord(row))
	}
	return out, nil
}

func (s *Service) DeleteEventCallback(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("name is required")
	}
	rows, err := s.repo.DeleteEventCallback(ctx, repository.DeleteEventCallbackParams{
		Name:   name,
		TeamID: nullString(callbackTeamID(ctx)),
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

func (s *Service) runEventCallbackAction(ctx context.Context, event TaskEventCallbackContext, callback repository.ChetterEventCallback) error {
	switch callback.ActionType {
	case EventCallbackActionCreateTask:
		return s.runCreateTaskCallback(ctx, event, callback)
	case EventCallbackActionWebhook, EventCallbackActionSlack:
		return s.runWebhookCallback(ctx, event, callback)
	default:
		return fmt.Errorf("unsupported action_type %q", callback.ActionType)
	}
}

func (s *Service) runCreateTaskCallback(ctx context.Context, event TaskEventCallbackContext, callback repository.ChetterEventCallback) error {
	var cfg callbackCreateTaskConfig
	if err := json.Unmarshal(callback.ActionConfig, &cfg); err != nil {
		return fmt.Errorf("parse action_config: %w", err)
	}
	if cfg.Prompt == "" {
		return fmt.Errorf("create_task action_config.prompt is required")
	}
	allowMCPProfiles := !callback.TeamID.Valid || callback.TeamID.String == ""
	if len(nonEmptyStrings(cfg.MCPProfiles)) > 0 && !allowMCPProfiles {
		return fmt.Errorf("team-scoped event callbacks cannot use mcp_profiles in this trusted self-hosted MVP")
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
	_, err = s.SubmitTask(ctx, SubmitTaskRequest{
		TeamID:           event.TeamID,
		Prompt:           prompt,
		GitURL:           cfg.GitURL,
		GitRef:           cfg.GitRef,
		AgentImage:       cfg.AgentImage,
		Agent:            cfg.Agent,
		ProviderID:       cfg.ProviderID,
		ModelID:          cfg.ModelID,
		VariantID:        cfg.VariantID,
		Harness:          cfg.Harness,
		Skills:           cfg.Skills,
		MCPProfiles:      cfg.MCPProfiles,
		AllowMCPProfiles: allowMCPProfiles && len(nonEmptyStrings(cfg.MCPProfiles)) > 0,
		Env:              env,
		TimeoutSec:       cfg.TimeoutSec,
		TriggerName:      callback.Name,
		TriggerType:      "event_callback",
	})
	return err
}

func (s *Service) runWebhookCallback(ctx context.Context, event TaskEventCallbackContext, callback repository.ChetterEventCallback) error {
	var cfg callbackWebhookConfig
	if err := json.Unmarshal(callback.ActionConfig, &cfg); err != nil {
		return fmt.Errorf("parse action_config: %w", err)
	}
	if cfg.URL == "" {
		return fmt.Errorf("webhook action_config.url is required")
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
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

func validateEventCallbackInputForContext(ctx context.Context, in EventCallbackInput) error {
	if err := validateEventCallbackInput(in); err != nil {
		return err
	}
	if in.ActionType != EventCallbackActionCreateTask {
		return nil
	}
	var cfg callbackCreateTaskConfig
	if err := json.Unmarshal(in.ActionConfig, &cfg); err != nil {
		return fmt.Errorf("parse create_task action_config: %w", err)
	}
	if len(nonEmptyStrings(cfg.MCPProfiles)) > 0 && !isAdmin(ctx) {
		return fmt.Errorf("mcp_profiles in create_task callbacks require admin access in this trusted self-hosted MVP")
	}
	return nil
}

func callbackTeamID(ctx context.Context) string {
	scope, scoped := auth.GetScope(ctx)
	if scoped && !scope.Admin {
		return scope.TeamID
	}
	return teamIDFromContext(ctx)
}

func eventCallbackRecord(row repository.ChetterEventCallback) EventCallbackRecord {
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
