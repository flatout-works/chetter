-- +goose Up
ALTER TABLE chetter_user_prompts
    DROP INDEX idx_user_prompts_required_runner,
    DROP COLUMN required_runner_id;

ALTER TABLE chetter_tasks
    DROP COLUMN checkpoint_after_success;

-- +goose Down
ALTER TABLE chetter_tasks
    ADD COLUMN checkpoint_after_success BOOL NOT NULL DEFAULT false AFTER git_ref;

UPDATE chetter_tasks task
JOIN chetter_agent_sessions session ON session.task_id = task.id
SET task.checkpoint_after_success = session.resume_mode <> 'none'
WHERE session.sequence = (
    SELECT MAX(latest.sequence)
    FROM chetter_agent_sessions latest
    WHERE latest.task_id = task.id
);

ALTER TABLE chetter_user_prompts
    ADD COLUMN required_runner_id VARCHAR(64) NULL AFTER source_user_prompt_id,
    ADD KEY idx_user_prompts_required_runner (required_runner_id, status);

UPDATE chetter_user_prompts prompt
JOIN chetter_execution_attempts attempt ON attempt.user_prompt_id = prompt.id
SET prompt.required_runner_id = attempt.required_runner_id
WHERE attempt.sequence = 1;
