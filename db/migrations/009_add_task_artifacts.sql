-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_task_artifacts (
    id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64) NOT NULL,
    artifact_type VARCHAR(32) NOT NULL,
    repo VARCHAR(255) NOT NULL,
    number INT NULL,
    url TEXT NULL,
    ref VARCHAR(255) NULL,
    sha VARCHAR(64) NULL,
    created_at DATETIME(6) NOT NULL,
    discovered_at DATETIME(6) NOT NULL,
    discovery_source VARCHAR(32) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_task_artifacts_task (task_id),
    KEY idx_task_artifacts_type_repo (artifact_type, repo),
    KEY idx_task_artifacts_number (repo, number)
);

-- +goose Down
DROP TABLE IF EXISTS chetter_task_artifacts;
