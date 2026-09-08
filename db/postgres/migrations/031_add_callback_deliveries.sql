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
    payload TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    error TEXT NULL,
    lease_expires_at TIMESTAMPTZ NULL,
    next_attempt_at TIMESTAMPTZ NULL,
    processed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_callback_deliveries_callback_event UNIQUE (callback_id, event_id)
);
CREATE INDEX IF NOT EXISTS idx_callback_deliveries_due ON callback_deliveries (status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_callback_deliveries_created ON callback_deliveries (created_at);

-- +goose Down
DROP TABLE IF EXISTS callback_deliveries;
