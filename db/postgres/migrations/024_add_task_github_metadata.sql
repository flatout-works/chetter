-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN github_repo VARCHAR(255) NULL;
ALTER TABLE chetter_tasks ADD COLUMN github_installation_id BIGINT NULL;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN github_installation_id;
ALTER TABLE chetter_tasks DROP COLUMN github_repo;
