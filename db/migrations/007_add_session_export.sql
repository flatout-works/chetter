-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN session_export MEDIUMTEXT NULL AFTER error;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN session_export;
