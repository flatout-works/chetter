-- name: InsertAgentSession :exec
INSERT INTO agent_sessions
    (id, task_id, sequence, team_id, status, resume_mode, isolation_required, pause_reason, expires_at, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, harness, skills, mcp_endpoints, env, commit_author_name, commit_author_email, git_identity_id, search_text, created_at, updated_at, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAgentSessionByID :one
SELECT * FROM agent_sessions
WHERE id = ?;

-- name: ListAgentSessions :many
SELECT * FROM agent_sessions
WHERE (sqlc.arg(team_filter) = '' OR COALESCE(team_id, '') = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: SearchAgentSessions :many
SELECT * FROM agent_sessions
WHERE (sqlc.arg(team_filter) = '' OR COALESCE(team_id, '') = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: ListAgentSessionsByTeams :many
SELECT * FROM agent_sessions
WHERE team_id IN (sqlc.slice(team_ids))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: SearchAgentSessionsByTeams :many
SELECT * FROM agent_sessions
WHERE team_id IN (sqlc.slice(team_ids))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY updated_at DESC
LIMIT ? OFFSET ?;

-- name: MarkAgentSessionTerminalByTask :execrows
UPDATE agent_sessions
SET status = ?,
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    summary = ?,
    error = ?,
    ended_at = ?,
    updated_at = ?
WHERE id = (SELECT agent.id FROM agent_sessions agent WHERE agent.task_id = ? ORDER BY agent.sequence DESC LIMIT 1)
AND status IN ('running', 'resuming');

-- name: GetAgentSessionByTaskID :one
SELECT * FROM agent_sessions
WHERE task_id = ?
ORDER BY sequence DESC
LIMIT 1;

-- name: UpdateAgentSessionFromRunnerEvent :execrows
UPDATE agent_sessions
SET provider_id = COALESCE(NULLIF(sqlc.arg(provider_id), ''), provider_id),
    model_id = COALESCE(NULLIF(sqlc.arg(model_id), ''), model_id),
    variant_id = COALESCE(NULLIF(sqlc.arg(variant_id), ''), variant_id),
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    updated_at = sqlc.arg(updated_at)
WHERE id = (SELECT agent.id FROM agent_sessions agent WHERE agent.task_id = sqlc.arg(task_id) ORDER BY agent.sequence DESC LIMIT 1)
  AND status IN ('running', 'resuming');

-- name: PauseAgentSessionByTaskID :execrows
UPDATE agent_sessions
SET status = ?,
    pinned_runner_id = COALESCE(NULLIF(sqlc.arg(pinned_runner_id), ''), pinned_runner_id),
    checkpoint_id = COALESCE(NULLIF(sqlc.arg(checkpoint_id), ''), checkpoint_id),
    workspace_path = COALESCE(NULLIF(sqlc.arg(workspace_path), ''), workspace_path),
    container_name = COALESCE(NULLIF(sqlc.arg(container_name), ''), container_name),
    harness_session_id = COALESCE(NULLIF(sqlc.arg(harness_session_id), ''), harness_session_id),
    paused_at = ?,
    updated_at = ?
WHERE id = (SELECT agent.id FROM agent_sessions agent WHERE agent.task_id = ? ORDER BY agent.sequence DESC LIMIT 1)
AND status IN ('running', 'resuming');

-- name: MarkAgentSessionResuming :execrows
UPDATE agent_sessions
SET status = ?,
    updated_at = ?
WHERE id = ?;

-- name: AbandonAgentSession :execrows
UPDATE agent_sessions
SET status = 'abandoned', error = ?, ended_at = ?, updated_at = ?
WHERE id = ? AND status IN ('running', 'resuming');

-- name: IsRunnerAlive :one
SELECT COUNT(*) > 0 FROM runners
WHERE id = sqlc.arg(runner_id)
  AND status = 'active'
  AND last_seen_at > DATE_SUB(NOW(), INTERVAL sqlc.arg(stale_seconds) SECOND);

-- name: GetPausedSessionByArtifact :one
SELECT s.* FROM agent_sessions s
JOIN task_artifacts a ON a.agent_session_id = s.id
WHERE a.repo = sqlc.arg(repo)
  AND a.number = sqlc.arg(number)
  AND a.artifact_type = sqlc.arg(artifact_type)
  AND s.status IN ('paused', 'recoverable', 'paused_waiting_review')
  AND s.resume_mode IN ('gvisor_checkpoint', 'harness_session')
ORDER BY a.discovered_at DESC
LIMIT 1;

-- name: ExpirePausedSessions :execrows
UPDATE agent_sessions
SET status = 'expired',
    ended_at = ?,
    updated_at = ?
WHERE status IN ('paused', 'recoverable', 'paused_waiting_review')
  AND expires_at IS NOT NULL
  AND expires_at < ?;

-- name: InsertUserPrompt :exec
INSERT INTO user_prompts
    (id, agent_session_id, task_id, sequence, status, prompt, source_user_prompt_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetUserPromptByID :one
SELECT * FROM user_prompts WHERE id = ?;

-- name: GetUserPromptByTaskID :one
SELECT prompt.* FROM user_prompts prompt
JOIN agent_sessions session ON session.id = prompt.agent_session_id
WHERE prompt.task_id = ?
ORDER BY session.sequence DESC, prompt.sequence DESC
LIMIT 1;

-- name: AbandonUserPrompt :execrows
UPDATE user_prompts
SET status = 'failed', error = ?, ended_at = ?, updated_at = ?
WHERE id = ? AND status IN ('pending', 'claimed', 'running');

-- name: ListUserPromptsBySession :many
SELECT * FROM user_prompts
WHERE agent_session_id = ?
ORDER BY sequence ASC, created_at ASC;

-- name: GetNextAgentSessionSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM agent_sessions
WHERE task_id = ?;

-- name: GetNextUserPromptSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM user_prompts
WHERE agent_session_id = ?;

-- name: MarkUserPromptRunningByTask :execrows
UPDATE user_prompts target
JOIN (
    SELECT latest.id
    FROM (
        SELECT prompt.id FROM user_prompts prompt
        JOIN agent_sessions session ON session.id = prompt.agent_session_id
        WHERE prompt.task_id = sqlc.arg(task_id)
        ORDER BY session.sequence DESC, prompt.sequence DESC
        LIMIT 1
    ) latest
) selected ON selected.id = target.id
SET target.status = 'running',
    target.started_at = COALESCE(target.started_at, sqlc.arg(started_at)),
    target.updated_at = sqlc.arg(updated_at)
WHERE target.status IN ('pending', 'claimed');

-- name: MarkUserPromptTerminalByTask :execrows
UPDATE user_prompts target
JOIN (
    SELECT latest.id
    FROM (
        SELECT prompt.id FROM user_prompts prompt
        JOIN agent_sessions session ON session.id = prompt.agent_session_id
        WHERE prompt.task_id = sqlc.arg(task_id)
        ORDER BY session.sequence DESC, prompt.sequence DESC
        LIMIT 1
    ) latest
) selected ON selected.id = target.id
SET target.status = sqlc.arg(status),
    target.summary = sqlc.narg(summary),
    target.error = sqlc.narg(error),
    target.session_export = COALESCE(sqlc.narg(session_export), target.session_export),
    target.started_at = COALESCE(target.started_at, sqlc.narg(started_at)),
    target.ended_at = COALESCE(sqlc.narg(ended_at), target.ended_at),
    target.updated_at = sqlc.arg(updated_at);

-- name: FailPendingResumeTasksForMissingRunner :execrows
UPDATE tasks t
JOIN user_prompts prompt ON prompt.task_id = t.id
JOIN execution_attempts attempt ON attempt.user_prompt_id = prompt.id
SET t.status = 'error',
    t.error = attempt.error,
    t.error_category = 'runner_unavailable',
    t.failure_category = 'runner_lost',
    t.failure_message = CONCAT('Runner unavailable: ', COALESCE(attempt.error, 'unknown')),
    t.ended_at = ?,
    t.updated_at = ?
WHERE t.status = 'pending'
  AND attempt.status = 'error'
  AND attempt.error_category = 'runner_unavailable';

-- name: FailPendingUserPromptsForUnavailableRunner :execrows
UPDATE user_prompts sr
JOIN tasks t ON t.id = sr.task_id
SET sr.status = 'failed',
    sr.error = t.error,
    sr.ended_at = COALESCE(sr.ended_at, ?),
    sr.updated_at = ?
WHERE sr.status = 'pending'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable';

-- name: MarkResumingSessionsFailedForUnavailableRunner :execrows
UPDATE agent_sessions s
JOIN user_prompts sr ON sr.agent_session_id = s.id
JOIN tasks t ON t.id = sr.task_id
SET s.status = 'error',
    s.error = COALESCE(sr.error, t.error),
    s.ended_at = ?,
    s.updated_at = ?
WHERE s.status = 'resuming'
  AND sr.status = 'failed'
  AND t.status = 'error'
  AND t.error_category = 'runner_unavailable';

-- name: InsertAgentSessionCheckpoint :exec
INSERT INTO agent_session_checkpoints
    (id, agent_session_id, user_prompt_id, runner_id, checkpoint_path, workspace_path, container_name, runsc_version, agent_image, size_bytes, status, error, created_at, updated_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestAgentSessionCheckpoint :one
SELECT * FROM agent_session_checkpoints
WHERE agent_session_id = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: GetLatestAgentSessionCheckpointByTaskID :one
SELECT chk.* FROM agent_session_checkpoints chk
JOIN user_prompts r ON r.agent_session_id = chk.agent_session_id
WHERE r.task_id = ?
ORDER BY chk.created_at DESC
LIMIT 1;

-- name: ReapStaleUserPrompts :execrows
UPDATE user_prompts sr
JOIN tasks t ON t.id = sr.task_id
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
UPDATE agent_sessions s
JOIN user_prompts sr ON sr.agent_session_id = s.id
JOIN tasks t ON t.id = sr.task_id
SET s.status = CASE
    WHEN t.status = 'done' THEN 'completed'
    WHEN t.status = 'cancelled' THEN 'error'
    ELSE 'error'
END,
s.error = COALESCE(NULLIF(s.error, ''), t.error, s.error),
s.summary = COALESCE(NULLIF(s.summary, ''), t.summary, s.summary),
s.ended_at = COALESCE(s.ended_at, t.ended_at, NOW()),
s.updated_at = NOW()
WHERE s.status = 'running'
  AND sr.status IN ('failed', 'completed', 'cancelled')
  AND t.status IN ('done', 'error', 'cancelled');

-- name: RevertOrphanedRunningUserPrompts :execrows
UPDATE user_prompts sr
JOIN tasks t ON t.id = sr.task_id
SET sr.status = 'pending',
    sr.started_at = NULL,
    sr.updated_at = NOW()
WHERE sr.status = 'running'
  AND t.status = 'pending';

-- name: ClearExpiredSessionCheckpoints :execrows
UPDATE agent_session_checkpoints
SET checkpoint_path = '', updated_at = NOW()
WHERE agent_session_id IN (
  SELECT id FROM agent_sessions
  WHERE status IN ('completed', 'failed', 'cancelled', 'timed_out', 'expired', 'abandoned', 'error')
    AND updated_at < DATE_SUB(NOW(), INTERVAL sqlc.arg(ttl_seconds) SECOND)
)
AND checkpoint_path != '';

-- name: ClearExpiredUserPromptExports :execrows
UPDATE user_prompts
SET session_export = NULL, updated_at = NOW()
WHERE agent_session_id IN (
  SELECT id FROM agent_sessions
  WHERE status IN ('completed', 'failed', 'cancelled', 'timed_out', 'expired', 'abandoned', 'error')
    AND updated_at < DATE_SUB(NOW(), INTERVAL sqlc.arg(ttl_seconds) SECOND)
)
AND session_export IS NOT NULL;

-- name: ClearExpiredExecutionAttemptExports :execrows
UPDATE execution_attempts
SET session_export = NULL, updated_at = NOW()
WHERE user_prompt_id IN (
  SELECT id FROM user_prompts
  WHERE agent_session_id IN (
    SELECT id FROM agent_sessions
    WHERE status IN ('completed', 'failed', 'cancelled', 'timed_out', 'expired', 'abandoned', 'error')
      AND updated_at < DATE_SUB(NOW(), INTERVAL sqlc.arg(ttl_seconds) SECOND)
  )
)
AND session_export IS NOT NULL;
