-- +goose Up
-- Generic inbound webhook endpoints (issue #120, epic #253 Phase 2). See the
-- MySQL/TiDB migration 056 for the full rationale. PostgreSQL dialect of the
-- same schema: TIMESTAMPTZ timestamps, TEXT payloads, JSONB for structured
-- columns, and standalone indexes.
CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id VARCHAR(64) NOT NULL,
    public_id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(16) NOT NULL,
    team_id VARCHAR(64) NULL,
    source_path VARCHAR(512) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    auth_type VARCHAR(32) NOT NULL,
    secret_env VARCHAR(256) NOT NULL,
    signature_header VARCHAR(256) NULL,
    signature_prefix VARCHAR(64) NULL,
    delivery_id_header VARCHAR(128) NULL,
    event_type_header VARCHAR(128) NULL,
    accepted_events JSONB NULL,
    action_type VARCHAR(32) NOT NULL DEFAULT 'create_task',
    action_prompt TEXT NOT NULL,
    action_agent VARCHAR(128) NULL,
    action_timeout_sec INT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_webhook_endpoints_public_id ON webhook_endpoints (public_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_webhook_endpoints_source_path ON webhook_endpoints (source_path);
CREATE INDEX IF NOT EXISTS idx_webhook_endpoints_team_created ON webhook_endpoints (team_id, created_at);

CREATE TABLE IF NOT EXISTS inbound_deliveries (
    id VARCHAR(64) NOT NULL,
    endpoint_id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NULL,
    delivery_id VARCHAR(128) NULL,
    event_type VARCHAR(128) NULL,
    source_ip VARCHAR(64) NULL,
    payload TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    error TEXT NULL,
    task_id VARCHAR(64) NULL,
    lease_expires_at TIMESTAMPTZ NULL,
    next_attempt_at TIMESTAMPTZ NULL,
    processed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inbound_deliveries_endpoint_delivery ON inbound_deliveries (endpoint_id, delivery_id);
CREATE INDEX IF NOT EXISTS idx_inbound_deliveries_due ON inbound_deliveries (status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_inbound_deliveries_endpoint_created ON inbound_deliveries (endpoint_id, created_at);
CREATE INDEX IF NOT EXISTS idx_inbound_deliveries_team_created ON inbound_deliveries (team_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS inbound_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
