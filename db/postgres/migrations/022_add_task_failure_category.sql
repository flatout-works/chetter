-- +goose Up
-- Add failure_category and failure_message columns to chetter_tasks
ALTER TABLE chetter_tasks
  ADD COLUMN failure_category VARCHAR(32) NULL,
  ADD COLUMN failure_message VARCHAR(500) NULL;

-- +goose Down
ALTER TABLE chetter_tasks
  DROP COLUMN failure_message,
  DROP COLUMN failure_category;
