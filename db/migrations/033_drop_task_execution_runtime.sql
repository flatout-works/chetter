-- +goose Up
ALTER TABLE chetter_tasks
    DROP INDEX idx_chetter_tasks_claim,
    DROP INDEX idx_chetter_tasks_runner,
    DROP INDEX idx_chetter_tasks_required_runner;

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
    ADD COLUMN runner_id VARCHAR(64) NULL,
    ADD COLUMN required_runner_id VARCHAR(64) NULL,
    ADD COLUMN claimed_at DATETIME(6) NULL,
    ADD COLUMN lease_expires_at DATETIME(6) NULL,
    ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN attempt INT NOT NULL DEFAULT 0,
    ADD COLUMN last_event_at DATETIME(6) NULL,
    ADD COLUMN started_at DATETIME(6) NULL,
    ADD COLUMN runner_image_digest VARCHAR(255) NULL,
    ADD COLUMN timeout_sec INT NOT NULL DEFAULT 600;

ALTER TABLE chetter_tasks
    ADD INDEX idx_chetter_tasks_claim (status, lease_expires_at, created_at),
    ADD INDEX idx_chetter_tasks_runner (runner_id, status),
    ADD INDEX idx_chetter_tasks_required_runner (required_runner_id, status, created_at);
