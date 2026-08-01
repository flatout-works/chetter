-- +goose Up
ALTER TABLE chetter_tasks
    ADD COLUMN github_repo VARCHAR(255) NULL AFTER git_ref;
ALTER TABLE chetter_tasks
    ADD COLUMN github_installation_id BIGINT NULL AFTER github_repo;

-- +goose Down
ALTER TABLE chetter_tasks
    DROP COLUMN github_installation_id,
    DROP COLUMN github_repo;
