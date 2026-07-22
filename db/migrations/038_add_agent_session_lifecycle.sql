-- +goose Up
ALTER TABLE chetter_agent_sessions
    ADD COLUMN summary TEXT NULL AFTER pause_reason,
    ADD COLUMN started_at DATETIME(6) NULL AFTER error,
    ADD COLUMN ended_at DATETIME(6) NULL AFTER started_at;

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
