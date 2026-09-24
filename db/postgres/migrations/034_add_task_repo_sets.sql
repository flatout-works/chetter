-- +goose Up
-- Multi-repo task support (issue #434): see db/migrations/058 for details.
-- The repos column stores the task's repository set as a JSON array of
-- {url, ref, primary} entries, primary first.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS repos JSONB NULL;
ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS repos JSONB NULL;

-- +goose Down
ALTER TABLE agent_sessions DROP COLUMN IF EXISTS repos;
ALTER TABLE tasks DROP COLUMN IF EXISTS repos;
