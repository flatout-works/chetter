-- +goose Up
DROP INDEX IF EXISTS idx_chetter_tasks_claim;
DROP INDEX IF EXISTS idx_chetter_tasks_runner;
DROP INDEX IF EXISTS idx_chetter_tasks_required_runner;

ALTER TABLE chetter_tasks
    DROP COLUMN runner_id,
    DROP COLUMN required_runner_id,
    DROP COLUMN claimed_at,
    DROP COLUMN lease_expires_at,
    DROP COLUMN execution_id,
    DROP COLUMN attempt,
    DROP COLUMN last_event_at,
    DROP COLUMN started_at,
    DROP COLUMN runner_image_digest,
    DROP COLUMN timeout_sec;

-- +goose Down
ALTER TABLE chetter_tasks
    ADD COLUMN runner_id VARCHAR(64),
    ADD COLUMN required_runner_id VARCHAR(64),
    ADD COLUMN claimed_at TIMESTAMPTZ,
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_event_at TIMESTAMPTZ,
    ADD COLUMN started_at TIMESTAMPTZ,
    ADD COLUMN runner_image_digest VARCHAR(255),
    ADD COLUMN timeout_sec INTEGER NOT NULL DEFAULT 600;

CREATE INDEX idx_chetter_tasks_claim ON chetter_tasks (status, lease_expires_at, created_at);
CREATE INDEX idx_chetter_tasks_runner ON chetter_tasks (runner_id, status);
CREATE INDEX idx_chetter_tasks_required_runner ON chetter_tasks (required_runner_id, status, created_at);
