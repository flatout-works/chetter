-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN submission_source VARCHAR(32) NOT NULL DEFAULT 'manual' AFTER trigger_type;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN submission_source;
