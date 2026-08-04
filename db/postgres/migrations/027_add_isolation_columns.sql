-- +goose Up
-- Isolation policy columns (issue #291): see db/migrations/051 for details.
ALTER TABLE chetter_agent_sessions
    ADD COLUMN IF NOT EXISTS isolation_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chetter_runners
    ADD COLUMN IF NOT EXISTS isolation_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE chetter_runners
    DROP COLUMN IF EXISTS isolation_enabled;
ALTER TABLE chetter_agent_sessions
    DROP COLUMN IF EXISTS isolation_required;
