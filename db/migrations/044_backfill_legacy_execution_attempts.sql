-- +goose Up
-- Preserve terminal prompt history created before execution attempts were recorded.
INSERT INTO chetter_execution_attempts
    (id, user_prompt_id, sequence, status, claimed_at, started_at, ended_at,
     summary, error, session_export, created_at, updated_at)
SELECT
    CONCAT('exec_legacy_', LEFT(SHA2(prompt.id, 256), 32)),
    prompt.id,
    1,
    CASE
        WHEN prompt.status IN ('done', 'completed') THEN 'succeeded'
        WHEN prompt.status IN ('error', 'failed') THEN 'failed'
        WHEN prompt.status = 'cancelled' THEN 'cancelled'
    END,
    COALESCE(prompt.started_at, prompt.created_at),
    prompt.started_at,
    COALESCE(prompt.ended_at, prompt.updated_at),
    prompt.summary,
    prompt.error,
    prompt.session_export,
    prompt.created_at,
    prompt.updated_at
FROM chetter_user_prompts prompt
LEFT JOIN chetter_execution_attempts attempt
    ON attempt.user_prompt_id = prompt.id
WHERE attempt.id IS NULL
  AND prompt.status IN ('done', 'completed', 'error', 'failed', 'cancelled');

-- +goose Down
DELETE attempt
FROM chetter_execution_attempts attempt
JOIN chetter_user_prompts prompt ON prompt.id = attempt.user_prompt_id
WHERE attempt.id = CONCAT('exec_legacy_', LEFT(SHA2(prompt.id, 256), 32));
