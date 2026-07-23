-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_webhook_deliveries (
    id VARCHAR(64) NOT NULL,
    delivery_id VARCHAR(64) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    event_action VARCHAR(64) NOT NULL DEFAULT '',
    payload MEDIUMTEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'received',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    error TEXT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    next_attempt_at DATETIME(6) NULL,
    processed_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_webhook_deliveries_delivery_id (delivery_id),
    KEY idx_webhook_deliveries_status_next (status, next_attempt_at),
    KEY idx_webhook_deliveries_created (created_at)
);

-- +goose Down
DROP TABLE IF EXISTS chetter_webhook_deliveries;
