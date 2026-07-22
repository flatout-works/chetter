-- name: InsertAgentSession :exec
INSERT INTO chetter_agent_sessions
    (id, task_id, sequence, team_id, status, resume_mode, pause_reason, expires_at, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, search_text, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18);

-- name: GetAgentSessionByID :one
SELECT * FROM chetter_agent_sessions WHERE id = $1;

-- name: ListAgentSessions :many
SELECT * FROM chetter_agent_sessions
WHERE (sqlc.arg(team_filter) = '' OR COALESCE(team_id, '') = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY updated_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchAgentSessions :many
SELECT * FROM chetter_agent_sessions
WHERE (sqlc.arg(team_filter) = '' OR COALESCE(team_id, '') = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY updated_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListAgentSessionsByTeams :many
SELECT * FROM chetter_agent_sessions
WHERE team_id = ANY(sqlc.arg(team_ids)::text[])
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY updated_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchAgentSessionsByTeams :many
SELECT * FROM chetter_agent_sessions
WHERE team_id = ANY(sqlc.arg(team_ids)::text[])
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY updated_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: MarkAgentSessionTerminalByTask :execrows
UPDATE chetter_agent_sessions
SET status = $1,
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    error = $2,
    updated_at = $3
WHERE id = (SELECT agent.id FROM chetter_agent_sessions agent WHERE agent.task_id = $4 ORDER BY agent.sequence DESC LIMIT 1)
  AND status IN ('running', 'resuming');

-- name: GetAgentSessionByTaskID :one
SELECT * FROM chetter_agent_sessions
WHERE task_id = $1
ORDER BY sequence DESC
LIMIT 1;

-- name: PauseAgentSessionByTaskID :execrows
UPDATE chetter_agent_sessions
SET status = $1,
    pinned_runner_id = COALESCE(NULLIF(sqlc.arg(pinned_runner_id), ''), pinned_runner_id),
    checkpoint_id = COALESCE(NULLIF(sqlc.arg(checkpoint_id), ''), checkpoint_id),
    workspace_path = COALESCE(NULLIF(sqlc.arg(workspace_path), ''), workspace_path),
    container_name = COALESCE(NULLIF(sqlc.arg(container_name), ''), container_name),
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    paused_at = $2,
    updated_at = $3
WHERE id = (SELECT agent.id FROM chetter_agent_sessions agent WHERE agent.task_id = $4 ORDER BY agent.sequence DESC LIMIT 1)
  AND status IN ('running', 'resuming');

-- name: MarkAgentSessionResuming :execrows
UPDATE chetter_agent_sessions SET status = $1, updated_at = $2 WHERE id = $3;

-- name: AbandonAgentSession :execrows
UPDATE chetter_agent_sessions
SET status = 'abandoned', error = $1, updated_at = $2
WHERE id = $3 AND status IN ('running', 'resuming');

-- name: IsRunnerAlive :one
SELECT COUNT(*) > 0 FROM chetter_runners
WHERE id = sqlc.arg(runner_id)
  AND status = 'active'
  AND last_seen_at > NOW() - (sqlc.arg(stale_seconds) * INTERVAL '1 second');

-- name: GetPausedSessionByArtifact :one
SELECT s.* FROM chetter_agent_sessions s
JOIN chetter_task_artifacts a ON a.agent_session_id = s.id
WHERE a.repo = sqlc.arg(repo)
  AND a.number = sqlc.arg(number)
  AND a.artifact_type = sqlc.arg(artifact_type)
  AND s.status IN ('paused', 'recoverable', 'paused_waiting_review')
  AND s.resume_mode IN ('gvisor_checkpoint', 'harness_session')
ORDER BY a.discovered_at DESC
LIMIT 1;

-- name: ExpirePausedSessions :execrows
UPDATE chetter_agent_sessions
SET status = 'expired', updated_at = $1
WHERE status IN ('paused', 'recoverable', 'paused_waiting_review')
  AND expires_at IS NOT NULL
  AND expires_at < $2;

-- name: InsertUserPrompt :exec
INSERT INTO chetter_user_prompts
    (id, agent_session_id, task_id, sequence, status, prompt, source_user_prompt_id, required_runner_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetUserPromptByID :one
SELECT * FROM chetter_user_prompts WHERE id = $1;

-- name: GetUserPromptByTaskID :one
SELECT prompt.* FROM chetter_user_prompts prompt
JOIN chetter_agent_sessions session ON session.id = prompt.agent_session_id
WHERE prompt.task_id = $1
ORDER BY session.sequence DESC, prompt.sequence DESC
LIMIT 1;

-- name: AbandonUserPrompt :execrows
UPDATE chetter_user_prompts
SET status = 'failed', error = $1, ended_at = $2, updated_at = $3
WHERE id = $4 AND status IN ('pending', 'claimed', 'running');

-- name: ListUserPromptsBySession :many
SELECT * FROM chetter_user_prompts WHERE agent_session_id = $1 ORDER BY sequence ASC, created_at ASC;

-- name: GetNextAgentSessionSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM chetter_agent_sessions
WHERE task_id = $1;

-- name: GetNextUserPromptSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM chetter_user_prompts
WHERE agent_session_id = $1;

-- name: MarkUserPromptRunningByTask :execrows
UPDATE chetter_user_prompts
SET status = 'running', started_at = COALESCE(started_at, $1), updated_at = $2
WHERE id = (
    SELECT prompt.id FROM chetter_user_prompts prompt
    JOIN chetter_agent_sessions session ON session.id = prompt.agent_session_id
    WHERE prompt.task_id = $3
    ORDER BY session.sequence DESC, prompt.sequence DESC
    LIMIT 1
)
  AND status IN ('pending', 'claimed');

-- name: MarkUserPromptTerminalByTask :execrows
UPDATE chetter_user_prompts
SET status = $1,
    summary = $2,
    error = $3,
    session_export = COALESCE($4, session_export),
    started_at = COALESCE(started_at, $5),
    ended_at = COALESCE($6, ended_at),
    updated_at = $7
WHERE id = (
    SELECT prompt.id FROM chetter_user_prompts prompt
    JOIN chetter_agent_sessions session ON session.id = prompt.agent_session_id
    WHERE prompt.task_id = $8
    ORDER BY session.sequence DESC, prompt.sequence DESC
    LIMIT 1
);

-- name: FailPendingResumeTasksForMissingRunner :execrows
UPDATE chetter_tasks t
SET status = 'error',
    error = 'pinned runner ' || t.required_runner_id || ' is not alive',
    error_category = 'runner_unavailable',
    ended_at = $1,
    updated_at = $2,
    last_event_at = $3
WHERE t.status = 'pending'
  AND t.required_runner_id IS NOT NULL
  AND t.required_runner_id <> ''
  AND NOT EXISTS (
    SELECT 1 FROM chetter_runners r
    WHERE r.id = t.required_runner_id
      AND r.status = 'active'
      AND r.last_seen_at > NOW() - (sqlc.arg(stale_seconds) * INTERVAL '1 second')
  )
  AND EXISTS (
    SELECT 1
    FROM chetter_user_prompts sr
    JOIN chetter_agent_sessions s ON s.id = sr.agent_session_id
    WHERE sr.task_id = t.id AND sr.status = 'pending' AND s.status = 'resuming'
  );

-- name: FailPendingUserPromptsForUnavailableRunner :execrows
UPDATE chetter_user_prompts sr
SET status = 'failed',
    error = t.error,
    ended_at = COALESCE(sr.ended_at, $1),
    updated_at = $2
FROM chetter_tasks t
WHERE t.id = sr.task_id
  AND sr.status = 'pending'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable';

-- name: MarkResumingSessionsFailedForUnavailableRunner :execrows
UPDATE chetter_agent_sessions s
SET status = 'error', error = COALESCE(sr.error, t.error), updated_at = $1
FROM chetter_user_prompts sr
JOIN chetter_tasks t ON t.id = sr.task_id
WHERE sr.agent_session_id = s.id
  AND s.status = 'resuming'
  AND sr.status = 'failed'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable';

-- name: InsertAgentSessionCheckpoint :exec
INSERT INTO chetter_agent_session_checkpoints
    (id, agent_session_id, user_prompt_id, runner_id, checkpoint_path, workspace_path, container_name, runsc_version, agent_image, size_bytes, status, error, created_at, updated_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: GetLatestAgentSessionCheckpoint :one
SELECT * FROM chetter_agent_session_checkpoints
WHERE agent_session_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: GetLatestAgentSessionCheckpointByTaskID :one
SELECT chk.* FROM chetter_agent_session_checkpoints chk
JOIN chetter_user_prompts r ON r.agent_session_id = chk.agent_session_id
WHERE r.task_id = $1
ORDER BY chk.created_at DESC
LIMIT 1;

-- name: ReapStaleUserPrompts :execrows
UPDATE chetter_user_prompts sr
SET status = CASE
        WHEN t.status = 'done' THEN 'completed'
        WHEN t.status = 'cancelled' THEN 'cancelled'
        ELSE 'failed'
    END,
    error = COALESCE(NULLIF(sr.error, ''), t.error, sr.error),
    ended_at = COALESCE(sr.ended_at, t.ended_at, NOW()),
    updated_at = NOW()
FROM chetter_tasks t
WHERE t.id = sr.task_id
  AND sr.status = 'running'
  AND t.status IN ('done', 'error', 'cancelled');

-- name: ReapStaleSessionsForTerminalRuns :execrows
UPDATE chetter_agent_sessions s
SET status = CASE
        WHEN t.status = 'done' THEN 'completed'
        WHEN t.status = 'cancelled' THEN 'error'
        ELSE 'error'
    END,
    error = COALESCE(NULLIF(s.error, ''), t.error, s.error),
    updated_at = NOW()
FROM chetter_user_prompts sr
JOIN chetter_tasks t ON t.id = sr.task_id
WHERE sr.agent_session_id = s.id
  AND s.status = 'running'
  AND sr.status IN ('failed', 'completed', 'cancelled')
  AND t.status IN ('done', 'error', 'cancelled');

-- name: RevertOrphanedRunningUserPrompts :execrows
UPDATE chetter_user_prompts sr
SET status = 'pending', started_at = NULL, updated_at = NOW()
FROM chetter_tasks t
WHERE t.id = sr.task_id
  AND sr.status = 'running'
  AND t.status = 'pending';
