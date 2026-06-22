package service

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
)

func TestValidateEventCallbackInput(t *testing.T) {
	validConfig := json.RawMessage(`{"url":"https://example.com/hook"}`)

	tests := []struct {
		name    string
		input   EventCallbackInput
		wantErr bool
	}{
		{
			"valid webhook callback",
			EventCallbackInput{Name: "my-hook", EventType: "task.completed", ActionType: "webhook", ActionConfig: validConfig, Enabled: true},
			false,
		},
		{
			"valid slack callback",
			EventCallbackInput{Name: "my-slack", EventType: "task.completed", ActionType: "slack", ActionConfig: validConfig, Enabled: true},
			false,
		},
		{
			"valid create_task callback",
			EventCallbackInput{Name: "my-task", EventType: "task.failed.*", ActionType: "create_task", ActionConfig: json.RawMessage(`{"prompt":"follow up"}`), Enabled: true},
			false,
		},
		{
			"empty name",
			EventCallbackInput{Name: "", EventType: "task.completed", ActionType: "webhook", ActionConfig: validConfig, Enabled: true},
			true,
		},
		{
			"empty event_type",
			EventCallbackInput{Name: "my-hook", EventType: "", ActionType: "webhook", ActionConfig: validConfig, Enabled: true},
			true,
		},
		{
			"invalid action_type",
			EventCallbackInput{Name: "my-hook", EventType: "task.completed", ActionType: "invalid", ActionConfig: validConfig, Enabled: true},
			true,
		},
		{
			"empty action_config",
			EventCallbackInput{Name: "my-hook", EventType: "task.completed", ActionType: "webhook", ActionConfig: nil, Enabled: true},
			true,
		},
		{
			"invalid JSON action_config",
			EventCallbackInput{Name: "my-hook", EventType: "task.completed", ActionType: "webhook", ActionConfig: json.RawMessage(`not json`), Enabled: true},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEventCallbackInput(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRenderEventTemplate(t *testing.T) {
	now := time.Now().UTC()
	event := TaskEventCallbackContext{
		ID:            "evt_123",
		TaskID:        "task_456",
		TeamID:        "team_789",
		Subject:       "runner.task_456",
		Status:        "error",
		EventType:     "task.failed.model_error",
		Summary:       "Model returned an error",
		Error:         "rate limit exceeded",
		ErrorCategory: "model_error",
		CreatedAt:     now,
	}

	tests := []struct {
		name     string
		template string
		want     string
		wantErr  bool
	}{
		{
			"simple template",
			"Task {{.TaskID}} failed: {{.Error}}",
			"Task task_456 failed: rate limit exceeded",
			false,
		},
		{
			"full context",
			"{{.ID}} | {{.EventType}} | {{.ErrorCategory}} | {{.TeamID}}",
			"evt_123 | task.failed.model_error | model_error | team_789",
			false,
		},
		{
			"missing key causes error",
			"{{.MissingKey}}",
			"",
			true,
		},
		{
			"empty template",
			"",
			"",
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderEventTemplate(tc.template, event)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tc.want {
				t.Errorf("renderEventTemplate(%q) = %q, want %q", tc.template, got, tc.want)
			}
		})
	}
}

func TestEventCallbackRecord(t *testing.T) {
	now := time.Now().UTC()
	row := repository.ChetterEventCallback{
		ID:           "ecb_1",
		TeamID:       sql.NullString{String: "team_1", Valid: true},
		Name:         "my-hook",
		EventType:    "task.completed",
		ActionType:   "webhook",
		ActionConfig: json.RawMessage(`{"url":"https://hook.example.com"}`),
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	rec := eventCallbackRecord(row)
	if rec.ID != "ecb_1" || rec.Name != "my-hook" || rec.EventType != "task.completed" {
		t.Errorf("basic fields mismatch: %+v", rec)
	}
	if rec.TeamID != "team_1" {
		t.Errorf("team_id = %q, want team_1", rec.TeamID)
	}
	if !rec.Enabled {
		t.Error("expected enabled = true")
	}
}

func TestTemplateData(t *testing.T) {
	now := time.Now().UTC()
	event := TaskEventCallbackContext{
		ID:            "evt_1",
		TaskID:        "task_1",
		TeamID:        "team_1",
		Subject:       "sub",
		Status:        "error",
		EventType:     "task.failed.timeout",
		Summary:       "timed out",
		Error:         "context deadline",
		ErrorCategory: "timeout",
		CreatedAt:     now,
	}
	data := templateData(event)
	if data.ID != "evt_1" || data.TaskID != "task_1" || data.ErrorCategory != "timeout" {
		t.Errorf("template data mismatch: %+v", data)
	}
	if data.CreatedAt != now.Format(time.RFC3339) {
		t.Errorf("created_at format: %q", data.CreatedAt)
	}
}
