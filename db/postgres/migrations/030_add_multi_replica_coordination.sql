-- +goose Up
-- Multi-replica coordination tables: see db/migrations/054 for details.
-- PostgreSQL types: BIGSERIAL is unnecessary for the single-row counter;
-- timestamps use TIMESTAMPTZ.
CREATE TABLE IF NOT EXISTS claim_notify_counter (
    id INT NOT NULL DEFAULT 1,
    counter BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (id)
);
INSERT INTO claim_notify_counter (id, counter)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS trigger_locks (
    trigger_id VARCHAR(64) NOT NULL,
    last_triggered_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (trigger_id)
);

CREATE TABLE IF NOT EXISTS admission_locks (
    name VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (name)
);
INSERT INTO admission_locks (name, created_at)
VALUES ('pending_tasks', NOW())
ON CONFLICT (name) DO NOTHING;

CREATE TABLE IF NOT EXISTS runner_drain_requests (
    runner_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (runner_id)
);

-- +goose Down
DROP TABLE IF EXISTS runner_drain_requests;
DROP TABLE IF EXISTS admission_locks;
DROP TABLE IF EXISTS trigger_locks;
DROP TABLE IF EXISTS claim_notify_counter;
