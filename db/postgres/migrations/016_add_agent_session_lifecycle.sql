-- +goose Up
ALTER TABLE chetter_agent_sessions
    ADD COLUMN summary TEXT NULL,
    ADD COLUMN started_at TIMESTAMPTZ NULL,
    ADD COLUMN ended_at TIMESTAMPTZ NULL;

UPDATE chetter_agent_sessions
SET started_at = created_at,
    ended_at = CASE
        WHEN status IN ('completed', 'error', 'abandoned', 'expired') THEN updated_at
        ELSE NULL
    END;

-- +goose Down
ALTER TABLE chetter_agent_sessions
    DROP COLUMN ended_at,
    DROP COLUMN started_at,
    DROP COLUMN summary;
