package service

import (
	"testing"
	"time"
)

func TestRepoFromGitURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https github", "https://github.com/flatout-works/chetter.git", "flatout-works/chetter"},
		{"https no .git", "https://github.com/flatout-works/chetter", "flatout-works/chetter"},
		{"ssh style", "git@github.com:flatout-works/chetter.git", "flatout-works/chetter"},
		{"https gitlab subgroup", "https://gitlab.com/group/subgroup/repo.git", "group/subgroup/repo"},
		{"empty", "", ""},
		{"no slash", "https://no-slash", ""},
		{"http protocol", "http://example.com/owner/repo.git", "owner/repo"},
		{"trailing slash", "https://github.com/owner/repo/", "owner/repo"},
		{"ssh no .git", "git@github.com:owner/repo", "owner/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := repoFromGitURL(tt.in)
			if got != tt.want {
				t.Errorf("repoFromGitURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseUsageWindow(t *testing.T) {
	t.Parallel()

	t.Run("default 30 days", func(t *testing.T) {
		t.Parallel()
		since, until, err := parseUsageWindow(UsageSummaryInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if since.IsZero() {
			t.Error("since should not be zero")
		}
		if !until.IsZero() {
			t.Error("until should be zero by default")
		}
		// Should be roughly 30 days ago.
		expected := time.Now().UTC().Add(-30 * 24 * time.Hour)
		diff := since.Sub(expected)
		if diff < -time.Minute || diff > time.Minute {
			t.Errorf("since %v not within 1 minute of expected %v", since, expected)
		}
	})

	t.Run("since hours overrides default", func(t *testing.T) {
		t.Parallel()
		since, until, err := parseUsageWindow(UsageSummaryInput{SinceHours: 24})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := time.Now().UTC().Add(-24 * time.Hour)
		diff := since.Sub(expected)
		if diff < -time.Minute || diff > time.Minute {
			t.Errorf("since %v not within 1 minute of expected %v", since, expected)
		}
		if !until.IsZero() {
			t.Error("until should be zero")
		}
	})

	t.Run("explicit since and until", func(t *testing.T) {
		t.Parallel()
		since, until, err := parseUsageWindow(UsageSummaryInput{
			Since: "2026-01-01T00:00:00Z",
			Until: "2026-01-31T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if since.Year() != 2026 || since.Month() != time.January || since.Day() != 1 {
			t.Errorf("unexpected since: %v", since)
		}
		if until.Year() != 2026 || until.Month() != time.January || until.Day() != 31 {
			t.Errorf("unexpected until: %v", until)
		}
	})

	t.Run("since hours overrides explicit since", func(t *testing.T) {
		t.Parallel()
		since, _, err := parseUsageWindow(UsageSummaryInput{
			Since:      "2025-01-01T00:00:00Z",
			SinceHours: 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := time.Now().UTC().Add(-1 * time.Hour)
		diff := since.Sub(expected)
		if diff < -time.Minute || diff > time.Minute {
			t.Errorf("since %v not within 1 minute of expected %v (since_hours should override explicit since)", since, expected)
		}
	})

	t.Run("invalid since", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseUsageWindow(UsageSummaryInput{Since: "not-a-date"})
		if err == nil {
			t.Fatal("expected error for invalid since")
		}
	})

	t.Run("invalid until", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseUsageWindow(UsageSummaryInput{Until: "not-a-date"})
		if err == nil {
			t.Fatal("expected error for invalid until")
		}
	})
}

func TestUsageSummaryOutputMatchesGeneratedSchema(t *testing.T) {
	t.Parallel()
	out := UsageSummaryOutput{
		Summary: []UsageSummaryRow{
			{
				TeamID:               "team_1",
				TeamName:             "my-team",
				TriggerName:          "nightly-docs",
				TriggerType:          "cron",
				Repo:                 "flatout-works/chetter",
				TaskCount:            5,
				TotalInputTokens:     1000,
				TotalOutputTokens:    500,
				TotalCacheReadTokens: 100,
				TotalCacheWriteTokens: 50,
				TotalReasoningTokens: 200,
				TotalTokens:          1850,
				CostCents:            15,
			},
		},
		Window: UsageWindow{
			Since:    "2026-01-01T00:00:00Z",
			Until:    "2026-02-01T00:00:00Z",
			GroupBy:  "trigger",
			RowCount: 1,
			TeamIDs:  []string{"team_1"},
		},
	}
	validateGeneratedOutputSchema(t, out)
}

func TestUsageSummaryToolRegistered(t *testing.T) {
	t.Parallel()
	// Tool registration is covered by TestRegisterTools which registers all tools
	// including chetter_usage_summary
}
