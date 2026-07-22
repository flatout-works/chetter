-- +goose Up
ALTER TABLE teams ADD COLUMN okta_group_id VARCHAR(255) NULL AFTER name;
ALTER TABLE teams ADD COLUMN okta_group_name VARCHAR(255) NULL AFTER okta_group_id;

CREATE TABLE IF NOT EXISTS user_team_memberships (
    user_id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NOT NULL,
    source VARCHAR(32) NOT NULL DEFAULT 'manual',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (user_id, team_id),
    KEY idx_user_team_memberships_team (team_id)
);

CREATE TABLE IF NOT EXISTS api_token_teams (
    token_id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (token_id, team_id),
    KEY idx_api_token_teams_team (team_id)
);

INSERT IGNORE INTO user_team_memberships (user_id, team_id, source, created_at, updated_at)
SELECT id, team_id, 'manual', created_at, updated_at
FROM users
WHERE team_id IS NOT NULL AND team_id <> '';

INSERT IGNORE INTO api_token_teams (token_id, team_id, created_at)
SELECT t.id, u.team_id, t.created_at
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE u.team_id IS NOT NULL AND u.team_id <> '';

-- +goose Down
DROP TABLE IF EXISTS api_token_teams;
DROP TABLE IF EXISTS user_team_memberships;
ALTER TABLE teams DROP COLUMN okta_group_name;
ALTER TABLE teams DROP COLUMN okta_group_id;
