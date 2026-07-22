-- +goose Up
ALTER TABLE chetter_task_events
    ADD COLUMN agent_session_id VARCHAR(64) NULL AFTER task_id;
ALTER TABLE chetter_task_events
    ADD COLUMN user_prompt_id VARCHAR(64) NULL AFTER agent_session_id;
ALTER TABLE chetter_task_events
    ADD COLUMN execution_attempt_id VARCHAR(64) NULL AFTER user_prompt_id;
ALTER TABLE chetter_task_events
    ADD KEY idx_task_events_hierarchy (agent_session_id, user_prompt_id, execution_attempt_id);

-- +goose Down
ALTER TABLE chetter_task_events DROP INDEX idx_task_events_hierarchy;
ALTER TABLE chetter_task_events
    DROP COLUMN execution_attempt_id;
ALTER TABLE chetter_task_events
    DROP COLUMN user_prompt_id;
ALTER TABLE chetter_task_events
    DROP COLUMN agent_session_id;
