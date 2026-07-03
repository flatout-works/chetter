package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
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

// searchAuditLogFTS attempts a real full-text search. On TiDB it uses
// FTS_MATCH_WORD; on MySQL it uses MATCH ... AGAINST ... IN BOOLEAN MODE.
func (s *Service) searchAuditLogFTS(ctx context.Context, filter AuditEventFilterInput, limit, offset int32, sinceTime sql.NullTime) ([]repository.ListAuditLogRow, error) {
	safe := sanitizeFTS(filter.Search)
	if safe == "" {
		return nil, nil
	}
	var query string
	var args []any
	if s.dialect == store.DialectMySQL {
		query = `
			SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload
			FROM chetter_audit_log
			WHERE (event_type = ? OR ? = '')
			  AND (source_type <=> ? OR ? = '')
			  AND (source_id <=> ? OR ? = '')
			  AND (target_type <=> ? OR ? = '')
			  AND (target_id <=> ? OR ? = '')
			  AND (repo <=> ? OR ? = '')
			  AND (created_at >= ? OR ? IS NULL)
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
			filter.Search,
			limit, offset,
		}
	} else {
		query = fmt.Sprintf(`
			SELECT id, event_type, created_at, source_type, source_id, target_type, target_id, repo, github_event, github_action, github_delivery_id, parent_event_id, detail, payload
			FROM chetter_audit_log
			WHERE (event_type = ? OR ? = '')
			  AND (source_type <=> ? OR ? = '')
			  AND (source_id <=> ? OR ? = '')
			  AND (target_type <=> ? OR ? = '')
			  AND (target_id <=> ? OR ? = '')
			  AND (repo <=> ? OR ? = '')
			  AND (created_at >= ? OR ? IS NULL)
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
			limit, offset,
		}
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
		result := make([]repository.ListAuditLogRow, len(searchRows))
		for i, r := range searchRows {
			result[i] = repository.ListAuditLogRow(r)
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
			SELECT id, task_id, agent_session_id, session_run_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
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
			SELECT id, task_id, agent_session_id, session_run_id, artifact_type, repo, number, url, ref, sha, created_at, discovered_at, discovery_source
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
			&i.SessionRunID,
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
