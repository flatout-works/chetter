-- name: InsertTask :exec
INSERT INTO chetter_tasks
    (id, team_id, status, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, commit_author_name, commit_author_email, git_identity_id, runner_id, trigger_name, trigger_type, submission_source, checkpoint_after_success, required_runner_id, skills, mcp_endpoints, env, timeout_sec, search_text, created_at, updated_at)
VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTaskByID :one
SELECT * FROM chetter_tasks
WHERE id = ?;

-- name: ListTasksByStatus :many
SELECT * FROM chetter_tasks
WHERE (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.arg(trigger_name_filter), '') = '' OR trigger_name = sqlc.arg(trigger_name_filter))
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: SearchTasks :many
SELECT * FROM chetter_tasks
WHERE (sqlc.arg(team_filter) = '' OR team_id = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.arg(trigger_name_filter), '') = '' OR trigger_name = sqlc.arg(trigger_name_filter))
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: GetClaimableTaskForUpdate :one
SELECT * FROM chetter_tasks
WHERE status = 'pending'
  AND (required_runner_id IS NULL OR required_runner_id = '' OR required_runner_id = sqlc.arg(runner_id))
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: MarkTaskClaimed :execrows
UPDATE chetter_tasks
SET status = 'running',
    runner_id = ?,
    execution_id = sqlc.arg(execution_id),
    claimed_at = ?,
    lease_expires_at = ?,
    started_at = COALESCE(started_at, ?),
    updated_at = ?,
    last_event_at = ?,
    attempt = attempt + 1
WHERE id = ? AND status = 'pending';

-- name: UpdateTaskFromRunnerEvent :execrows
UPDATE chetter_tasks
SET status = sqlc.arg(status),
    summary = ?,
    error = ?,
    error_category = COALESCE(NULLIF(sqlc.arg(error_category), ''), error_category),
    session_export = COALESCE(?, session_export),
    provider_id = COALESCE(NULLIF(sqlc.arg(provider_id), ''), provider_id),
    model_id = COALESCE(NULLIF(sqlc.arg(model_id), ''), model_id),
    variant_id = COALESCE(NULLIF(sqlc.arg(variant_id), ''), variant_id),
    opencode_session_id = COALESCE(NULLIF(sqlc.arg(opencode_session_id), ''), opencode_session_id),
    runner_image_digest = COALESCE(NULLIF(sqlc.arg(runner_image_digest), ''), runner_image_digest),
    lease_expires_at = ?,
    started_at = COALESCE(?, started_at),
    ended_at = COALESCE(?, ended_at),
    updated_at = ?,
    last_event_at = ?,
    total_input_tokens = total_input_tokens + COALESCE(sqlc.arg(total_input_tokens), 0),
    total_output_tokens = total_output_tokens + COALESCE(sqlc.arg(total_output_tokens), 0),
    total_cache_read_tokens = total_cache_read_tokens + COALESCE(sqlc.arg(total_cache_read_tokens), 0),
    total_cache_write_tokens = total_cache_write_tokens + COALESCE(sqlc.arg(total_cache_write_tokens), 0),
    total_reasoning_tokens = total_reasoning_tokens + COALESCE(sqlc.arg(total_reasoning_tokens), 0),
    cost_cents = cost_cents + COALESCE(sqlc.arg(cost_cents), 0)
WHERE id = ?
  AND runner_id = sqlc.arg(runner_id)
  AND execution_id = sqlc.arg(execution_id)
  AND (status = 'running' OR status = sqlc.arg(status));

-- name: RenewTaskLease :execrows
UPDATE chetter_tasks
SET lease_expires_at = ?,
    updated_at = ?,
    last_event_at = ?
WHERE id = ?
  AND runner_id = sqlc.arg(runner_id)
  AND execution_id = sqlc.arg(execution_id)
  AND status = 'running';

-- name: ListHeartbeatTasks :many
SELECT id, status, runner_id, execution_id, error FROM chetter_tasks
WHERE id IN (sqlc.slice(ids))
  AND sqlc.arg(runner_id) = sqlc.arg(runner_id);

-- name: RenewRunningTaskLeases :execrows
UPDATE chetter_tasks
SET lease_expires_at = ?,
    updated_at = ?,
    last_event_at = ?
WHERE runner_id = sqlc.arg(runner_id)
  AND id IN (sqlc.slice(ids))
  AND status = 'running';

-- name: ReclaimExpiredLeases :execrows
UPDATE chetter_tasks
SET status = 'pending',
    runner_id = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    started_at = NULL,
    updated_at = ?
WHERE status = 'running'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at < ?
  AND attempt < max_attempts;

-- name: ListReclaimableExpiredLeases :many
SELECT id, team_id, runner_id, execution_id, attempt, lease_expires_at
FROM chetter_tasks
WHERE status = 'running'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at < ?
  AND attempt < max_attempts
ORDER BY lease_expires_at ASC
FOR UPDATE;

-- name: FailExpiredLeases :execrows
UPDATE chetter_tasks
SET status = 'error',
    error = CONCAT('runner lease expired after ', attempt, ' attempts'),
    error_category = 'timeout',
    ended_at = ?,
    updated_at = ?,
    last_event_at = ?
WHERE status = 'running'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at < ?
  AND attempt >= max_attempts;

-- name: CancelTask :execrows
UPDATE chetter_tasks
SET status = 'cancelled',
    error = ?,
    error_category = 'cancelled',
    ended_at = COALESCE(ended_at, ?),
    updated_at = ?
WHERE id = ? AND status IN ('pending', 'running');

-- name: ExtendTaskTimeout :execrows
UPDATE chetter_tasks
SET timeout_sec = timeout_sec + sqlc.arg(extension_sec),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status IN ('pending', 'running');

-- name: ClearPendingTasks :execrows
UPDATE chetter_tasks
SET status = 'cancelled',
    error = ?,
    error_category = 'cancelled',
    ended_at = COALESCE(ended_at, ?),
    updated_at = ?
WHERE status = 'pending';

-- name: GetLatestTaskEvent :one
SELECT * FROM chetter_task_events
WHERE task_id = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: ListTasksByStatusAndTeam :many
SELECT * FROM chetter_tasks
WHERE team_id = sqlc.arg(team_id)
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.arg(trigger_name_filter), '') = '' OR trigger_name = sqlc.arg(trigger_name_filter))
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListTasksByStatusAndTeams :many
SELECT * FROM chetter_tasks
WHERE team_id IN (sqlc.slice(team_ids))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.arg(trigger_name_filter), '') = '' OR trigger_name = sqlc.arg(trigger_name_filter))
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: SearchTasksByTeams :many
SELECT * FROM chetter_tasks
WHERE team_id IN (sqlc.slice(team_ids))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.arg(trigger_name_filter), '') = '' OR trigger_name = sqlc.arg(trigger_name_filter))
  AND (search_text LIKE CONCAT('%', sqlc.arg(search), '%'))
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: UpdateTaskSearchText :exec
UPDATE chetter_tasks
SET search_text = CONCAT_WS(' ',
    COALESCE(prompt, ''), COALESCE(summary, ''), COALESCE(error, ''),
    COALESCE(agent, ''), COALESCE(model_id, ''), COALESCE(trigger_name, ''),
    COALESCE(git_url, '')
)
WHERE id = sqlc.arg(id);
