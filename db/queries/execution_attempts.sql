-- name: InsertExecutionAttempt :exec
INSERT INTO execution_attempts
    (id, user_prompt_id, sequence, status, runner_id, required_runner_id, claimed_at, lease_expires_at, timeout_sec, last_event_at, started_at, created_at, updated_at)
VALUES (?, ?, ?, 'running', ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertPendingExecutionAttempt :exec
INSERT INTO execution_attempts
    (id, user_prompt_id, sequence, status, required_runner_id, timeout_sec, created_at, updated_at)
VALUES (?, ?, ?, 'pending', ?, ?, ?, ?);

-- name: GetClaimableExecutionAttemptForUpdate :one
SELECT attempt.id AS execution_attempt_id, attempt.sequence,
       prompt.task_id, prompt.id AS user_prompt_id, task.id AS locked_task_id
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN tasks task ON task.id = prompt.task_id
WHERE attempt.status = 'pending'
  AND task.status = 'pending'
  AND (attempt.required_runner_id IS NULL OR attempt.required_runner_id = '' OR attempt.required_runner_id = sqlc.arg(runner_id))
  -- Isolation admission (issue #291): a task that requires enforced isolation
  -- is only claimable by a runner advertising it. The capability is passed in
  -- (resolved from the runner's heartbeat metadata before the locking read).
  -- The session check uses NOT EXISTS instead of a JOIN because TiDB
  -- serverless v8.5.x rejects SELECT ... FOR UPDATE SKIP LOCKED plans that
  -- join agent_sessions with Error 1105 "Can't find column
  -- agent_sessions.id in schema" (verified against the live fleet
  -- DB). The 3-table join without the session join works.
  AND (NOT EXISTS (
          SELECT 1 FROM agent_sessions session
          WHERE session.id = prompt.agent_session_id
            AND session.isolation_required = 1
      ) OR sqlc.arg(isolation_enabled) = 1)
ORDER BY attempt.created_at ASC
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: MarkExecutionAttemptClaimed :execrows
UPDATE execution_attempts
SET status = 'running', runner_id = sqlc.narg(runner_id), claim_id = sqlc.arg(claim_id), claimed_at = sqlc.narg(claimed_at),
    lease_expires_at = sqlc.narg(lease_expires_at), started_at = sqlc.narg(started_at),
    last_event_at = sqlc.narg(last_event_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'pending';

-- name: GetExecutionAttemptByID :one
SELECT * FROM execution_attempts WHERE id = ?;

-- name: ValidateRunnerExecutionClaim :one
SELECT attempt.id AS execution_attempt_id,
       attempt.claim_id,
       attempt.runner_id,
       attempt.status,
       attempt.lease_expires_at,
       prompt.task_id,
       prompt.agent_session_id,
       prompt.id AS user_prompt_id
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
WHERE attempt.id = sqlc.arg(execution_id)
  AND prompt.task_id = sqlc.arg(task_id)
  AND attempt.runner_id = sqlc.arg(runner_id)
  AND attempt.claim_id = sqlc.arg(claim_id)
  AND attempt.status = 'running'
  AND attempt.lease_expires_at > CURRENT_TIMESTAMP;

-- name: GetExecutionAttemptContext :one
SELECT attempt.id AS execution_attempt_id,
       prompt.id AS user_prompt_id,
       prompt.agent_session_id,
       prompt.task_id
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
WHERE attempt.id = ?;

-- name: GetGitHubExecutionContext :one
SELECT attempt.id AS execution_attempt_id,
       prompt.id AS user_prompt_id,
       prompt.agent_session_id,
       prompt.task_id,
       attempt.runner_id,
       attempt.status AS execution_status,
       attempt.lease_expires_at,
       task.status AS task_status,
       task.github_repo,
       task.github_installation_id,
       attempt.claim_id
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN tasks task ON task.id = prompt.task_id
WHERE attempt.id = ?;

-- name: ListExecutionAttemptsByPrompt :many
SELECT * FROM execution_attempts
WHERE user_prompt_id = ?
ORDER BY sequence ASC, created_at ASC;

-- name: GetNextExecutionAttemptSequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM execution_attempts
WHERE user_prompt_id = ?;

-- name: CountExecutionAttemptsByTask :one
SELECT COUNT(*)
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
WHERE prompt.task_id = ?;

-- name: GetExecutionAttemptUsageByTask :one
SELECT CAST(COALESCE(SUM(attempt.total_input_tokens), 0) AS SIGNED) AS total_input_tokens,
       CAST(COALESCE(SUM(attempt.total_output_tokens), 0) AS SIGNED) AS total_output_tokens,
       CAST(COALESCE(SUM(attempt.total_cache_read_tokens), 0) AS SIGNED) AS total_cache_read_tokens,
       CAST(COALESCE(SUM(attempt.total_cache_write_tokens), 0) AS SIGNED) AS total_cache_write_tokens,
       CAST(COALESCE(SUM(attempt.total_reasoning_tokens), 0) AS SIGNED) AS total_reasoning_tokens,
       CAST(COALESCE(SUM(attempt.cost_cents), 0) AS SIGNED) AS cost_cents
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
WHERE prompt.task_id = ?;

-- name: RenewExecutionAttemptLease :execrows
UPDATE execution_attempts
SET lease_expires_at = sqlc.narg(lease_expires_at), last_event_at = sqlc.narg(last_event_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND runner_id = sqlc.narg(runner_id) AND claim_id = sqlc.arg(claim_id)
  AND status = 'running' AND lease_expires_at > CURRENT_TIMESTAMP;

-- name: ExtendActiveExecutionAttemptTimeout :execrows
UPDATE execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
SET attempt.timeout_sec = attempt.timeout_sec + sqlc.arg(extension_sec),
    attempt.updated_at = sqlc.arg(updated_at)
WHERE prompt.task_id = sqlc.arg(task_id)
  AND attempt.status IN ('pending', 'running');

-- name: ListExecutionAttemptsForHeartbeat :many
SELECT attempt.id AS execution_attempt_id, attempt.claim_id, attempt.status, attempt.error,
       prompt.task_id, prompt.agent_session_id, prompt.id AS user_prompt_id
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
WHERE attempt.id IN (sqlc.slice(execution_ids))
  AND attempt.runner_id = sqlc.arg(runner_id);

-- name: MarkExecutionAttemptLost :execrows
UPDATE execution_attempts
SET status = 'lost', error = ?, ended_at = ?, updated_at = ?
WHERE id = ? AND status = 'running';

-- name: RequeueTaskAfterExecutionAttemptLost :execrows
UPDATE tasks
SET status = 'pending',
    summary = NULL,
    error = NULL,
    error_category = NULL,
    ended_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(task_id)
  AND status = 'running';

-- name: FailExpiredExecutionAttempts :execrows
UPDATE execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN tasks task ON task.id = prompt.task_id
JOIN (
    SELECT counted_prompt.task_id, COUNT(*) AS attempt_count
    FROM execution_attempts counted_attempt
    JOIN user_prompts counted_prompt ON counted_prompt.id = counted_attempt.user_prompt_id
    GROUP BY counted_prompt.task_id
) counts ON counts.task_id = task.id
SET attempt.status = 'error',
    attempt.error = CONCAT('runner lease expired after ', counts.attempt_count, ' attempts'),
    attempt.error_category = 'timeout',
    attempt.ended_at = sqlc.arg(ended_at),
    attempt.updated_at = sqlc.arg(updated_at)
WHERE attempt.status = 'running'
  AND attempt.lease_expires_at IS NOT NULL
  AND attempt.lease_expires_at < sqlc.arg(lease_expires_at)
  AND counts.attempt_count >= task.max_attempts;

-- name: FailAllExpiredExecutionAttempts :execrows
UPDATE execution_attempts attempt
SET attempt.status = 'error',
    attempt.error = 'runner lease expired; auto-recovery disabled',
    attempt.error_category = 'timeout',
    attempt.ended_at = sqlc.arg(ended_at),
    attempt.updated_at = sqlc.arg(updated_at)
WHERE attempt.status = 'running'
  AND attempt.lease_expires_at IS NOT NULL
  AND attempt.lease_expires_at < sqlc.arg(lease_expires_at);

-- name: CancelExecutionAttemptsByTask :execrows
UPDATE execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
SET attempt.status = 'cancelled',
    attempt.error = sqlc.arg(error),
    attempt.error_category = 'cancelled',
    attempt.lease_expires_at = NULL,
    attempt.ended_at = COALESCE(attempt.ended_at, sqlc.arg(ended_at)),
    attempt.updated_at = sqlc.arg(updated_at)
WHERE prompt.task_id = sqlc.arg(task_id)
  AND attempt.status IN ('pending', 'running');

-- name: CancelPendingExecutionAttempts :execrows
UPDATE execution_attempts
SET status = 'cancelled',
    error = sqlc.arg(error),
    error_category = 'cancelled',
    lease_expires_at = NULL,
    ended_at = COALESCE(ended_at, sqlc.arg(ended_at)),
    updated_at = sqlc.arg(updated_at)
WHERE status = 'pending';

-- name: FailPendingExecutionAttemptsForMissingRunner :execrows
UPDATE execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN agent_sessions session ON session.id = prompt.agent_session_id
LEFT JOIN runners runner ON runner.id = attempt.required_runner_id
SET attempt.status = 'error',
    attempt.error = CONCAT('pinned runner ', attempt.required_runner_id, ' is not alive'),
    attempt.error_category = 'runner_unavailable',
    attempt.ended_at = sqlc.arg(ended_at),
    attempt.updated_at = sqlc.arg(updated_at)
WHERE attempt.status = 'pending'
  AND session.status = 'resuming'
  AND attempt.required_runner_id IS NOT NULL
  AND attempt.required_runner_id <> ''
  AND (
    runner.id IS NULL
    OR runner.status <> 'active'
    OR runner.last_seen_at <= DATE_SUB(NOW(), INTERVAL sqlc.arg(stale_seconds) SECOND)
  );

-- name: ListReclaimableExecutionAttemptsForUpdate :many
SELECT attempt.id AS execution_attempt_id, prompt.task_id, prompt.id AS user_prompt_id,
       task.id AS locked_task_id, task.team_id, attempt.runner_id, attempt.sequence AS attempt,
       task.max_attempts, attempt.timeout_sec,
       attempt.lease_expires_at
FROM execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN tasks task ON task.id = prompt.task_id
WHERE attempt.status = 'running'
  AND attempt.lease_expires_at IS NOT NULL
  AND attempt.lease_expires_at < ?
ORDER BY attempt.lease_expires_at ASC
FOR UPDATE;

-- name: UpdateExecutionAttemptFromRunnerEvent :execrows
UPDATE execution_attempts
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
    runner_image_digest = COALESCE(NULLIF(sqlc.arg(runner_image_digest), ''), runner_image_digest),
    total_input_tokens = total_input_tokens + sqlc.arg(total_input_tokens),
    total_output_tokens = total_output_tokens + sqlc.arg(total_output_tokens),
    total_cache_read_tokens = total_cache_read_tokens + sqlc.arg(total_cache_read_tokens),
    total_cache_write_tokens = total_cache_write_tokens + sqlc.arg(total_cache_write_tokens),
    total_reasoning_tokens = total_reasoning_tokens + sqlc.arg(total_reasoning_tokens),
    cost_cents = cost_cents + sqlc.arg(cost_cents),
    last_event_at = sqlc.narg(last_event_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND runner_id = sqlc.narg(runner_id) AND claim_id = sqlc.arg(claim_id)
  AND status = 'running' AND lease_expires_at > CURRENT_TIMESTAMP;

-- name: FailPendingIsolationAttemptsWithoutCapableRunner :execrows
-- Fails pending attempts whose session requires enforced isolation when no
-- live runner advertises isolation_enabled. The task must never run
-- unsandboxed; without a capable runner it fails fast with
-- error_category isolation_unavailable instead of waiting forever. See issue
-- #291.
UPDATE execution_attempts attempt
JOIN user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN agent_sessions session ON session.id = prompt.agent_session_id
SET attempt.status = 'error',
    attempt.error = 'no active runner enforces isolation (gVisor) for this task',
    attempt.error_category = 'isolation_unavailable',
    attempt.ended_at = sqlc.arg(ended_at),
    attempt.updated_at = sqlc.arg(updated_at)
WHERE attempt.status = 'pending'
  AND session.isolation_required = 1
  AND NOT EXISTS (
      SELECT 1 FROM runners capable
      WHERE capable.isolation_enabled = 1
        AND capable.status = 'active'
        AND capable.last_seen_at > DATE_SUB(NOW(), INTERVAL sqlc.arg(stale_seconds) SECOND)
  );
