package controller

import (
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/agentenv"
)

func TestClassifyErrorCategory(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		message string
		want    string
	}{
		{"cancelled status", "cancelled", "user requested", "cancelled"},
		{"budget exceeded", "error", "Budget limit reached", "budget_exceeded"},
		{"cost limit", "error", "cost limit exceeded", "budget_exceeded"},
		{"max budget", "error", "max budget of $10", "budget_exceeded"},
		{"timeout", "error", "context deadline exceeded", "timeout"},
		{"deadline exceeded", "error", "deadline exceeded", "timeout"},
		{"opencode eof", "error", `prompt failed: POST /message: Post "http://127.0.0.1/session/ses/message": EOF`, "transport_error"},
		{"opencode reset", "error", `prompt failed: POST /message: read: connection reset by peer`, "transport_error"},
		{"stuck", "error", "stuck in a loop", "stuck"},
		{"model error", "error", "model returned invalid", "model_error"},
		{"llm error", "error", "LLM provider error", "model_error"},
		{"rate limit", "error", "rate limit exceeded", "model_error"},
		{"provider error", "error", "provider api error", "model_error"},
		{"api error", "error", "API error 500", "model_error"},
		{"empty message", "error", "", "unknown"},
		{"generic error", "error", "something went wrong", "runtime_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyErrorCategory(tc.status, tc.message)
			if got != tc.want {
				t.Errorf("classifyErrorCategory(%q, %q) = %q, want %q", tc.status, tc.message, got, tc.want)
			}
		})
	}
}

func TestInjectPATIntoURL(t *testing.T) {
	tests := []struct {
		name, url, pat, want string
	}{
		{
			"github with pat",
			"https://github.com/owner/repo.git",
			"abc123",
			"https://abc123@github.com/owner/repo.git",
		},
		{
			"gitlab with pat",
			"https://gitlab.com/owner/repo.git",
			"tok",
			"https://tok@gitlab.com/owner/repo.git",
		},
		{
			"empty pat",
			"https://github.com/owner/repo.git",
			"",
			"https://github.com/owner/repo.git",
		},
		{
			"non-https url",
			"git@github.com:owner/repo.git",
			"abc123",
			"git@github.com:owner/repo.git",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := agentenv.InjectPATIntoURL(tc.url, tc.pat)
			if got != tc.want {
				t.Errorf("injectPATIntoURL(%q, %q) = %q, want %q", tc.url, tc.pat, got, tc.want)
			}
		})
	}
}

func TestTaskPromptTimeout(t *testing.T) {
	tests := []struct {
		input int
		want  time.Duration
	}{
		{0, 3600 * time.Second},
		{-1, 3600 * time.Second},
		{100, 100 * time.Second},
	}
	for _, tc := range tests {
		got := taskPromptTimeout(tc.input)
		if got != tc.want {
			t.Errorf("taskPromptTimeout(%d) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
