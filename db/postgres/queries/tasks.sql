-- name: InsertTask :exec
INSERT INTO tasks
    (id, team_id, status, prompt, git_url, git_ref, github_repo, github_installation_id, trigger_name, trigger_type, submission_source, self_test_run_id, self_test_profile, self_test_check, self_test_nonce, search_text, created_at, updated_at)
VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: GetTaskByID :one
SELECT * FROM tasks WHERE id = $1;

-- name: ListTasksBySelfTestRun :many
SELECT * FROM tasks
WHERE self_test_run_id = $1
ORDER BY created_at ASC, id ASC;

-- name: PinTaskGitHubInstallation :execrows
UPDATE tasks
SET github_installation_id = sqlc.arg(github_installation_id),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND github_installation_id IS NULL;

-- name: RequeueTaskForPrompt :execrows
UPDATE tasks
SET status = 'pending',
    summary = NULL,
    error = NULL,
    error_category = NULL,
    failure_category = NULL,
    failure_message = NULL,
    ended_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status IN ('done', 'error', 'cancelled');

-- name: ListTasksByStatus :many
SELECT * FROM tasks
WHERE (sqlc.arg(status_filter) = '' OR tasks.status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR tasks.trigger_name = sqlc.narg(trigger_name_filter))
  AND (COALESCE(sqlc.arg(agent_filter), '') = '' OR EXISTS (
      SELECT 1 FROM agent_sessions session
      WHERE session.task_id = tasks.id AND session.agent = sqlc.arg(agent_filter)
  ))
ORDER BY tasks.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchTasks :many
SELECT * FROM tasks
WHERE (sqlc.arg(team_filter) = '' OR tasks.team_id = sqlc.arg(team_filter))
  AND (sqlc.arg(status_filter) = '' OR tasks.status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR tasks.trigger_name = sqlc.narg(trigger_name_filter))
  AND (COALESCE(sqlc.arg(agent_filter), '') = '' OR EXISTS (
      SELECT 1 FROM agent_sessions session
      WHERE session.task_id = tasks.id AND session.agent = sqlc.arg(agent_filter)
  ))
  AND tasks.search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY tasks.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: MarkTaskRunning :execrows
UPDATE tasks
SET status = 'running',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'pending';

-- name: UpdateTaskAggregateFromRunnerEvent :execrows
UPDATE tasks
SET status = sqlc.arg(status),
    summary = sqlc.narg(summary),
    error = sqlc.narg(error),
    error_category = COALESCE(NULLIF(sqlc.arg(error_category), ''), error_category),
    failure_category = COALESCE(NULLIF(sqlc.arg(failure_category), ''), failure_category),
    failure_message = COALESCE(sqlc.narg(failure_message), failure_message),
    ended_at = COALESCE(sqlc.narg(ended_at), ended_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND (status = 'running' OR status = sqlc.arg(status));

-- name: FailExpiredLeases :execrows
UPDATE tasks task
SET status = 'error',
    error = attempt.error,
    error_category = 'timeout',
    failure_category = 'timeout',
    failure_message = 'Task timed out after ' || COALESCE(attempt.error, 'lease expiry'),
    ended_at = $1,
    updated_at = $2
FROM user_prompts prompt, execution_attempts attempt
WHERE prompt.task_id = task.id
  AND attempt.user_prompt_id = prompt.id
  AND task.status = 'running'
  AND attempt.status = 'error'
  AND attempt.error_category = 'timeout'
  AND attempt.lease_expires_at IS NOT NULL
  AND attempt.lease_expires_at < $3;

-- name: CancelTask :execrows
UPDATE tasks
SET status = 'cancelled',
    error = $1,
    error_category = 'cancelled',
    failure_category = 'user_cancelled',
    failure_message = $2,
    ended_at = COALESCE(ended_at, $3),
    updated_at = $4
WHERE id = $5 AND status IN ('pending', 'running');

-- name: ClearPendingTasks :execrows
UPDATE tasks
SET status = 'cancelled',
    error = $1,
    error_category = 'cancelled',
    failure_category = 'user_cancelled',
    failure_message = $2,
    ended_at = COALESCE(ended_at, $3),
    updated_at = $4
WHERE status = 'pending';

-- name: GetLatestTaskEvent :one
SELECT * FROM task_events
WHERE task_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ListTasksByStatusAndTeam :many
SELECT * FROM tasks
WHERE tasks.team_id = sqlc.arg(team_id)
  AND (sqlc.arg(status_filter) = '' OR tasks.status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR tasks.trigger_name = sqlc.narg(trigger_name_filter))
  AND (COALESCE(sqlc.arg(agent_filter), '') = '' OR EXISTS (
      SELECT 1 FROM agent_sessions session
      WHERE session.task_id = tasks.id AND session.agent = sqlc.arg(agent_filter)
  ))
ORDER BY tasks.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListTasksByStatusAndTeams :many
SELECT * FROM tasks
WHERE tasks.team_id = ANY(sqlc.arg(team_ids)::text[])
  AND (sqlc.arg(status_filter) = '' OR tasks.status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR tasks.trigger_name = sqlc.narg(trigger_name_filter))
  AND (COALESCE(sqlc.arg(agent_filter), '') = '' OR EXISTS (
      SELECT 1 FROM agent_sessions session
      WHERE session.task_id = tasks.id AND session.agent = sqlc.arg(agent_filter)
  ))
ORDER BY tasks.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SearchTasksByTeams :many
SELECT * FROM tasks
WHERE tasks.team_id = ANY(sqlc.arg(team_ids)::text[])
  AND (sqlc.arg(status_filter) = '' OR tasks.status = sqlc.arg(status_filter))
  AND (COALESCE(sqlc.narg(trigger_name_filter), '') = '' OR tasks.trigger_name = sqlc.narg(trigger_name_filter))
  AND (COALESCE(sqlc.arg(agent_filter), '') = '' OR EXISTS (
      SELECT 1 FROM agent_sessions session
      WHERE session.task_id = tasks.id AND session.agent = sqlc.arg(agent_filter)
  ))
  AND tasks.search_text ILIKE '%' || sqlc.arg(search) || '%'
ORDER BY tasks.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateTaskSearchText :exec
UPDATE tasks
SET search_text = concat_ws(' ',
	COALESCE(prompt, ''), COALESCE(summary, ''), COALESCE(error, ''),
	COALESCE((SELECT agent FROM agent_sessions WHERE task_id = tasks.id ORDER BY sequence DESC LIMIT 1), ''),
	COALESCE((SELECT model_id FROM agent_sessions WHERE task_id = tasks.id ORDER BY sequence DESC LIMIT 1), ''),
	COALESCE(trigger_name, ''), COALESCE(git_url, ''), COALESCE(github_repo, '')
)
WHERE tasks.id = sqlc.arg(id);

-- name: FailPendingIsolationTasks :execrows
-- Marks pending tasks whose execution attempts failed with
-- isolation_unavailable as terminal errors. Companion to
-- FailPendingIsolationAttemptsWithoutCapableRunner (issue #291).
UPDATE tasks task
SET status = 'error',
    error = 'no active runner enforces isolation (gVisor) for this task',
    error_category = 'isolation_unavailable',
    failure_category = 'harness_error',
    failure_message = 'No active runner enforces isolation (gVisor) for this task; it cannot run unsandboxed.',
    ended_at = sqlc.arg(ended_at),
    updated_at = sqlc.arg(updated_at)
FROM user_prompts prompt, execution_attempts attempt
WHERE prompt.task_id = task.id
  AND attempt.user_prompt_id = prompt.id
  AND task.status = 'pending'
  AND attempt.status = 'error'
  AND attempt.error_category = 'isolation_unavailable';
