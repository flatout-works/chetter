-- +goose Up
CREATE TABLE IF NOT EXISTS definition_change_proposals (
    id VARCHAR(64) NOT NULL,
    source_id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64) NULL,
    repo VARCHAR(255) NOT NULL,
    branch VARCHAR(255) NOT NULL,
    base_branch VARCHAR(255) NOT NULL,
    pr_number INT NOT NULL,
    pr_url TEXT NOT NULL,
    title VARCHAR(255) NOT NULL,
    body MEDIUMTEXT NOT NULL,
    files JSON NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_definition_proposals_repo_pr (repo, pr_number),
    KEY idx_definition_proposals_source_created (source_id, created_at),
    KEY idx_definition_proposals_status_created (status, created_at),
    KEY idx_definition_proposals_task (task_id)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS definition_change_proposals;
