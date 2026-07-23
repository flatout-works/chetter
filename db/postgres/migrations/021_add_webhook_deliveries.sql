-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_webhook_deliveries (
    id VARCHAR(64) NOT NULL,
    delivery_id VARCHAR(64) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    event_action VARCHAR(64) NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'received',
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    error TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NULL,
    processed_at TIMESTAMPTZ NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_webhook_deliveries_delivery_id UNIQUE (delivery_id)
);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status_next ON chetter_webhook_deliveries (status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_created ON chetter_webhook_deliveries (created_at);

-- +goose Down
DROP TABLE IF EXISTS chetter_webhook_deliveries;
