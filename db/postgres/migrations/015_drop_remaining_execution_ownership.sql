-- +goose Up
DROP INDEX IF EXISTS idx_user_prompts_required_runner;
ALTER TABLE chetter_user_prompts DROP COLUMN required_runner_id;
ALTER TABLE chetter_tasks DROP COLUMN checkpoint_after_success;

-- +goose Down
ALTER TABLE chetter_tasks
    ADD COLUMN checkpoint_after_success BOOLEAN NOT NULL DEFAULT false;

UPDATE chetter_tasks task
SET checkpoint_after_success = session.resume_mode <> 'none'
FROM chetter_agent_sessions session
WHERE session.task_id = task.id
  AND session.sequence = (
      SELECT MAX(latest.sequence)
      FROM chetter_agent_sessions latest
      WHERE latest.task_id = task.id
  );

ALTER TABLE chetter_user_prompts
    ADD COLUMN required_runner_id VARCHAR(64);

CREATE INDEX idx_user_prompts_required_runner
    ON chetter_user_prompts (required_runner_id, status);

UPDATE chetter_user_prompts prompt
SET required_runner_id = attempt.required_runner_id
FROM chetter_execution_attempts attempt
WHERE attempt.user_prompt_id = prompt.id
  AND attempt.sequence = 1;
