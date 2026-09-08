-- +goose Up
-- Durable outbound delivery queue for webhook/slack event-callback actions
-- (issue #357). Delivery rows are written in the same transaction as their
-- task_events row (outbox pattern) and claimed by a leased multi-replica
-- worker. Unique (callback_id, event_id) makes replays idempotent.
CREATE TABLE IF NOT EXISTS callback_deliveries (
    id VARCHAR(64) NOT NULL,
    callback_id VARCHAR(64) NOT NULL,
    event_id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64) NULL,
    team_id VARCHAR(64) NULL,
    event_type VARCHAR(64) NOT NULL,
    endpoint_url TEXT NOT NULL,
    method VARCHAR(16) NOT NULL DEFAULT 'POST',
    headers TEXT NULL,
    payload MEDIUMTEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    error TEXT NULL,
    lease_expires_at DATETIME(6) NULL,
    next_attempt_at DATETIME(6) NULL,
    processed_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_callback_deliveries_callback_event (callback_id, event_id),
    KEY idx_callback_deliveries_due (status, next_attempt_at),
    KEY idx_callback_deliveries_created (created_at)
);

-- +goose Down
DROP TABLE IF EXISTS callback_deliveries;
