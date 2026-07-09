-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN IF NOT EXISTS mcp_profiles JSON NULL AFTER skills;
UPDATE chetter_tasks SET mcp_profiles = JSON_ARRAY() WHERE mcp_profiles IS NULL;
ALTER TABLE chetter_tasks MODIFY COLUMN mcp_profiles JSON NOT NULL;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN IF EXISTS mcp_profiles;
