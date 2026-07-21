-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN IF NOT EXISTS mcp_endpoints JSON NULL AFTER skills;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN IF EXISTS mcp_endpoints;
