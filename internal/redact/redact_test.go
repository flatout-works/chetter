package redact

import (
	"testing"
)

func TestNewSet_Empty(t *testing.T) {
	s := NewSet(nil)
	if !s.Empty() {
		t.Error("nil env should produce empty set")
	}
}

func TestNewSet_FiltersSensitiveKeys(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY":    "sk-abc123secret",
		"ANTHROPIC_API_KEY": "sk-ant-def456secret",
		"GITHUB_TOKEN":      "ghp_abc123token",
		"DB_PASSWORD":       "s3cret!",
		"DEBUG_MODE":        "visible-value",
		"empty_key":         "",
		"ANOTHER_TOKEN":     "ghp_abc123token", // duplicate value
	}
	s := NewSet(env)
	if s.Empty() {
		t.Fatal("expected non-empty set")
	}
	if len(s.values) != 4 {
		// 4 unique non-empty values: sk-abc123secret, sk-ant-def456secret,
		// ghp_abc123token, s3cret!
		t.Errorf("expected 4 values, got %d: %v", len(s.values), s.values)
	}
}

func TestNewSet_NoSensitiveKeys(t *testing.T) {
	env := map[string]string{
		"DEBUG":     "true",
		"HOME":      "/home/user",
		"LANG":      "en_US.UTF-8",
		"EMPTY_VAR": "",
	}
	s := NewSet(env)
	if !s.Empty() {
		t.Errorf("expected empty set, got %v", s.values)
	}
}

func TestApply_NilSet(t *testing.T) {
	var s *Set
	result := s.Apply("some text with sk-abc123secret in it")
	if result != "some text with sk-abc123secret in it" {
		t.Errorf("nil set should not modify text, got %q", result)
	}
}

func TestApply_EmptySet(t *testing.T) {
	s := &Set{}
	result := s.Apply("some text")
	if result != "some text" {
		t.Errorf("empty set should not modify text, got %q", result)
	}
}

func TestApply_RedactsExactMatches(t *testing.T) {
	s := NewSet(map[string]string{
		"API_KEY": "sk-abc123secret",
		"TOKEN":   "ghp_token_value",
	})
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single occurrence",
			in:   "Using API key: sk-abc123secret to authenticate",
			want: "Using API key: [REDACTED] to authenticate",
		},
		{
			name: "multiple occurrences",
			in:   "Key: sk-abc123secret token: ghp_token_value",
			want: "Key: [REDACTED] token: [REDACTED]",
		},
		{
			name: "no secrets",
			in:   "Task completed successfully",
			want: "Task completed successfully",
		},
		{
			name: "empty text",
			in:   "",
			want: "",
		},
		{
			name: "secret at boundaries",
			in:   "sk-abc123secret",
			want: "[REDACTED]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Apply(tt.in)
			if got != tt.want {
				t.Errorf("Apply(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestApply_SkipsShortValues(t *testing.T) {
	s := NewSet(map[string]string{
		"KEY": "ab", // 2 chars - should be skipped
	})
	result := s.Apply("text with ab in it")
	if result != "text with ab in it" {
		t.Errorf("short values should not be redacted, got %q", result)
	}
}

func TestApply_Redacts4CharSecret(t *testing.T) {
	s := NewSet(map[string]string{
		"KEY": "abcd",
	})
	result := s.Apply("text with abcd in it")
	if result != "text with [REDACTED] in it" {
		t.Errorf("4-char values should be redacted, got %q", result)
	}
}
