-- +goose Up
-- Generic inbound webhook endpoints (issue #120, epic #253 Phase 2).
-- webhook_endpoints materializes Git-managed inbound endpoint definitions
-- (global/webhooks/inbound/*.yaml and groups/<team>/webhooks/inbound/*.yaml)
-- into stable runtime identities with opaque public URLs. Secret values are
-- never stored: secret_env names the server environment variable holding the
-- HMAC-SHA256 or bearer secret.
CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id VARCHAR(64) NOT NULL,
    public_id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(16) NOT NULL,
    team_id VARCHAR(64) NULL,
    source_path VARCHAR(512) NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    auth_type VARCHAR(32) NOT NULL,
    secret_env VARCHAR(256) NOT NULL,
    signature_header VARCHAR(256) NULL,
    signature_prefix VARCHAR(64) NULL,
    delivery_id_header VARCHAR(128) NULL,
    event_type_header VARCHAR(128) NULL,
    accepted_events JSON NULL,
    action_type VARCHAR(32) NOT NULL DEFAULT 'create_task',
    action_prompt MEDIUMTEXT NOT NULL,
    action_agent VARCHAR(128) NULL,
    action_timeout_sec INT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_webhook_endpoints_public_id (public_id),
    UNIQUE KEY uq_webhook_endpoints_source_path (source_path),
    KEY idx_webhook_endpoints_team_created (team_id, created_at)
);

-- inbound_deliveries is the durable inbox for inbound webhook requests. A
-- request row is inserted before the receiver answers 202; a leased
-- multi-replica worker claims due rows and performs the endpoint action
-- (create_task) exactly once. Statuses: pending, processing, succeeded,
-- retry_wait, failed_permanent, dead_letter. Unique (endpoint_id,
-- delivery_id) rejects client replays; task_id persists the delivery/task
-- correlation so retries can never create a duplicate task.
CREATE TABLE IF NOT EXISTS inbound_deliveries (
    id VARCHAR(64) NOT NULL,
    endpoint_id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NULL,
    delivery_id VARCHAR(128) NULL,
    event_type VARCHAR(128) NULL,
    source_ip VARCHAR(64) NULL,
    payload MEDIUMTEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    error TEXT NULL,
    task_id VARCHAR(64) NULL,
    lease_expires_at DATETIME(6) NULL,
    next_attempt_at DATETIME(6) NULL,
    processed_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_inbound_deliveries_endpoint_delivery (endpoint_id, delivery_id),
    KEY idx_inbound_deliveries_due (status, next_attempt_at),
    KEY idx_inbound_deliveries_endpoint_created (endpoint_id, created_at),
    KEY idx_inbound_deliveries_team_created (team_id, created_at)
);

-- +goose Down
DROP TABLE IF EXISTS inbound_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
