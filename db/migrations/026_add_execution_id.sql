-- +goose Up
ALTER TABLE chetter_tasks
    ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '' AFTER attempt;

-- +goose Down
ALTER TABLE chetter_tasks
    DROP COLUMN execution_id;
