-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN IF NOT EXISTS mcp_profiles JSON NULL AFTER skills;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN IF EXISTS mcp_profiles;
