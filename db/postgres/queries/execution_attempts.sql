-- name: InsertExecutionAttempt :exec
INSERT INTO chetter_execution_attempts
    (id, user_prompt_id, sequence, status, runner_id, required_runner_id, claimed_at, lease_expires_at, started_at, created_at, updated_at)
VALUES ($1, $2, $3, 'running', $4, $5, $6, $7, $8, $9, $10);

-- name: GetExecutionAttemptByID :one
SELECT * FROM chetter_execution_attempts WHERE id = $1;

-- name: GetNextExecutionAttemptSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM chetter_execution_attempts
WHERE user_prompt_id = $1;

-- name: RenewExecutionAttemptLease :execrows
UPDATE chetter_execution_attempts
SET lease_expires_at = $1, updated_at = $2
WHERE id = $3 AND runner_id = $4 AND status = 'running';

-- name: MarkExecutionAttemptLost :execrows
UPDATE chetter_execution_attempts
SET status = 'lost', error = $1, ended_at = $2, updated_at = $3
WHERE id = $4 AND status = 'running';

-- name: UpdateExecutionAttemptFromRunnerEvent :execrows
UPDATE chetter_execution_attempts
SET status = sqlc.arg(status),
    summary = sqlc.narg(summary),
    error = sqlc.narg(error),
    error_category = COALESCE(NULLIF(sqlc.arg(error_category)::text, ''), error_category),
    session_export = COALESCE(sqlc.narg(session_export), session_export),
    lease_expires_at = sqlc.narg(lease_expires_at),
    started_at = COALESCE(sqlc.narg(started_at), started_at),
    ended_at = COALESCE(sqlc.narg(ended_at), ended_at),
    workspace_path = COALESCE(NULLIF(sqlc.arg(workspace_path)::text, ''), workspace_path),
    harness_execution_id = COALESCE(NULLIF(sqlc.arg(harness_execution_id)::text, ''), harness_execution_id),
    total_input_tokens = total_input_tokens + sqlc.arg(total_input_tokens),
    total_output_tokens = total_output_tokens + sqlc.arg(total_output_tokens),
    total_cache_read_tokens = total_cache_read_tokens + sqlc.arg(total_cache_read_tokens),
    total_cache_write_tokens = total_cache_write_tokens + sqlc.arg(total_cache_write_tokens),
    total_reasoning_tokens = total_reasoning_tokens + sqlc.arg(total_reasoning_tokens),
    cost_cents = cost_cents + sqlc.arg(cost_cents),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND runner_id = sqlc.narg(runner_id) AND status = 'running';
