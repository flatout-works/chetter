-- +goose Up
ALTER TABLE chetter_tasks
    DROP COLUMN opencode_session_id,
    DROP COLUMN session_export;

-- +goose Down
ALTER TABLE chetter_tasks
    ADD COLUMN opencode_session_id VARCHAR(128),
    ADD COLUMN session_export TEXT;
