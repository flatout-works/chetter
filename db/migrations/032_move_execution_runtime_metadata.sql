-- +goose Up
ALTER TABLE chetter_execution_attempts
    ADD COLUMN IF NOT EXISTS timeout_sec INT NOT NULL DEFAULT 600 AFTER lease_expires_at,
    ADD COLUMN IF NOT EXISTS last_event_at DATETIME(6) NULL AFTER timeout_sec,
    ADD COLUMN IF NOT EXISTS runner_image_digest VARCHAR(255) NULL AFTER harness_execution_id;

UPDATE chetter_execution_attempts attempt
JOIN chetter_user_prompts prompt ON prompt.id = attempt.user_prompt_id
JOIN chetter_tasks task ON task.id = prompt.task_id
SET attempt.timeout_sec = task.timeout_sec,
    attempt.last_event_at = task.last_event_at,
    attempt.runner_image_digest = task.runner_image_digest;

-- +goose Down
ALTER TABLE chetter_execution_attempts
    DROP COLUMN IF EXISTS runner_image_digest,
    DROP COLUMN IF EXISTS last_event_at,
    DROP COLUMN IF EXISTS timeout_sec;
