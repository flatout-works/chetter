-- name: InsertAgentSession :exec
INSERT INTO chetter_agent_sessions
    (id, task_id, sequence, team_id, status, resume_mode, pause_reason, expires_at, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, mcp_endpoints, env, commit_author_name, commit_author_email, git_identity_id, search_text, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAgentSessionByID :one
SELECT * FROM chetter_agent_sessions
WHERE id = ?;

-- name: ListAgentSessions :many
SELECT * FROM chetter_agent_sessions
WHERE (sqlc.arg(team_filter) = '' OR COALESCE(team_id, '') = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: SearchAgentSessions :many
SELECT * FROM chetter_agent_sessions
WHERE (sqlc.arg(team_filter) = '' OR COALESCE(team_id, '') = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: ListAgentSessionsByTeams :many
SELECT * FROM chetter_agent_sessions
WHERE team_id IN (sqlc.slice(team_ids))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: SearchAgentSessionsByTeams :many
SELECT * FROM chetter_agent_sessions
WHERE team_id IN (sqlc.slice(team_ids))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: MarkAgentSessionTerminalByTask :execrows
UPDATE chetter_agent_sessions
SET status = ?,
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    error = ?,
    updated_at = ?
WHERE id = (SELECT agent.id FROM chetter_agent_sessions agent WHERE agent.task_id = ? ORDER BY agent.sequence DESC LIMIT 1)
AND status IN ('running', 'resuming');

-- name: GetAgentSessionByTaskID :one
SELECT * FROM chetter_agent_sessions
WHERE task_id = ?
ORDER BY sequence DESC
LIMIT 1;

-- name: UpdateAgentSessionFromRunnerEvent :execrows
UPDATE chetter_agent_sessions
SET provider_id = COALESCE(NULLIF(sqlc.arg(provider_id), ''), provider_id),
    model_id = COALESCE(NULLIF(sqlc.arg(model_id), ''), model_id),
    variant_id = COALESCE(NULLIF(sqlc.arg(variant_id), ''), variant_id),
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    updated_at = sqlc.arg(updated_at)
WHERE id = (SELECT agent.id FROM chetter_agent_sessions agent WHERE agent.task_id = sqlc.arg(task_id) ORDER BY agent.sequence DESC LIMIT 1)
  AND status IN ('running', 'resuming');

-- name: PauseAgentSessionByTaskID :execrows
UPDATE chetter_agent_sessions
SET status = ?,
    pinned_runner_id = COALESCE(NULLIF(sqlc.arg(pinned_runner_id), ''), pinned_runner_id),
    checkpoint_id = COALESCE(NULLIF(sqlc.arg(checkpoint_id), ''), checkpoint_id),
    workspace_path = COALESCE(NULLIF(sqlc.arg(workspace_path), ''), workspace_path),
    container_name = COALESCE(NULLIF(sqlc.arg(container_name), ''), container_name),
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    paused_at = ?,
    updated_at = ?
WHERE id = (SELECT agent.id FROM chetter_agent_sessions agent WHERE agent.task_id = ? ORDER BY agent.sequence DESC LIMIT 1)
AND status IN ('running', 'resuming');

-- name: MarkAgentSessionResuming :execrows
UPDATE chetter_agent_sessions
SET status = ?,
    updated_at = ?
WHERE id = ?;

-- name: AbandonAgentSession :execrows
UPDATE chetter_agent_sessions
SET status = 'abandoned', error = ?, updated_at = ?
WHERE id = ? AND status IN ('running', 'resuming');

-- name: IsRunnerAlive :one
SELECT COUNT(*) > 0 FROM chetter_runners
WHERE id = sqlc.arg(runner_id)
  AND status = 'active'
  AND last_seen_at > DATE_SUB(NOW(), INTERVAL sqlc.arg(stale_seconds) SECOND);

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
SET status = 'expired',
    updated_at = ?
WHERE status IN ('paused', 'recoverable', 'paused_waiting_review')
  AND expires_at IS NOT NULL
  AND expires_at < ?;

-- name: InsertUserPrompt :exec
INSERT INTO chetter_user_prompts
    (id, agent_session_id, task_id, sequence, status, prompt, source_user_prompt_id, required_runner_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetUserPromptByID :one
SELECT * FROM chetter_user_prompts WHERE id = ?;

-- name: GetUserPromptByTaskID :one
SELECT prompt.* FROM chetter_user_prompts prompt
JOIN chetter_agent_sessions session ON session.id = prompt.agent_session_id
WHERE prompt.task_id = ?
ORDER BY session.sequence DESC, prompt.sequence DESC
LIMIT 1;

-- name: AbandonUserPrompt :execrows
UPDATE chetter_user_prompts
SET status = 'failed', error = ?, ended_at = ?, updated_at = ?
WHERE id = ? AND status IN ('pending', 'claimed', 'running');

-- name: ListUserPromptsBySession :many
SELECT * FROM chetter_user_prompts
WHERE agent_session_id = ?
ORDER BY sequence ASC, created_at ASC;

-- name: GetNextAgentSessionSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM chetter_agent_sessions
WHERE task_id = ?;

-- name: GetNextUserPromptSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM chetter_user_prompts
WHERE agent_session_id = ?;

-- name: MarkUserPromptRunningByTask :execrows
UPDATE chetter_user_prompts
SET status = 'running',
    started_at = COALESCE(started_at, ?),
    updated_at = ?
WHERE id = (
    SELECT prompt.id FROM chetter_user_prompts prompt
    JOIN chetter_agent_sessions session ON session.id = prompt.agent_session_id
    WHERE prompt.task_id = ?
    ORDER BY session.sequence DESC, prompt.sequence DESC
    LIMIT 1
)
  AND status IN ('pending', 'claimed');

-- name: MarkUserPromptTerminalByTask :execrows
UPDATE chetter_user_prompts
SET status = ?,
    summary = ?,
    error = ?,
    session_export = COALESCE(?, session_export),
    started_at = COALESCE(started_at, ?),
    ended_at = COALESCE(?, ended_at),
    updated_at = ?
WHERE id = (
    SELECT prompt.id FROM chetter_user_prompts prompt
    JOIN chetter_agent_sessions session ON session.id = prompt.agent_session_id
    WHERE prompt.task_id = ?
    ORDER BY session.sequence DESC, prompt.sequence DESC
    LIMIT 1
);

-- name: FailPendingResumeTasksForMissingRunner :execrows
UPDATE chetter_tasks t
JOIN chetter_user_prompts prompt ON prompt.task_id = t.id
JOIN chetter_execution_attempts attempt ON attempt.user_prompt_id = prompt.id
SET t.status = 'error',
    t.error = attempt.error,
    t.error_category = 'runner_unavailable',
    t.ended_at = ?,
    t.updated_at = ?
WHERE t.status = 'pending'
  AND attempt.status = 'error'
  AND attempt.error_category = 'runner_unavailable';

-- name: FailPendingUserPromptsForUnavailableRunner :execrows
UPDATE chetter_user_prompts sr
JOIN chetter_tasks t ON t.id = sr.task_id
SET sr.status = 'failed',
    sr.error = t.error,
    sr.ended_at = COALESCE(sr.ended_at, ?),
    sr.updated_at = ?
WHERE sr.status = 'pending'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable';

-- name: MarkResumingSessionsFailedForUnavailableRunner :execrows
UPDATE chetter_agent_sessions s
JOIN chetter_user_prompts sr ON sr.agent_session_id = s.id
JOIN chetter_tasks t ON t.id = sr.task_id
SET s.status = 'error',
    s.error = COALESCE(sr.error, t.error),
    s.updated_at = ?
WHERE s.status = 'resuming'
  AND sr.status = 'failed'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable';

-- name: InsertAgentSessionCheckpoint :exec
INSERT INTO chetter_agent_session_checkpoints
    (id, agent_session_id, user_prompt_id, runner_id, checkpoint_path, workspace_path, container_name, runsc_version, agent_image, size_bytes, status, error, created_at, updated_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestAgentSessionCheckpoint :one
SELECT * FROM chetter_agent_session_checkpoints
WHERE agent_session_id = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: GetLatestAgentSessionCheckpointByTaskID :one
SELECT chk.* FROM chetter_agent_session_checkpoints chk
JOIN chetter_user_prompts r ON r.agent_session_id = chk.agent_session_id
WHERE r.task_id = ?
ORDER BY chk.created_at DESC
LIMIT 1;

-- name: ReapStaleUserPrompts :execrows
UPDATE chetter_user_prompts sr
JOIN chetter_tasks t ON t.id = sr.task_id
SET sr.status = CASE
    WHEN t.status = 'done' THEN 'completed'
    WHEN t.status = 'cancelled' THEN 'cancelled'
    ELSE 'failed'
END,
sr.error = COALESCE(NULLIF(sr.error, ''), t.error, sr.error),
sr.ended_at = COALESCE(sr.ended_at, t.ended_at, NOW()),
sr.updated_at = NOW()
WHERE sr.status = 'running'
  AND t.status IN ('done', 'error', 'cancelled');

-- name: ReapStaleSessionsForTerminalRuns :execrows
UPDATE chetter_agent_sessions s
JOIN chetter_user_prompts sr ON sr.agent_session_id = s.id
JOIN chetter_tasks t ON t.id = sr.task_id
SET s.status = CASE
    WHEN t.status = 'done' THEN 'completed'
    WHEN t.status = 'cancelled' THEN 'error'
    ELSE 'error'
END,
s.error = COALESCE(NULLIF(s.error, ''), t.error, s.error),
s.updated_at = NOW()
WHERE s.status = 'running'
  AND sr.status IN ('failed', 'completed', 'cancelled')
  AND t.status IN ('done', 'error', 'cancelled');

-- name: RevertOrphanedRunningUserPrompts :execrows
UPDATE chetter_user_prompts sr
JOIN chetter_tasks t ON t.id = sr.task_id
SET sr.status = 'pending',
    sr.started_at = NULL,
    sr.updated_at = NOW()
WHERE sr.status = 'running'
  AND t.status = 'pending';
