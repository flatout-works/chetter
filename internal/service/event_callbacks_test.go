package service

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/ssrf"
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
	row := repository.EventCallback{
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

func TestCallbackTaskGitHubMetadata(t *testing.T) {
	t.Parallel()
	source := repository.Task{
		GithubRepo:           sql.NullString{String: "Flatout-Works/Chetter", Valid: true},
		GithubInstallationID: sql.NullInt64{Int64: 12345, Valid: true},
	}
	tests := []struct {
		name             string
		gitURL           string
		wantRepo         string
		wantInstallation int64
	}{
		{name: "repository-less callback inherits source", wantRepo: "Flatout-Works/Chetter", wantInstallation: 12345},
		{name: "same repository keeps pin", gitURL: "git@github.com:flatout-works/chetter.git", wantRepo: "flatout-works/chetter", wantInstallation: 12345},
		{name: "different repository drops pin", gitURL: "https://github.com/flatout-works/other.git", wantRepo: "flatout-works/other"},
		{name: "non GitHub repository has no metadata", gitURL: "https://gitlab.com/flatout-works/chetter.git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo, installationID := callbackTaskGitHubMetadata(source, tt.gitURL)
			if repo != tt.wantRepo || installationID != tt.wantInstallation {
				t.Fatalf("callbackTaskGitHubMetadata() = (%q, %d), want (%q, %d)", repo, installationID, tt.wantRepo, tt.wantInstallation)
			}
		})
	}
}

func TestValidateEventCallbackDestination(t *testing.T) {
	hardened := config.Config{}.WebhookDestinationPolicy()
	permissive := config.Config{WebhookAllowHTTP: true, WebhookAllowPrivate: true}.WebhookDestinationPolicy()

	tests := []struct {
		name    string
		input   EventCallbackInput
		pol     ssrf.Policy
		wantErr bool
	}{
		{
			"public https webhook passes",
			EventCallbackInput{Name: "h", EventType: "task.completed", ActionType: "webhook", ActionConfig: json.RawMessage(`{"url":"https://hooks.example.com/cb"}`)},
			hardened,
			false,
		},
		{
			"public https slack passes",
			EventCallbackInput{Name: "s", EventType: "task.completed", ActionType: "slack", ActionConfig: json.RawMessage(`{"url":"https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX"}`)},
			hardened,
			false,
		},
		{
			"http scheme rejected",
			EventCallbackInput{Name: "h", EventType: "task.completed", ActionType: "webhook", ActionConfig: json.RawMessage(`{"url":"http://hooks.example.com/cb"}`)},
			hardened,
			true,
		},
		{
			"http scheme passes with explicit override",
			EventCallbackInput{Name: "h", EventType: "task.completed", ActionType: "webhook", ActionConfig: json.RawMessage(`{"url":"http://127.0.0.1:8080/cb"}`)},
			permissive,
			false,
		},
		{
			"private IP rejected",
			EventCallbackInput{Name: "h", EventType: "task.completed", ActionType: "webhook", ActionConfig: json.RawMessage(`{"url":"https://10.0.0.5/cb"}`)},
			hardened,
			true,
		},
		{
			"cloud metadata hostname rejected",
			EventCallbackInput{Name: "h", EventType: "task.completed", ActionType: "webhook", ActionConfig: json.RawMessage(`{"url":"https://metadata.google.internal/cb"}`)},
			hardened,
			true,
		},
		{
			"empty url passes validation (delivery-time error)",
			EventCallbackInput{Name: "h", EventType: "task.completed", ActionType: "webhook", ActionConfig: json.RawMessage(`{"method":"POST"}`)},
			hardened,
			false,
		},
		{
			"create_task action ignores destination policy",
			EventCallbackInput{Name: "t", EventType: "task.completed", ActionType: "create_task", ActionConfig: json.RawMessage(`{"prompt":"http://10.0.0.5 not a destination"}`)},
			hardened,
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEventCallbackDestination(tc.input, tc.pol)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateWebhookDestinationAllowlist(t *testing.T) {
	pol := config.Config{WebhookAllowlistRaw: "10.0.0.0/8, .internal.example"}.WebhookDestinationPolicy()
	if err := validateWebhookDestination("https://10.1.2.3/hook", pol); err != nil {
		t.Errorf("allowlisted CIDR rejected: %v", err)
	}
	if err := validateWebhookDestination("https://hooks.internal.example/hook", pol); err != nil {
		t.Errorf("allowlisted hostname rejected: %v", err)
	}
	if err := validateWebhookDestination("https://192.168.1.5/hook", pol); err == nil {
		t.Errorf("non-allowlisted private IP should be rejected")
	}
}
