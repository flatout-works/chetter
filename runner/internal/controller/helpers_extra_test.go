package controller

import (
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/agentenv"
	"github.com/flatout-works/chetter/runner/internal/task"
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
		{"oomkilled container", "error", "task container exceeded its memory limit (OOMKilled): prompt failed: EOF", "resource_limit"},
		{"out of memory", "error", "container out of memory", "resource_limit"},
		{"resource limit", "error", "cgroup memory limit reached", "resource_limit"},
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

func TestDockerOOMMessage(t *testing.T) {
	tests := []struct {
		name    string
		oom     bool
		message string
		want    string
	}{
		{"oom killed enriches message", true, "prompt failed: EOF", "task container exceeded its memory limit (OOMKilled): prompt failed: EOF"},
		{"not oom leaves message unchanged", false, "prompt failed: EOF", "prompt failed: EOF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dockerOOMMessage(tc.oom, tc.message); got != tc.want {
				t.Errorf("dockerOOMMessage(%v, %q) = %q, want %q", tc.oom, tc.message, got, tc.want)
			}
		})
	}
}

// TestDockerOOMFailureMessageNoOpForNonDocker ensures the OOM check is a no-op
// when no container name is supplied (local/Kubernetes backends).
func TestDockerOOMFailureMessageNoOpForNonDocker(t *testing.T) {
	if got := dockerOOMFailureMessage("", "prompt failed: EOF"); got != "prompt failed: EOF" {
		t.Fatalf("dockerOOMFailureMessage with empty container = %q, want original message", got)
	}
}

func TestValidateTaskResourceLimits(t *testing.T) {
	tests := []struct {
		name    string
		req     task.TaskRequest
		wantErr bool
	}{
		{"unset allowed", task.TaskRequest{}, false},
		{"positive memory and cpu", task.TaskRequest{MaxMemoryMB: 4096, MaxCPU: 2}, false},
		{"zero memory and cpu allowed", task.TaskRequest{MaxMemoryMB: 0, MaxCPU: 0}, false},
		{"negative memory", task.TaskRequest{MaxMemoryMB: -1, MaxCPU: 2}, true},
		{"negative cpu", task.TaskRequest{MaxMemoryMB: 4096, MaxCPU: -1}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateTaskResourceLimits(tc.req)
			if tc.wantErr && got == "" {
				t.Fatalf("validateTaskResourceLimits(%+v) = %q, want error", tc.req, got)
			}
			if !tc.wantErr && got != "" {
				t.Fatalf("validateTaskResourceLimits(%+v) = %q, want no error", tc.req, got)
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
