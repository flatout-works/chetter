-- name: InsertTask :exec
INSERT INTO chetter_tasks
    (id, team_id, status, prompt, git_url, git_ref, agent_image, agent, provider_id, model_id, variant_id, commit_author_name, commit_author_email, git_identity_id, runner_id, trigger_name, trigger_type, submission_source, checkpoint_after_success, required_runner_id, skills, env, timeout_sec, search_text, created_at, updated_at)
VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NULL, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24);

-- name: GetTaskByID :one
SELECT * FROM chetter_tasks WHERE id = $1;

-- name: ListTasksByStatus :many
SELECT * FROM chetter_tasks
WHERE (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR trigger_name = sqlc.narg(trigger_name_filter))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchTasks :many
SELECT * FROM chetter_tasks
WHERE (sqlc.arg(team_filter) = '' OR team_id = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR trigger_name = sqlc.narg(trigger_name_filter))
  AND search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

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
    runner_id = $1,
    claimed_at = $2,
    lease_expires_at = $3,
    started_at = COALESCE(started_at, $4),
    updated_at = $5,
    last_event_at = $6,
    attempt = attempt + 1
WHERE id = $7 AND status = 'pending';

-- name: UpdateTaskFromRunnerEvent :execrows
UPDATE chetter_tasks
SET status = sqlc.arg(status),
    summary = $1,
    error = $2,
    error_category = COALESCE(NULLIF(sqlc.arg(error_category), ''), error_category),
    session_export = COALESCE($3, session_export),
    provider_id = COALESCE(NULLIF(sqlc.arg(provider_id), ''), provider_id),
    model_id = COALESCE(NULLIF(sqlc.arg(model_id), ''), model_id),
    variant_id = COALESCE(NULLIF(sqlc.arg(variant_id), ''), variant_id),
    opencode_session_id = COALESCE(NULLIF(sqlc.arg(opencode_session_id), ''), opencode_session_id),
    runner_image_digest = COALESCE(NULLIF(sqlc.arg(runner_image_digest), ''), runner_image_digest),
    lease_expires_at = $4,
    started_at = COALESCE($5, started_at),
    ended_at = COALESCE($6, ended_at),
    updated_at = $7,
    last_event_at = $8,
    total_input_tokens = total_input_tokens + COALESCE(sqlc.narg(total_input_tokens), 0),
    total_output_tokens = total_output_tokens + COALESCE(sqlc.narg(total_output_tokens), 0),
    total_cache_read_tokens = total_cache_read_tokens + COALESCE(sqlc.narg(total_cache_read_tokens), 0),
    total_cache_write_tokens = total_cache_write_tokens + COALESCE(sqlc.narg(total_cache_write_tokens), 0),
    total_reasoning_tokens = total_reasoning_tokens + COALESCE(sqlc.narg(total_reasoning_tokens), 0),
    cost_cents = cost_cents + COALESCE(sqlc.narg(cost_cents), 0)
WHERE id = $9
  AND runner_id = sqlc.arg(runner_id)
  AND (status = 'running' OR status = sqlc.arg(status));

-- name: RenewTaskLease :execrows
UPDATE chetter_tasks
SET lease_expires_at = $1, updated_at = $2, last_event_at = $3
WHERE id = $4 AND runner_id = sqlc.arg(runner_id) AND status = 'running';

-- name: ListHeartbeatTasks :many
SELECT id, status, runner_id, error FROM chetter_tasks
WHERE id = ANY(sqlc.arg(ids)::text[])
  AND (
    runner_id = sqlc.arg(runner_id)
    OR status = 'cancelled'
    OR (status = 'pending' AND runner_id IS NULL)
  );

-- name: RenewRunningTaskLeases :execrows
UPDATE chetter_tasks
SET lease_expires_at = $1, updated_at = $2, last_event_at = $3
WHERE runner_id = sqlc.arg(runner_id)
  AND id = ANY(sqlc.arg(ids)::text[])
  AND status = 'running';

-- name: ReclaimExpiredLeases :execrows
UPDATE chetter_tasks
SET status = 'pending',
    runner_id = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    started_at = NULL,
    updated_at = $1
WHERE status = 'running'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at < $2
  AND attempt < max_attempts;

-- name: FailExpiredLeases :execrows
UPDATE chetter_tasks
SET status = 'error',
    error = 'runner lease expired after ' || attempt || ' attempts',
    error_category = 'timeout',
    ended_at = $1,
    updated_at = $2,
    last_event_at = $3
WHERE status = 'running'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at < $4
  AND attempt >= max_attempts;

-- name: CancelTask :execrows
UPDATE chetter_tasks
SET status = 'cancelled',
    error = $1,
    error_category = 'cancelled',
    ended_at = COALESCE(ended_at, $2),
    updated_at = $3
WHERE id = $4 AND status IN ('pending', 'running');

-- name: ExtendTaskTimeout :execrows
UPDATE chetter_tasks
SET timeout_sec = timeout_sec + sqlc.arg(extension_sec),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status IN ('pending', 'running');

-- name: ClearPendingTasks :execrows
UPDATE chetter_tasks
SET status = 'cancelled',
    error = $1,
    error_category = 'cancelled',
    ended_at = COALESCE(ended_at, $2),
    updated_at = $3
WHERE status = 'pending';

-- name: GetLatestTaskEvent :one
SELECT * FROM chetter_task_events
WHERE task_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ListTasksByStatusAndTeam :many
SELECT * FROM chetter_tasks
WHERE team_id = sqlc.arg(team_id)
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR trigger_name = sqlc.narg(trigger_name_filter))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListTasksByStatusAndTeams :many
SELECT * FROM chetter_tasks
WHERE team_id = ANY(sqlc.arg(team_ids)::text[])
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR trigger_name = sqlc.narg(trigger_name_filter))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchTasksByTeams :many
SELECT * FROM chetter_tasks
WHERE team_id = ANY(sqlc.arg(team_ids)::text[])
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR trigger_name = sqlc.narg(trigger_name_filter))
  AND search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateTaskSearchText :exec
UPDATE chetter_tasks
SET search_text = concat_ws(' ',
    COALESCE(prompt, ''), COALESCE(summary, ''), COALESCE(error, ''),
    COALESCE(agent, ''), COALESCE(model_id, ''), COALESCE(trigger_name, ''),
    COALESCE(git_url, '')
)
WHERE id = sqlc.arg(id);
