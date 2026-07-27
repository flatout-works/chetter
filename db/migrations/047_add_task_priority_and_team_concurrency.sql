-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN priority INT NOT NULL DEFAULT 0 AFTER status;
ALTER TABLE teams ADD COLUMN max_concurrent_tasks INT NOT NULL DEFAULT 0 AFTER name;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN priority;
ALTER TABLE teams DROP COLUMN max_concurrent_tasks;
