-- +goose Up
ALTER TABLE chetter_tasks
    ADD COLUMN IF NOT EXISTS execution_id VARCHAR(64) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chetter_tasks
    DROP COLUMN IF EXISTS execution_id;
