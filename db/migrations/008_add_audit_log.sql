-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_audit_log (
    id VARCHAR(64) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    source_type VARCHAR(32) NULL,
    source_id VARCHAR(128) NULL,
    target_type VARCHAR(32) NULL,
    target_id VARCHAR(128) NULL,
    repo VARCHAR(255) NULL,
    github_event VARCHAR(64) NULL,
    github_action VARCHAR(64) NULL,
    github_delivery_id VARCHAR(128) NULL,
    parent_event_id VARCHAR(64) NULL,
    detail TEXT NULL,
    payload JSON NULL,
    PRIMARY KEY (id),
    KEY idx_audit_log_created (created_at),
    KEY idx_audit_log_source (source_type, source_id),
    KEY idx_audit_log_target (target_type, target_id),
    KEY idx_audit_log_repo (repo, created_at),
    KEY idx_audit_log_parent (parent_event_id)
);

-- +goose Down
DROP TABLE IF EXISTS chetter_audit_log;
