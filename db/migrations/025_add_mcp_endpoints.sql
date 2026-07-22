-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN mcp_endpoints JSON NULL AFTER skills;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN mcp_endpoints;
