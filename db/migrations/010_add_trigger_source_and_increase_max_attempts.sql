-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN trigger_name VARCHAR(128) NULL AFTER runner_id;
ALTER TABLE chetter_tasks ADD COLUMN trigger_type VARCHAR(32) NULL AFTER trigger_name;
ALTER TABLE chetter_tasks MODIFY COLUMN max_attempts INT NOT NULL DEFAULT 5;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN trigger_type;
ALTER TABLE chetter_tasks DROP COLUMN trigger_name;
ALTER TABLE chetter_tasks MODIFY COLUMN max_attempts INT NOT NULL DEFAULT 3;
