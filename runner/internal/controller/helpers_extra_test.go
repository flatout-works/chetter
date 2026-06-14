package controller

import (
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
		if !strings.Contains(cmd[2], "opencode") {
			t.Errorf("expected 'opencode' in command, got %q", cmd[2])
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
