-- name: InsertTask :exec
INSERT INTO chetter_tasks
    (id, team_id, status, prompt, git_url, git_ref, trigger_name, trigger_type, submission_source, search_text, created_at, updated_at)
VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: GetTaskByID :one
SELECT * FROM chetter_tasks WHERE id = $1;

-- name: RequeueTaskForPrompt :execrows
UPDATE chetter_tasks
SET status = 'pending',
    summary = NULL,
    error = NULL,
    error_category = NULL,
    ended_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status IN ('done', 'error', 'cancelled');

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

-- name: MarkTaskRunning :execrows
UPDATE chetter_tasks
SET status = 'running',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'pending';

-- name: UpdateTaskAggregateFromRunnerEvent :execrows
UPDATE chetter_tasks
SET status = sqlc.arg(status),
    summary = sqlc.narg(summary),
    error = sqlc.narg(error),
    error_category = COALESCE(NULLIF(sqlc.arg(error_category), ''), error_category),
    ended_at = COALESCE(sqlc.narg(ended_at), ended_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND (status = 'running' OR status = sqlc.arg(status));

-- name: FailExpiredLeases :execrows
UPDATE chetter_tasks task
SET status = 'error',
    error = attempt.error,
    error_category = 'timeout',
    ended_at = $1,
    updated_at = $2
FROM chetter_user_prompts prompt, chetter_execution_attempts attempt
WHERE prompt.task_id = task.id
  AND attempt.user_prompt_id = prompt.id
  AND task.status = 'running'
  AND attempt.status = 'error'
  AND attempt.error_category = 'timeout'
  AND attempt.lease_expires_at IS NOT NULL
  AND attempt.lease_expires_at < $3;

-- name: CancelTask :execrows
UPDATE chetter_tasks
SET status = 'cancelled',
    error = $1,
    error_category = 'cancelled',
    ended_at = COALESCE(ended_at, $2),
    updated_at = $3
WHERE id = $4 AND status IN ('pending', 'running');

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
	COALESCE((SELECT agent FROM chetter_agent_sessions WHERE task_id = chetter_tasks.id ORDER BY sequence DESC LIMIT 1), ''),
	COALESCE((SELECT model_id FROM chetter_agent_sessions WHERE task_id = chetter_tasks.id ORDER BY sequence DESC LIMIT 1), ''),
	COALESCE(trigger_name, ''), COALESCE(git_url, '')
)
WHERE chetter_tasks.id = sqlc.arg(id);
