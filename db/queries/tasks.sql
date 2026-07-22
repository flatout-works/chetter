-- name: InsertTask :exec
INSERT INTO chetter_tasks
    (id, team_id, status, prompt, git_url, git_ref, trigger_name, trigger_type, submission_source, search_text, created_at, updated_at)
VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTaskByID :one
SELECT * FROM chetter_tasks
WHERE id = ?;

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
JOIN chetter_user_prompts prompt ON prompt.task_id = task.id
JOIN chetter_execution_attempts attempt ON attempt.user_prompt_id = prompt.id
SET task.status = 'error',
    task.error = attempt.error,
    task.error_category = 'timeout',
    task.ended_at = ?,
    task.updated_at = ?
WHERE task.status = 'running'
  AND attempt.status = 'error'
  AND attempt.error_category = 'timeout'
  AND attempt.lease_expires_at IS NOT NULL
  AND attempt.lease_expires_at < ?;

-- name: CancelTask :execrows
UPDATE chetter_tasks
SET status = 'cancelled',
    error = ?,
    error_category = 'cancelled',
    ended_at = COALESCE(ended_at, ?),
    updated_at = ?
WHERE id = ? AND status IN ('pending', 'running');

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
	COALESCE((SELECT agent FROM chetter_agent_sessions WHERE task_id = chetter_tasks.id ORDER BY sequence DESC LIMIT 1), ''),
	COALESCE((SELECT model_id FROM chetter_agent_sessions WHERE task_id = chetter_tasks.id ORDER BY sequence DESC LIMIT 1), ''),
	COALESCE(trigger_name, ''), COALESCE(git_url, '')
)
WHERE chetter_tasks.id = sqlc.arg(id);
