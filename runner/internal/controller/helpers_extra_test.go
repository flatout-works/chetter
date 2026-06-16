package controller

import (
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
