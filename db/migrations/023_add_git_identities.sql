-- +goose Up
CREATE TABLE IF NOT EXISTS git_identities (
    id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NOT NULL DEFAULT '',
    name VARCHAR(128) NOT NULL,
    git_author_name VARCHAR(128) NOT NULL,
    git_author_email VARCHAR(255) NOT NULL,
    credential_type VARCHAR(32) NOT NULL DEFAULT 'github_app',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_git_identities_team_name (team_id, name),
    KEY idx_git_identities_team (team_id)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE chetter_tasks ADD COLUMN git_identity_id VARCHAR(64) NULL AFTER commit_author_email;

-- +goose Down
ALTER TABLE chetter_tasks DROP COLUMN git_identity_id;
DROP TABLE IF EXISTS git_identities;
