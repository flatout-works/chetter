-- +goose Up
-- Multi-replica coordination tables (eliminates single-replica assumption):
--   claim_notify_counter — single-row counter bumped after every task
--     becomes claimable, so idle ClaimTask long-polls on other replicas
--     re-check the queue without waiting for the safety-net poll.
--   trigger_locks — per-trigger rows locked via SELECT FOR UPDATE so
--     only one replica fires a cron trigger per tick.
--   admission_locks — single-row lock serializing the pending-task
--     admission check across replicas (replaces the in-process mutex).
--   runner_drain_requests — per-runner drain queue replacing the
--     in-process sync.Map so any replica can drain any runner.
CREATE TABLE IF NOT EXISTS claim_notify_counter (
    id INT NOT NULL DEFAULT 1,
    counter BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (id)
);
INSERT IGNORE INTO claim_notify_counter (id, counter) VALUES (1, 0);

CREATE TABLE IF NOT EXISTS trigger_locks (
    trigger_id VARCHAR(64) NOT NULL,
    last_triggered_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (trigger_id)
);

CREATE TABLE IF NOT EXISTS admission_locks (
    name VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (name)
);
INSERT IGNORE INTO admission_locks (name, created_at) VALUES ('pending_tasks', NOW(6));

CREATE TABLE IF NOT EXISTS runner_drain_requests (
    runner_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (runner_id)
);

-- +goose Down
DROP TABLE IF EXISTS runner_drain_requests;
DROP TABLE IF EXISTS admission_locks;
DROP TABLE IF EXISTS trigger_locks;
DROP TABLE IF EXISTS claim_notify_counter;
