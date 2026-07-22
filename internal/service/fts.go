package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
)

// sanitizeFTS escapes a search term so it can be safely interpolated into a
// SQL string literal inside FTS_MATCH_WORD. TiDB's FTS_MATCH_WORD does not
// accept parameterized bindings, so we must use string interpolation with
// careful escaping.
func sanitizeFTS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// intersectStrings returns the intersection of two string slices.
func intersectStrings(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, v := range b {
		set[v] = true
	}
	out := make([]string, 0, len(a))
	for _, v := range a {
		if set[v] {
			out = append(out, v)
		}
	}
	return out
}

// repoMatchClause builds a SQL fragment that matches git_url against any of
// the provided repo patterns (case-insensitive LIKE).
func repoMatchClause(repos []string) (string, []any) {
	if len(repos) == 0 {
		return "", nil
	}
	conditions := make([]string, len(repos))
	args := make([]any, 0, len(repos))
	for i, r := range repos {
		conditions[i] = "LOWER(COALESCE(git_url, '')) LIKE ?"
		args = append(args, "%"+strings.ToLower(r)+"%")
	}
	return " AND (" + strings.Join(conditions, " OR ") + ")", args
}

// teamInClause builds a SQL fragment that filters team_id by a set of IDs.
func teamInClause(teamIDs []string) (string, []any) {
	if len(teamIDs) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(teamIDs))
	args := make([]any, 0, len(teamIDs))
	for i, id := range teamIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	return " AND team_id IN (" + strings.Join(placeholders, ",") + ")", args
}

// listTasksRaw queries tasks with team and repo filtering applied before
// LIMIT/OFFSET, avoiding the client-side pagination bug.
func (s *Service) listTasksRaw(ctx context.Context, teamIDs, repos []string, status string, limit, offset int32) ([]repository.ChetterTask, error) {
	teamClause, teamArgs := teamInClause(teamIDs)
	repoClause, repoArgs := repoMatchClause(repos)
	query := `SELECT id FROM chetter_tasks WHERE (? = '' OR status = ?)` + teamClause + repoClause + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args := append([]any{status, status}, teamArgs...)
	args = append(args, repoArgs...)
	args = append(args, limit, offset)
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks raw: %w", err)
	}
	defer rows.Close()
	var tasks []repository.ChetterTask
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		task, err := s.repo.GetTaskByID(ctx, id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// searchTasksRaw queries tasks with team, repo, and FTS search filtering.
func (s *Service) searchTasksRaw(ctx context.Context, teamIDs, repos []string, status, search string, limit, offset int32) ([]repository.ChetterTask, error) {
	safe := sanitizeFTS(search)
	if safe == "" {
		return nil, nil
	}
	teamClause, teamArgs := teamInClause(teamIDs)
	repoClause, repoArgs := repoMatchClause(repos)
	var query string
	var args []any
	if s.dialect == store.DialectMySQL {
		query = `SELECT id FROM chetter_tasks WHERE (? = '' OR status = ?)` + teamClause + repoClause + ` AND MATCH(search_text) AGAINST(? IN BOOLEAN MODE) ORDER BY created_at DESC LIMIT ? OFFSET ?`
		args = append([]any{status, status}, teamArgs...)
		args = append(args, repoArgs...)
		args = append(args, search, limit, offset)
	} else {
		query = fmt.Sprintf(`SELECT id FROM chetter_tasks WHERE (? = '' OR status = ?)%s%s AND FTS_MATCH_WORD(search_text, '%s') ORDER BY created_at DESC LIMIT ? OFFSET ?`, teamClause, repoClause, safe)
		args = append([]any{status, status}, teamArgs...)
		args = append(args, repoArgs...)
		args = append(args, limit, offset)
	}
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		slog.DebugContext(ctx, "raw FTS search tasks failed, falling back", "err", err)
		// Fall back to non-FTS raw query
		return s.listTasksRaw(ctx, teamIDs, repos, status, limit, offset)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tasks := make([]repository.ChetterTask, 0, len(ids))
	for _, id := range ids {
		t, err := s.repo.GetTaskByID(ctx, id)
		if err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// listAgentSessionsRaw queries sessions with team and repo filtering applied
// before LIMIT/OFFSET.
func (s *Service) listAgentSessionsRaw(ctx context.Context, teamIDs, repos []string, status string, limit, offset int32) ([]repository.ChetterAgentSession, error) {
	teamClause, teamArgs := teamInClause(teamIDs)
	repoClause, repoArgs := repoMatchClause(repos)
	query := `SELECT id, task_id, sequence, team_id, status, resume_mode, pinned_runner_id, pinned_runner_name, checkpoint_id, workspace_path, container_name, harness_session_id, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, mcp_endpoints, env, commit_author_name, commit_author_email, git_identity_id, created_at, updated_at, paused_at, expires_at, pause_reason, summary, error, started_at, ended_at, search_text FROM chetter_agent_sessions WHERE (? = '' OR status = ?)` + teamClause + repoClause + ` ORDER BY updated_at DESC LIMIT ? OFFSET ?`
	args := append([]any{status, status}, teamArgs...)
	args = append(args, repoArgs...)
	args = append(args, limit, offset)
	return s.scanAgentSessions(ctx, query, args)
}

// searchAgentSessionsRaw queries sessions with team, repo, and FTS search filtering.
func (s *Service) searchAgentSessionsRaw(ctx context.Context, teamIDs, repos []string, status, search string, limit, offset int32) ([]repository.ChetterAgentSession, error) {
	safe := sanitizeFTS(search)
	if safe == "" {
		return nil, nil
	}
	teamClause, teamArgs := teamInClause(teamIDs)
	repoClause, repoArgs := repoMatchClause(repos)
	var query string
	var args []any
	if s.dialect == store.DialectMySQL {
		query = `SELECT id FROM chetter_agent_sessions WHERE (? = '' OR status = ?)` + teamClause + repoClause + ` AND MATCH(search_text) AGAINST(? IN BOOLEAN MODE) ORDER BY updated_at DESC LIMIT ? OFFSET ?`
		args = append([]any{status, status}, teamArgs...)
		args = append(args, repoArgs...)
		args = append(args, search, limit, offset)
	} else {
		query = fmt.Sprintf(`SELECT id FROM chetter_agent_sessions WHERE (? = '' OR status = ?)%s%s AND FTS_MATCH_WORD(search_text, '%s') ORDER BY updated_at DESC LIMIT ? OFFSET ?`, teamClause, repoClause, safe)
		args = append([]any{status, status}, teamArgs...)
		args = append(args, repoArgs...)
		args = append(args, limit, offset)
	}
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		slog.DebugContext(ctx, "raw FTS search sessions failed, falling back", "err", err)
		return s.listAgentSessionsRaw(ctx, teamIDs, repos, status, limit, offset)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sessions := make([]repository.ChetterAgentSession, 0, len(ids))
	for _, id := range ids {
		s, err := s.repo.GetAgentSessionByID(ctx, id)
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// scanAgentSessions executes a query and scans the rows into ChetterAgentSession slices.
func (s *Service) scanAgentSessions(ctx context.Context, query string, args []any) ([]repository.ChetterAgentSession, error) {
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions raw: %w", err)
	}
	defer rows.Close()
	var items []repository.ChetterAgentSession
	for rows.Next() {
		var i repository.ChetterAgentSession
		if err := rows.Scan(
			&i.ID, &i.TaskID, &i.Sequence, &i.TeamID, &i.Status, &i.ResumeMode, &i.PinnedRunnerID, &i.PinnedRunnerName,
			&i.CheckpointID, &i.WorkspacePath, &i.ContainerName, &i.HarnessSessionID,
			&i.GitUrl, &i.GitRef, &i.AgentImage, &i.Agent, &i.ProviderID, &i.ModelID, &i.VariantID,
			&i.Harness, &i.Skills, &i.McpEndpoints, &i.Env, &i.CommitAuthorName, &i.CommitAuthorEmail, &i.GitIdentityID,
			&i.CreatedAt, &i.UpdatedAt, &i.PausedAt, &i.ExpiresAt, &i.PauseReason, &i.Summary, &i.Error, &i.StartedAt, &i.EndedAt, &i.SearchText,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// searchTasksFTS attempts a real full-text search. On TiDB it uses
// FTS_MATCH_WORD; on MySQL it uses MATCH ... AGAINST ... IN BOOLEAN MODE.
// If the FTS index is unavailable it falls back to the sqlc LIKE query.
func (s *Service) searchTasksFTS(ctx context.Context, teamFilter sql.NullString, status, search string, limit, offset int32) ([]repository.ChetterTask, error) {
	safe := sanitizeFTS(search)
	if safe == "" {
		return nil, nil
	}
	var query string
	var args []any
	if s.dialect == store.DialectMySQL {
		query = `
			SELECT id FROM chetter_tasks
			WHERE (? = '' OR team_id = ?)
			  AND (? = '' OR status = ?)
			  AND MATCH(search_text) AGAINST(? IN BOOLEAN MODE)
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?`
		args = []any{teamFilter, teamFilter, status, status, search, limit, offset}
	} else {
		query = fmt.Sprintf(`
			SELECT id FROM chetter_tasks
			WHERE (? = '' OR team_id = ?)
			  AND (? = '' OR status = ?)
			  AND FTS_MATCH_WORD(search_text, '%s')
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?`, safe)
		args = []any{teamFilter, teamFilter, status, status, limit, offset}
	}
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		slog.DebugContext(ctx, "FTS search tasks failed, falling back to LIKE", "err", err, "dialect", s.dialect)
		return s.repo.SearchTasks(ctx, repository.SearchTasksParams{
			TeamFilter:   teamFilter,
			StatusFilter: status,
			Search:       search,
			Limit:        limit,
			Offset:       offset,
		})
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tasks := make([]repository.ChetterTask, 0, len(ids))
	for _, id := range ids {
		t, err := s.repo.GetTaskByID(ctx, id)
		if err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// searchAgentSessionsFTS attempts a real full-text search. On TiDB it uses
// FTS_MATCH_WORD; on MySQL it uses MATCH ... AGAINST ... IN BOOLEAN MODE.
func (s *Service) searchAgentSessionsFTS(ctx context.Context, teamFilter sql.NullString, status, search string, limit, offset int32) ([]repository.ChetterAgentSession, error) {
	safe := sanitizeFTS(search)
	if safe == "" {
		return nil, nil
	}
	var query string
	var args []any
	if s.dialect == store.DialectMySQL {
		query = `
			SELECT id FROM chetter_agent_sessions
			WHERE (? = '' OR COALESCE(team_id, '') = ?)
			  AND (? = '' OR status = ?)
			  AND MATCH(search_text) AGAINST(? IN BOOLEAN MODE)
			ORDER BY updated_at DESC
			LIMIT ? OFFSET ?`
		args = []any{teamFilter, teamFilter, status, status, search, limit, offset}
	} else {
		query = fmt.Sprintf(`
			SELECT id FROM chetter_agent_sessions
			WHERE (? = '' OR COALESCE(team_id, '') = ?)
			  AND (? = '' OR status = ?)
			  AND FTS_MATCH_WORD(search_text, '%s')
			ORDER BY updated_at DESC
			LIMIT ? OFFSET ?`, safe)
		args = []any{teamFilter, teamFilter, status, status, limit, offset}
	}
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		slog.DebugContext(ctx, "FTS search sessions failed, falling back to LIKE", "err", err, "dialect", s.dialect)
		return s.repo.SearchAgentSessions(ctx, repository.SearchAgentSessionsParams{
			TeamFilter:   teamFilter,
			StatusFilter: status,
			Search:       search,
			Limit:        limit,
			Offset:       offset,
		})
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sessions := make([]repository.ChetterAgentSession, 0, len(ids))
	for _, id := range ids {
		sess, err := s.repo.GetAgentSessionByID(ctx, id)
		if err != nil {
			continue
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

// listAuditLogRaw queries the audit log with a raw SQL query that supports
// excluding specific event types. This is used by the web UI toggle filters.
func (s *Service) listAuditLogRaw(ctx context.Context, filter AuditEventFilterInput, limit, offset int32, sinceTime sql.NullTime) ([]repository.ListAuditLogRow, error) {
	placeholders := make([]string, len(filter.ExcludeTypes))
	for i := range filter.ExcludeTypes {
		placeholders[i] = "?"
	}
	excludeClause := fmt.Sprintf(" AND event_type NOT IN (%s)", strings.Join(placeholders, ","))

	query := `
		SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload, token_id, token_name
		FROM chetter_audit_log
		WHERE (event_type = ? OR ? = '')
		  AND (source_type = ? OR ? = '')
		  AND (source_id = ? OR ? = '')
		  AND (target_type = ? OR ? = '')
		  AND (target_id = ? OR ? = '')
		  AND (repo = ? OR ? = '')
		  AND (created_at >= ? OR ? IS NULL)` + excludeClause + `
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`
	if s.dialect == store.DialectPostgres {
		query = strings.Replace(query, "created_at >= ? OR ? IS NULL", "created_at >= COALESCE(?, '-infinity'::timestamptz)", 1)
		query = strings.Replace(query, "LIMIT ? OFFSET ?", "LIMIT "+strconv.Itoa(int(limit))+" OFFSET "+strconv.Itoa(int(offset)), 1)
	}

	args := []any{
		filter.EventType, filter.EventType,
		filter.SourceType, filter.SourceType,
		filter.SourceID, filter.SourceID,
		filter.TargetType, filter.TargetType,
		filter.TargetID, filter.TargetID,
		filter.Repo, filter.Repo,
	}
	if s.dialect == store.DialectPostgres {
		args = append(args, sinceTime)
	} else {
		args = append(args, sinceTime, sinceTime)
	}
	for _, t := range filter.ExcludeTypes {
		args = append(args, t)
	}
	if s.dialect != store.DialectPostgres {
		args = append(args, limit, offset)
	}

	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit log raw: %w", err)
	}
	defer rows.Close()
	var items []repository.ListAuditLogRow
	for rows.Next() {
		var i repository.ListAuditLogRow
		if err := rows.Scan(
			&i.ID,
			&i.EventType,
			&i.CreatedAt,
			&i.SourceType,
			&i.SourceID,
			&i.TargetType,
			&i.TargetID,
			&i.Repo,
			&i.GithubEvent,
			&i.GithubAction,
			&i.GithubDeliveryID,
			&i.ParentEventID,
			&i.Detail,
			&i.Payload,
			&i.TokenID,
			&i.TokenName,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// searchAuditLogFTS attempts a real full-text search. On TiDB it uses
// FTS_MATCH_WORD; on MySQL it uses MATCH ... AGAINST ... IN BOOLEAN MODE.
func (s *Service) searchAuditLogFTS(ctx context.Context, filter AuditEventFilterInput, limit, offset int32, sinceTime sql.NullTime) ([]repository.ListAuditLogRow, error) {
	safe := sanitizeFTS(filter.Search)
	if safe == "" {
		return nil, nil
	}
	excludeClause := ""
	if len(filter.ExcludeTypes) > 0 {
		placeholders := make([]string, len(filter.ExcludeTypes))
		for i := range filter.ExcludeTypes {
			placeholders[i] = "?"
		}
		excludeClause = fmt.Sprintf(" AND event_type NOT IN (%s)", strings.Join(placeholders, ","))
	}
	var query string
	var args []any
	if s.dialect == store.DialectMySQL {
		query = `
			SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload, token_id, token_name
			FROM chetter_audit_log
			WHERE (event_type = ? OR ? = '')
			  AND (source_type <=> ? OR ? = '')
			  AND (source_id <=> ? OR ? = '')
			  AND (target_type <=> ? OR ? = '')
			  AND (target_id <=> ? OR ? = '')
			  AND (repo <=> ? OR ? = '')
			  AND (created_at >= ? OR ? IS NULL)` + excludeClause + `
			  AND MATCH(search_text) AGAINST(? IN BOOLEAN MODE)
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?`
		args = []any{
			filter.EventType, filter.EventType,
			nullString(filter.SourceType), filter.SourceType,
			nullString(filter.SourceID), filter.SourceID,
			nullString(filter.TargetType), filter.TargetType,
			nullString(filter.TargetID), filter.TargetID,
			nullString(filter.Repo), filter.Repo,
			sinceTime, sinceTime,
		}
		for _, t := range filter.ExcludeTypes {
			args = append(args, t)
		}
		args = append(args, filter.Search, limit, offset)
	} else {
		query = fmt.Sprintf(`
			SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload, token_id, token_name
			FROM chetter_audit_log
			WHERE (event_type = ? OR ? = '')
			  AND (source_type <=> ? OR ? = '')
			  AND (source_id <=> ? OR ? = '')
			  AND (target_type <=> ? OR ? = '')
			  AND (target_id <=> ? OR ? = '')
			  AND (repo <=> ? OR ? = '')
			  AND (created_at >= ? OR ? IS NULL)`+excludeClause+`
			  AND FTS_MATCH_WORD(search_text, '%s')
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?`, safe)
		args = []any{
			filter.EventType, filter.EventType,
			nullString(filter.SourceType), filter.SourceType,
			nullString(filter.SourceID), filter.SourceID,
			nullString(filter.TargetType), filter.TargetType,
			nullString(filter.TargetID), filter.TargetID,
			nullString(filter.Repo), filter.Repo,
			sinceTime, sinceTime,
		}
		for _, t := range filter.ExcludeTypes {
			args = append(args, t)
		}
		args = append(args, limit, offset)
	}
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		slog.DebugContext(ctx, "FTS search audit log failed, falling back to LIKE", "err", err, "dialect", s.dialect)
		searchRows, ferr := s.repo.SearchAuditLog(ctx, repository.SearchAuditLogParams{
			EventType:  filter.EventType,
			Column2:    filter.EventType,
			SourceType: nullString(filter.SourceType),
			Column4:    filter.SourceType,
			SourceID:   nullString(filter.SourceID),
			Column6:    filter.SourceID,
			TargetType: nullString(filter.TargetType),
			Column8:    filter.TargetType,
			TargetID:   nullString(filter.TargetID),
			Column10:   filter.TargetID,
			Repo:       nullString(filter.Repo),
			Column12:   filter.Repo,
			CreatedAt:  sinceTime.Time,
			Column14:   sinceTime,
			Search:     filter.Search,
			Limit:      limit,
			Offset:     offset,
		})
		if ferr != nil {
			return nil, ferr
		}
		result := make([]repository.ListAuditLogRow, 0, len(searchRows))
		excludeSet := make(map[string]bool, len(filter.ExcludeTypes))
		for _, t := range filter.ExcludeTypes {
			excludeSet[t] = true
		}
		for _, r := range searchRows {
			if excludeSet[r.EventType] {
				continue
			}
			result = append(result, repository.ListAuditLogRow(r))
		}
		return result, nil
	}
	defer rows.Close()
	var items []repository.ListAuditLogRow
	for rows.Next() {
		var i repository.ListAuditLogRow
		if err := rows.Scan(
			&i.ID,
			&i.EventType,
			&i.CreatedAt,
			&i.SourceType,
			&i.SourceID,
			&i.TargetType,
			&i.TargetID,
			&i.Repo,
			&i.GithubEvent,
			&i.GithubAction,
			&i.GithubDeliveryID,
			&i.ParentEventID,
			&i.Detail,
			&i.Payload,
			&i.TokenID,
			&i.TokenName,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// searchTaskArtifactsFTS attempts a real full-text search. On TiDB it uses
// FTS_MATCH_WORD; on MySQL it uses MATCH ... AGAINST ... IN BOOLEAN MODE.
func (s *Service) searchTaskArtifactsFTS(ctx context.Context, filter TaskArtifactFilterInput, limit, offset int32) ([]repository.ListTaskArtifactsRow, error) {
	safe := sanitizeFTS(filter.Search)
	if safe == "" {
		return nil, nil
	}
	var query string
	var args []any
	if s.dialect == store.DialectMySQL {
		query = `
			SELECT id, task_id, agent_session_id, user_prompt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
			FROM chetter_task_artifacts
			WHERE (task_id = ? OR ? = '')
			  AND (agent_session_id <=> ? OR ? = '')
			  AND (artifact_type = ? OR ? = '')
			  AND (repo = ? OR ? = '')
			  AND MATCH(search_text) AGAINST(? IN BOOLEAN MODE)
			ORDER BY discovered_at DESC
			LIMIT ? OFFSET ?`
		args = []any{
			filter.TaskID, filter.TaskID,
			nullString(filter.AgentSessionID), filter.AgentSessionID,
			filter.ArtifactType, filter.ArtifactType,
			filter.Repo, filter.Repo,
			filter.Search,
			limit, offset,
		}
	} else {
		query = fmt.Sprintf(`
			SELECT id, task_id, agent_session_id, user_prompt_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
			FROM chetter_task_artifacts
			WHERE (task_id = ? OR ? = '')
			  AND (agent_session_id <=> ? OR ? = '')
			  AND (artifact_type = ? OR ? = '')
			  AND (repo = ? OR ? = '')
			  AND FTS_MATCH_WORD(search_text, '%s')
			ORDER BY discovered_at DESC
			LIMIT ? OFFSET ?`, safe)
		args = []any{
			filter.TaskID, filter.TaskID,
			nullString(filter.AgentSessionID), filter.AgentSessionID,
			filter.ArtifactType, filter.ArtifactType,
			filter.Repo, filter.Repo,
			limit, offset,
		}
	}
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		slog.DebugContext(ctx, "FTS search artifacts failed, falling back to LIKE", "err", err, "dialect", s.dialect)
		searchRows, err := s.repo.SearchTaskArtifacts(ctx, repository.SearchTaskArtifactsParams{
			TaskID:         filter.TaskID,
			Column2:        filter.TaskID,
			AgentSessionID: nullString(filter.AgentSessionID),
			Column4:        filter.AgentSessionID,
			ArtifactType:   filter.ArtifactType,
			Column6:        filter.ArtifactType,
			Repo:           filter.Repo,
			Column8:        filter.Repo,
			Search:         filter.Search,
			Limit:          limit,
			Offset:         offset,
		})
		if err != nil {
			return nil, err
		}
		result := make([]repository.ListTaskArtifactsRow, len(searchRows))
		for i, r := range searchRows {
			result[i] = repository.ListTaskArtifactsRow(r)
		}
		return result, nil
	}
	defer rows.Close()
	var items []repository.ListTaskArtifactsRow
	for rows.Next() {
		var i repository.ListTaskArtifactsRow
		if err := rows.Scan(
			&i.ID,
			&i.TaskID,
			&i.AgentSessionID,
			&i.UserPromptID,
			&i.ArtifactType,
			&i.Repo,
			&i.Number,
			&i.Url,
			&i.Ref,
			&i.Sha,
			&i.CreatedAt,
			&i.DiscoveredAt,
			&i.DiscoverySource,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
