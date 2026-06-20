-- +goose Up
ALTER TABLE chetter_tasks ADD INDEX idx_chetter_tasks_trigger_created (trigger_name, created_at);

-- +goose Down
ALTER TABLE chetter_tasks DROP INDEX idx_chetter_tasks_trigger_created;
