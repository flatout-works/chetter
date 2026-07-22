-- +goose Up
ALTER TABLE chetter_task_events
    ADD COLUMN agent_session_id VARCHAR(64),
    ADD COLUMN user_prompt_id VARCHAR(64),
    ADD COLUMN execution_attempt_id VARCHAR(64);
CREATE INDEX idx_task_events_hierarchy
    ON chetter_task_events (agent_session_id, user_prompt_id, execution_attempt_id);

-- +goose Down
DROP INDEX IF EXISTS idx_task_events_hierarchy;
ALTER TABLE chetter_task_events
    DROP COLUMN IF EXISTS execution_attempt_id,
    DROP COLUMN IF EXISTS user_prompt_id,
    DROP COLUMN IF EXISTS agent_session_id;
