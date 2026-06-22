package service

import (
	"testing"
)

func TestNormalizeErrorCategory(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"budget_exceeded", "budget_exceeded"},
		{"model_error", "model_error"},
		{"runtime_error", "runtime_error"},
		{"timeout", "timeout"},
		{"stuck", "stuck"},
		{"cancelled", "cancelled"},
		{"unknown", "unknown"},
		{"", ""},
		{"random_value", ""},
		{"BUDGET_EXCEEDED", ""},
		{" model_error", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeErrorCategory(tc.input)
			if got != tc.want {
				t.Errorf("normalizeErrorCategory(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestStatusIsErrorCategoryCandidate(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"error", true},
		{"cancelled", true},
		{"done", false},
		{"running", false},
		{"pending", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			got := statusIsErrorCategoryCandidate(tc.status)
			if got != tc.want {
				t.Errorf("statusIsErrorCategoryCandidate(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestClassifyTaskErrorCategory(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		message string
		want    string
	}{
		{"cancelled status", "cancelled", "user requested cancel", "cancelled"},
		{"cancelled regardless of message", "cancelled", "budget exceeded", "cancelled"},
		{"budget exceeded", "error", "Budget limit reached", "budget_exceeded"},
		{"budget different wording", "error", "cost limit exceeded", "budget_exceeded"},
		{"max budget", "error", "Max budget of $10 reached", "budget_exceeded"},
		{"timeout", "error", "context deadline exceeded", "timeout"},
		{"timeout lease", "error", "lease expired", "timeout"},
		{"stuck", "error", "stuck in loop", "stuck"},
		{"model error", "error", "model returned invalid response", "model_error"},
		{"llm error", "error", "LLM provider error", "model_error"},
		{"rate limit", "error", "rate limit exceeded", "model_error"},
		{"provider error", "error", "provider api error", "model_error"},
		{"api error", "error", "API error 500", "model_error"},
		{"empty message", "error", "", "unknown"},
		{"generic error", "error", "something went wrong", "runtime_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTaskErrorCategory(tc.status, tc.message)
			if got != tc.want {
				t.Errorf("classifyTaskErrorCategory(%q, %q) = %q, want %q", tc.status, tc.message, got, tc.want)
			}
		})
	}
}

func TestTaskEventType(t *testing.T) {
	tests := []struct {
		name          string
		status        string
		errorCategory string
		heartbeat     bool
		want          string
	}{
		{"heartbeat", "running", "", true, "task.heartbeat"},
		{"completed", "done", "", false, "task.completed"},
		{"completed aliased status", "completed", "", false, "task.completed"},
		{"failed with category", "error", "model_error", false, "task.failed.model_error"},
		{"failed with empty category defaults to unknown", "error", "", false, "task.failed.unknown"},
		{"failed runtime", "error", "runtime_error", false, "task.failed.runtime_error"},
		{"cancelled", "cancelled", "", false, "task.cancelled"},
		{"running progress", "running", "", false, "task.progress"},
		{"custom status", "claimed", "", false, "task.claimed"},
		{"empty status", "", "", false, "task.unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := taskEventType(tc.status, tc.errorCategory, tc.heartbeat)
			if got != tc.want {
				t.Errorf("taskEventType(%q, %q, %v) = %q, want %q", tc.status, tc.errorCategory, tc.heartbeat, got, tc.want)
			}
		})
	}
}

func TestSanitizeEventTypePart(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"running", "running"},
		{"claimed", "claimed"},
		{"UPPERCASE", "uppercase"},
		{"Mixed_Case-With.dots", "mixed_case-with.dots"},
		{"special chars!@#", "special_chars___"},
		{"", "unknown"},
		{"   ", "___"},
		{"a", "a"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := sanitizeEventTypePart(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeEventTypePart(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
