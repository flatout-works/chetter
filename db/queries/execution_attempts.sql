-- name: InsertExecutionAttempt :exec
INSERT INTO chetter_execution_attempts
    (id, user_prompt_id, sequence, status, runner_id, required_runner_id, claimed_at, lease_expires_at, started_at, created_at, updated_at)
VALUES (?, ?, ?, 'running', ?, ?, ?, ?, ?, ?, ?);

-- name: InsertPendingExecutionAttempt :exec
INSERT INTO chetter_execution_attempts
    (id, user_prompt_id, sequence, status, required_runner_id, created_at, updated_at)
VALUES (?, ?, ?, 'pending', ?, ?, ?);

-- name: GetClaimableExecutionAttemptForUpdate :one
SELECT attempt.id AS execution_attempt_id, prompt.task_id, prompt.id AS user_prompt_id, task.id AS locked_task_id
FROM chetter_execution_attempts attempt
JOIN chetter_user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN chetter_tasks task ON task.id = prompt.task_id
WHERE attempt.status = 'pending'
  AND task.status = 'pending'
  AND task.attempt < task.max_attempts
  AND (attempt.required_runner_id IS NULL OR attempt.required_runner_id = '' OR attempt.required_runner_id = sqlc.arg(runner_id))
ORDER BY attempt.created_at ASC
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: MarkExecutionAttemptClaimed :execrows
UPDATE chetter_execution_attempts
SET status = 'running', runner_id = ?, claimed_at = ?, lease_expires_at = ?, started_at = ?, updated_at = ?
WHERE id = ? AND status = 'pending';

-- name: GetExecutionAttemptByID :one
SELECT * FROM chetter_execution_attempts WHERE id = ?;

-- name: ListExecutionAttemptsByPrompt :many
SELECT * FROM chetter_execution_attempts
WHERE user_prompt_id = ?
ORDER BY sequence ASC, created_at ASC;

-- name: GetNextExecutionAttemptSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM chetter_execution_attempts
WHERE user_prompt_id = ?;

-- name: RenewExecutionAttemptLease :execrows
UPDATE chetter_execution_attempts
SET lease_expires_at = ?, updated_at = ?
WHERE id = ? AND runner_id = ? AND status = 'running';

-- name: MarkExecutionAttemptLost :execrows
UPDATE chetter_execution_attempts
SET status = 'lost', error = ?, ended_at = ?, updated_at = ?
WHERE id = ? AND status = 'running';

-- name: UpdateExecutionAttemptFromRunnerEvent :execrows
UPDATE chetter_execution_attempts
SET status = sqlc.arg(status),
    summary = sqlc.narg(summary),
    error = sqlc.narg(error),
    error_category = COALESCE(NULLIF(sqlc.arg(error_category), ''), error_category),
    session_export = COALESCE(sqlc.narg(session_export), session_export),
    lease_expires_at = sqlc.narg(lease_expires_at),
    started_at = COALESCE(sqlc.narg(started_at), started_at),
    ended_at = COALESCE(sqlc.narg(ended_at), ended_at),
    workspace_path = COALESCE(NULLIF(sqlc.arg(workspace_path), ''), workspace_path),
    harness_execution_id = COALESCE(NULLIF(sqlc.arg(harness_execution_id), ''), harness_execution_id),
    total_input_tokens = total_input_tokens + sqlc.arg(total_input_tokens),
    total_output_tokens = total_output_tokens + sqlc.arg(total_output_tokens),
    total_cache_read_tokens = total_cache_read_tokens + sqlc.arg(total_cache_read_tokens),
    total_cache_write_tokens = total_cache_write_tokens + sqlc.arg(total_cache_write_tokens),
    total_reasoning_tokens = total_reasoning_tokens + sqlc.arg(total_reasoning_tokens),
    cost_cents = cost_cents + sqlc.arg(cost_cents),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND runner_id = sqlc.narg(runner_id) AND status = 'running';
