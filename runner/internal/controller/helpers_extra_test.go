package controller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInjectPATIntoURL(t *testing.T) {
	tests := []struct {
		name, url, pat, want string
	}{
		{
			"github with pat",
			"https://github.com/owner/repo.git",
			"abc123",
			"https://abc123:x-oauth-basic@github.com/owner/repo.git",
		},
		{
			"gitlab with pat",
			"https://gitlab.com/owner/repo.git",
			"tok",
			"https://tok:x-oauth-basic@gitlab.com/owner/repo.git",
		},
		{
			"empty pat",
			"https://github.com/owner/repo.git",
			"",
			"https://:x-oauth-basic@github.com/owner/repo.git",
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
			got := injectPATIntoURL(tc.url, tc.pat)
			if got != tc.want {
				t.Errorf("injectPATIntoURL(%q, %q) = %q, want %q", tc.url, tc.pat, got, tc.want)
			}
		})
	}
}

func TestKataRunCommand(t *testing.T) {
	t.Run("command with args", func(t *testing.T) {
		cmd := kataRunCommand([]string{"opencode", "run"})
		if len(cmd) != 3 {
			t.Fatalf("expected 3 elements, got %d", len(cmd))
		}
		if cmd[0] != "sh" || cmd[1] != "-c" {
			t.Fatalf("expected [sh -c ...], got %v", cmd[:2])
		}
		if !strings.Contains(cmd[2], "cd /tmp") {
			t.Errorf("expected 'cd /tmp' in command, got %q", cmd[2])
		}
		if !strings.Contains(cmd[2], "exec") {
			t.Errorf("expected 'exec' in command, got %q", cmd[2])
		}
	})
	t.Run("single arg", func(t *testing.T) {
		cmd := kataRunCommand([]string{"opencode"})
		if !strings.Contains(cmd[2], "'opencode'") {
			t.Errorf("expected quoted 'opencode' in command, got %q", cmd[2])
		}
	})
}

func TestSummarizeOpenCodeJSONL(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		got := summarizeOpenCodeJSONL("")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
	t.Run("valid jsonl with text event", func(t *testing.T) {
		line := `{"type":"text","part":{"text":"hello world"}}`
		got := summarizeOpenCodeJSONL(line)
		if !strings.Contains(got, "hello world") {
			t.Errorf("expected text content in summary, got %q", got)
		}
	})
	t.Run("multiple lines", func(t *testing.T) {
		line1 := `{"type":"text","part":{"text":"hello"}}`
		line2 := `{"type":"text","part":{"text":"world"}}`
		got := summarizeOpenCodeJSONL(line1 + "\n" + line2)
		if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
			t.Errorf("expected both texts in summary, got %q", got)
		}
	})
}

func TestSummarizeOpenCodeEvent(t *testing.T) {
	t.Run("text type", func(t *testing.T) {
		raw := `{"type":"text","text":"hello"}`
		got := summarizeOpenCodeEvent(raw)
		if got != "text" {
			t.Errorf("expected 'text', got %q", got)
		}
	})
	t.Run("tool_use type", func(t *testing.T) {
		raw := `{"type":"tool_use","properties":{"name":"read_file"}}`
		got := summarizeOpenCodeEvent(raw)
		if !strings.Contains(got, "tool_use") {
			t.Errorf("expected tool_use in summary, got %q", got)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		got := summarizeOpenCodeEvent("not json")
		if got == "" {
			t.Errorf("expected non-empty result for invalid json, got %q", got)
		}
		if len(got) > 303 {
			t.Errorf("expected truncation at 300+3 chars, got %d", len(got))
		}
	})
	t.Run("empty type", func(t *testing.T) {
		raw := `{"type":"","data":{}}`
		got := summarizeOpenCodeEvent(raw)
		if got != "" {
			t.Errorf("expected empty for empty type, got %q", got)
		}
	})
}

func TestCompactJSON(t *testing.T) {
	t.Run("simple map", func(t *testing.T) {
		m := map[string]any{"key": "value"}
		got := compactJSON(m)
		if !strings.Contains(got, "key") || !strings.Contains(got, "value") {
			t.Errorf("expected key/value in output, got %q", got)
		}
	})
	t.Run("large value truncation", func(t *testing.T) {
		m := map[string]any{"data": strings.Repeat("x", 600)}
		got := compactJSON(m)
		if len(got) > 503 {
			t.Errorf("expected truncation at 500+3 chars, got %d", len(got))
		}
	})
	t.Run("nil value", func(t *testing.T) {
		got := compactJSON(nil)
		if got != "" {
			t.Errorf("expected empty for nil, got %q", got)
		}
	})
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

func TestSummarizeOpenCodeEvent_Truncation(t *testing.T) {
	longText := strings.Repeat("a", 400)
	raw, _ := json.Marshal(map[string]any{"type": "unknown", "data": longText})
	got := summarizeOpenCodeEvent(string(raw))
	if got != "unknown" {
		t.Errorf("expected 'unknown' for default type, got %q", got)
	}
}
