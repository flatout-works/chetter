-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN IF NOT EXISTS git_identity_id VARCHAR(64) NULL;

CREATE TABLE IF NOT EXISTS git_identities (
    id VARCHAR(64) PRIMARY KEY,
    team_id VARCHAR(64) NOT NULL DEFAULT '',
    name VARCHAR(128) NOT NULL,
    git_author_name VARCHAR(128) NOT NULL,
    git_author_email VARCHAR(255) NOT NULL,
    credential_type VARCHAR(32) NOT NULL DEFAULT 'github_app',
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_git_identities_team_name ON git_identities (team_id, name);
CREATE INDEX IF NOT EXISTS idx_git_identities_team ON git_identities (team_id);

-- +goose Down
DROP TABLE IF EXISTS git_identities;
ALTER TABLE chetter_tasks DROP COLUMN IF EXISTS git_identity_id;
