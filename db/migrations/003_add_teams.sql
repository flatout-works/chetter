-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN IF NOT EXISTS team_id VARCHAR(64) NULL AFTER id;
ALTER TABLE chetter_schedules ADD COLUMN IF NOT EXISTS team_id VARCHAR(64) NULL AFTER id;

CREATE TABLE IF NOT EXISTS teams (
    id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_teams_name (name)
);

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    team_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_users_team (team_id)
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    token_hash CHAR(64) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_api_tokens_hash (token_hash),
    KEY idx_api_tokens_user (user_id)
);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS teams;
ALTER TABLE chetter_tasks DROP COLUMN IF EXISTS team_id;
ALTER TABLE chetter_schedules DROP COLUMN IF EXISTS team_id;
