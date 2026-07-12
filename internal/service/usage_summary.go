package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
)

// UsageSummaryInput is the input for chetter_usage_summary.
type UsageSummaryInput struct {
	// GroupBy selects the grouping dimension: "team", "trigger", "repo", or
	// empty (all dimensions). When GroupBy is empty the summary is grouped by
	// team, trigger name/type, and repository together.
	GroupBy string `json:"group_by,omitempty" jsonschema:"Grouping dimension: team, trigger, repo, or empty for all"`

	// Time window. Only one of SinceHours or Since/Until need be set.
	// SinceHours looks back from now. Since/Until sets an absolute window.
	SinceHours int    `json:"since_hours,omitempty" jsonschema:"Only include tasks from the last N hours"`
	Since      string `json:"since,omitempty" jsonschema:"ISO 8601 start time (inclusive)"`
	Until      string `json:"until,omitempty" jsonschema:"ISO 8601 end time (exclusive)"`

	// Optional filters to narrow results.
	TeamName    string `json:"team_name,omitempty" jsonschema:"Filter by team name"`
	TriggerName string `json:"trigger_name,omitempty" jsonschema:"Filter by trigger name"`
	TriggerType string `json:"trigger_type,omitempty" jsonschema:"Filter by trigger type (cron, pr_review, issue)"`
	Repo        string `json:"repo,omitempty" jsonschema:"Filter by repository (e.g. flatout-works/chetter)"`
}

// UsageSummaryOutput is the output for chetter_usage_summary.
type UsageSummaryOutput struct {
	Summary []UsageSummaryRow `json:"summary"`
	Window  UsageWindow       `json:"window"`
}

// UsageWindow describes the effective time window and filters applied.
type UsageWindow struct {
	Since    string  `json:"since,omitempty"`
	Until    string  `json:"until,omitempty"`
	GroupBy  string  `json:"group_by,omitempty"`
	Filters  string  `json:"filters,omitempty"`
	RowCount int     `json:"row_count"`
	TeamIDs  []string `json:"team_ids,omitempty"`
}

// UsageSummaryRow is one aggregated row in the usage summary.
type UsageSummaryRow struct {
	TeamID               string `json:"team_id,omitempty"`
	TeamName             string `json:"team_name,omitempty"`
	TriggerName          string `json:"trigger_name,omitempty"`
	TriggerType          string `json:"trigger_type,omitempty"`
	Repo                 string `json:"repo,omitempty"`
	TaskCount            int64  `json:"task_count"`
	TotalInputTokens     int64  `json:"total_input_tokens"`
	TotalOutputTokens    int64  `json:"total_output_tokens"`
	TotalCacheReadTokens int64  `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int64  `json:"total_cache_write_tokens"`
	TotalReasoningTokens int64  `json:"total_reasoning_tokens"`
	TotalTokens          int64  `json:"total_tokens"`
	CostCents            int64  `json:"cost_cents"`
}

// repoFromGitURL extracts an owner/repo string from a git URL.
// Returns empty string if extraction fails.
func repoFromGitURL(gitURL string) string {
	if gitURL == "" {
		return ""
	}
	// Handle SSH-style: git@github.com:owner/repo.git
	if strings.Contains(gitURL, "@") && strings.Contains(gitURL, ":") {
		if idx := strings.LastIndex(gitURL, ":"); idx >= 0 {
			path := gitURL[idx+1:]
			path = strings.TrimSuffix(path, ".git")
			path = strings.TrimSuffix(path, "/")
			return path
		}
	}
	// Handle HTTPS-style: https://github.com/owner/repo.git
	// Strip protocol
	s := gitURL
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	// Strip host
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	// Only return if it looks like owner/repo (at least one slash)
	if strings.Count(s, "/") >= 1 && !strings.Contains(s, " ") {
		return s
	}
	return ""
}

// GetUsageSummary returns aggregated token usage and cost summaries.
// Admins see all teams; team tokens see only their own teams.
func (s *Service) GetUsageSummary(ctx context.Context, in UsageSummaryInput) (UsageSummaryOutput, error) {
	scope, scoped := auth.GetScope(ctx)

	// Determine effective team IDs.
	var teamIDs []string
	if scoped && !scope.Admin {
		teamIDs = scope.Teams()
	}

	// Parse time window.
	since, until, err := parseUsageWindow(in)
	if err != nil {
		return UsageSummaryOutput{}, fmt.Errorf("parse time window: %w", err)
	}

	// Build and execute the aggregation query.
	rows, err := s.queryUsageSummary(ctx, teamIDs, in.TriggerName, in.TriggerType, in.Repo, in.TeamName, in.GroupBy, since, until)
	if err != nil {
		return UsageSummaryOutput{}, fmt.Errorf("query usage summary: %w", err)
	}

	// Resolve team names for display.
	teamNames := make(map[string]string)
	if len(rows) > 0 {
		allTeams, err := s.repo.ListTeams(ctx)
		if err == nil {
			for _, t := range allTeams {
				teamNames[t.ID] = t.Name
			}
		}
	}

	// Build output rows, extracting repo from git_url.
	out := make([]UsageSummaryRow, 0, len(rows))
	for _, r := range rows {
		row := UsageSummaryRow{
			TeamID:               r.TeamID,
			TeamName:             teamNames[r.TeamID],
			TriggerName:          r.TriggerName,
			TriggerType:          r.TriggerType,
			Repo:                 repoFromGitURL(r.GitURL),
			TaskCount:            r.TaskCount,
			TotalInputTokens:     r.TotalInputTokens,
			TotalOutputTokens:    r.TotalOutputTokens,
			TotalCacheReadTokens: r.TotalCacheReadTokens,
			TotalCacheWriteTokens: r.TotalCacheWriteTokens,
			TotalReasoningTokens: r.TotalReasoningTokens,
			TotalTokens:          r.TotalInputTokens + r.TotalOutputTokens + r.TotalCacheReadTokens + r.TotalCacheWriteTokens + r.TotalReasoningTokens,
			CostCents:            r.CostCents,
		}
		out = append(out, row)
	}

	window := UsageWindow{
		RowCount: len(out),
		GroupBy:  in.GroupBy,
		TeamIDs:  teamIDs,
	}
	if !since.IsZero() {
		window.Since = since.Format(time.RFC3339)
	}
	if !until.IsZero() {
		window.Until = until.Format(time.RFC3339)
	}
	var filters []string
	if in.TriggerName != "" {
		filters = append(filters, "trigger_name="+in.TriggerName)
	}
	if in.TriggerType != "" {
		filters = append(filters, "trigger_type="+in.TriggerType)
	}
	if in.Repo != "" {
		filters = append(filters, "repo="+in.Repo)
	}
	if in.TeamName != "" {
		filters = append(filters, "team="+in.TeamName)
	}
	window.Filters = strings.Join(filters, ", ")

	return UsageSummaryOutput{Summary: out, Window: window}, nil
}

// usageSummaryRow is an internal row from the aggregation query.
type usageSummaryRow struct {
	TeamID               string
	TriggerName          string
	TriggerType          string
	GitURL               string
	TaskCount            int64
	TotalInputTokens     int64
	TotalOutputTokens    int64
	TotalCacheReadTokens int64
	TotalCacheWriteTokens int64
	TotalReasoningTokens int64
	CostCents            int64
}

// queryUsageSummary executes the aggregation query.
func (s *Service) queryUsageSummary(
	ctx context.Context,
	teamIDs []string,
	triggerName, triggerType, repo, teamName, groupBy string,
	since, until time.Time,
) ([]usageSummaryRow, error) {
	// Build SELECT expressions based on groupBy.
	selectExprs := []string{
		"COALESCE(t.team_id, '') AS team_id",
		"COALESCE(t.trigger_name, '') AS trigger_name",
		"COALESCE(t.trigger_type, '') AS trigger_type",
		"COALESCE(t.git_url, '') AS git_url",
		"COUNT(*) AS task_count",
		"SUM(t.total_input_tokens) AS total_input_tokens",
		"SUM(t.total_output_tokens) AS total_output_tokens",
		"SUM(t.total_cache_read_tokens) AS total_cache_read_tokens",
		"SUM(t.total_cache_write_tokens) AS total_cache_write_tokens",
		"SUM(t.total_reasoning_tokens) AS total_reasoning_tokens",
		"SUM(t.cost_cents) AS cost_cents",
	}

	groupExprs := []string{"t.team_id", "t.trigger_name", "t.trigger_type", "t.git_url"}

	var conditions []string
	var args []any

	// Time window.
	if !since.IsZero() {
		conditions = append(conditions, "t.created_at >= ?")
		args = append(args, since)
	}
	if !until.IsZero() {
		conditions = append(conditions, "t.created_at < ?")
		args = append(args, until)
	}

	// Team scoping.
	if len(teamIDs) > 0 {
		placeholders := make([]string, len(teamIDs))
		for i, id := range teamIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conditions = append(conditions, "t.team_id IN ("+strings.Join(placeholders, ",")+")")
	}

	// Team name filter (requires lookup).
	if teamName != "" {
		team, err := s.repo.GetTeamByName(ctx, teamName)
		if err != nil {
			return nil, fmt.Errorf("lookup team %q: %w", teamName, err)
		}
		conditions = append(conditions, "t.team_id = ?")
		args = append(args, team.ID)
	}

	// Optional filters.
	if triggerName != "" {
		conditions = append(conditions, "t.trigger_name = ?")
		args = append(args, triggerName)
	}
	if triggerType != "" {
		conditions = append(conditions, "t.trigger_type = ?")
		args = append(args, triggerType)
	}
	if repo != "" {
		conditions = append(conditions, "LOWER(COALESCE(t.git_url, '')) LIKE ?")
		args = append(args, "%"+strings.ToLower(repo)+"%")
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	groupClause := " GROUP BY " + strings.Join(groupExprs, ", ")

	query := "SELECT " + strings.Join(selectExprs, ", ") +
		" FROM chetter_tasks t" +
		whereClause +
		groupClause +
		" ORDER BY SUM(t.cost_cents) DESC, COUNT(*) DESC"

	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []usageSummaryRow
	for rows.Next() {
		var r usageSummaryRow
		if err := rows.Scan(
			&r.TeamID,
			&r.TriggerName,
			&r.TriggerType,
			&r.GitURL,
			&r.TaskCount,
			&r.TotalInputTokens,
			&r.TotalOutputTokens,
			&r.TotalCacheReadTokens,
			&r.TotalCacheWriteTokens,
			&r.TotalReasoningTokens,
			&r.CostCents,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// parseUsageWindow parses the time window from the input.
func parseUsageWindow(in UsageSummaryInput) (since, until time.Time, err error) {
	now := time.Now().UTC()

	// Parse explicit Since/Until strings.
	if in.Since != "" {
		since, err = time.Parse(time.RFC3339, in.Since)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid since time %q: %w", in.Since, err)
		}
	}
	if in.Until != "" {
		until, err = time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid until time %q: %w", in.Until, err)
		}
	}

	// SinceHours overrides explicit Since if set.
	if in.SinceHours > 0 {
		since = now.Add(-time.Duration(in.SinceHours) * time.Hour)
	}

	// Default: last 30 days if no window specified.
	if since.IsZero() && until.IsZero() {
		since = now.Add(-30 * 24 * time.Hour)
	}

	return since, until, nil
}
