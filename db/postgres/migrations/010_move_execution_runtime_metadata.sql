-- +goose Up
ALTER TABLE chetter_execution_attempts
    ADD COLUMN IF NOT EXISTS timeout_sec INTEGER NOT NULL DEFAULT 600,
    ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS runner_image_digest VARCHAR(255);

UPDATE chetter_execution_attempts attempt
SET timeout_sec = task.timeout_sec,
    last_event_at = task.last_event_at,
    runner_image_digest = task.runner_image_digest
FROM chetter_user_prompts prompt, chetter_tasks task
WHERE prompt.id = attempt.user_prompt_id
  AND task.id = prompt.task_id;

-- +goose Down
ALTER TABLE chetter_execution_attempts
    DROP COLUMN IF EXISTS runner_image_digest,
    DROP COLUMN IF EXISTS last_event_at,
    DROP COLUMN IF EXISTS timeout_sec;
