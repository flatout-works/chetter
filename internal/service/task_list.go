package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/flatout-works/chetter/internal/repository"
)

const taskListAgentSessionColumns = `
	session.id, session.team_id, session.status, session.resume_mode,
	session.pinned_runner_id, session.pinned_runner_name, session.checkpoint_id,
	session.workspace_path, session.container_name, session.harness_session_id,
	session.git_url, session.git_ref, session.agent_image, session.agent,
	session.provider_id, session.model_id, session.variant_id, session.created_at,
	session.updated_at, session.paused_at, session.expires_at, session.pause_reason,
	session.error, session.search_text, session.task_id, session.sequence,
	session.harness, session.mcp_endpoints, session.commit_author_name,
	session.commit_author_email, session.git_identity_id, session.skills, session.env,
	session.summary, session.started_at, session.ended_at`

type sqlRowScanner interface {
	Scan(dest ...any) error
}

// batchTaskDetails loads the same session and execution data that ListTasks
// used to fetch one task at a time. The task list is refreshed frequently, so
// keeping these lookups batched avoids exhausting the database connection pool.
func (s *Service) batchTaskDetails(ctx context.Context, tasks []repository.ChetterTask) (map[string]repository.ChetterAgentSession, map[string]sql.NullTime, error) {
	if len(tasks) == 0 {
		return map[string]repository.ChetterAgentSession{}, map[string]sql.NullTime{}, nil
	}
	ids := make([]string, len(tasks))
	args := make([]any, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
		args[i] = task.ID
	}

	sessions, err := s.batchTaskSessions(ctx, ids, args)
	if err != nil {
		return nil, nil, err
	}

	startedAt, err := s.batchTaskStartedAt(ctx, ids, args)
	if err != nil {
		// Duration is supplementary list data. Preserve the list response if an
		// older or partially migrated database cannot provide it.
		startedAt = make(map[string]sql.NullTime)
	}
	return sessions, startedAt, nil
}

func (s *Service) batchTaskSessions(ctx context.Context, ids []string, args []any) (map[string]repository.ChetterAgentSession, error) {
	db := s.repo.DB()
	if db == nil {
		return nil, fmt.Errorf("repository database is unavailable")
	}
	placeholders := strings.Join(sqlPlaceholders(s.dialect, len(ids)), ",")
	query := `SELECT ` + taskListAgentSessionColumns + `
FROM chetter_agent_sessions session
JOIN (
	SELECT task_id, MAX(sequence) AS sequence
	FROM chetter_agent_sessions
	WHERE task_id IN (` + placeholders + `)
	GROUP BY task_id
) latest ON latest.task_id = session.task_id AND latest.sequence = session.sequence`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch task sessions: %w", err)
	}
	defer rows.Close()

	sessions := make(map[string]repository.ChetterAgentSession, len(ids))
	for rows.Next() {
		session, err := scanTaskListAgentSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan batched task session: %w", err)
		}
		sessions[session.TaskID] = session
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read batched task sessions: %w", err)
	}
	return sessions, nil
}

func (s *Service) batchTaskStartedAt(ctx context.Context, ids []string, args []any) (map[string]sql.NullTime, error) {
	db := s.repo.DB()
	if db == nil {
		return nil, fmt.Errorf("repository database is unavailable")
	}
	placeholders := strings.Join(sqlPlaceholders(s.dialect, len(ids)), ",")
	query := `SELECT prompt.task_id, attempt.started_at
FROM chetter_execution_attempts attempt
JOIN chetter_user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN chetter_agent_sessions session ON session.id = prompt.agent_session_id
WHERE prompt.task_id IN (` + placeholders + `)
  AND NOT EXISTS (
    SELECT 1
    FROM chetter_user_prompts newer_prompt
    JOIN chetter_agent_sessions newer_session ON newer_session.id = newer_prompt.agent_session_id
    WHERE newer_prompt.task_id = prompt.task_id
      AND (newer_session.sequence > session.sequence
        OR (newer_session.sequence = session.sequence AND newer_prompt.sequence > prompt.sequence))
  )
ORDER BY prompt.task_id, attempt.sequence DESC, attempt.created_at DESC`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch task start times: %w", err)
	}
	defer rows.Close()

	startedAt := make(map[string]sql.NullTime, len(ids))
	for rows.Next() {
		var taskID string
		var value sql.NullTime
		if err := rows.Scan(&taskID, &value); err != nil {
			return nil, fmt.Errorf("scan batched task start time: %w", err)
		}
		if _, exists := startedAt[taskID]; !exists {
			startedAt[taskID] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read batched task start times: %w", err)
	}
	return startedAt, nil
}

func scanTaskListAgentSession(row sqlRowScanner) (repository.ChetterAgentSession, error) {
	var session repository.ChetterAgentSession
	err := row.Scan(
		&session.ID,
		&session.TeamID,
		&session.Status,
		&session.ResumeMode,
		&session.PinnedRunnerID,
		&session.PinnedRunnerName,
		&session.CheckpointID,
		&session.WorkspacePath,
		&session.ContainerName,
		&session.HarnessSessionID,
		&session.GitUrl,
		&session.GitRef,
		&session.AgentImage,
		&session.Agent,
		&session.ProviderID,
		&session.ModelID,
		&session.VariantID,
		&session.CreatedAt,
		&session.UpdatedAt,
		&session.PausedAt,
		&session.ExpiresAt,
		&session.PauseReason,
		&session.Error,
		&session.SearchText,
		&session.TaskID,
		&session.Sequence,
		&session.Harness,
		&session.McpEndpoints,
		&session.CommitAuthorName,
		&session.CommitAuthorEmail,
		&session.GitIdentityID,
		&session.Skills,
		&session.Env,
		&session.Summary,
		&session.StartedAt,
		&session.EndedAt,
	)
	return session, err
}
