-- +goose Up
CREATE TABLE IF NOT EXISTS definition_sources (
    id VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    team_id VARCHAR(64) NULL,
    repo VARCHAR(255) NULL,
    repo_url TEXT NOT NULL,
    branch VARCHAR(255) NOT NULL,
    path VARCHAR(512) NOT NULL DEFAULT '',
    enabled BOOL NOT NULL DEFAULT true,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    last_sync_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_definition_sources_name (name),
    KEY idx_definition_sources_scope (scope, team_id, repo),
    KEY idx_definition_sources_enabled_updated (enabled, updated_at)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS definitions (
    id VARCHAR(64) NOT NULL,
    source_id VARCHAR(64) NOT NULL,
    definition_type VARCHAR(32) NOT NULL,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    team_id VARCHAR(64) NULL,
    repo VARCHAR(255) NULL,
    path VARCHAR(512) NOT NULL,
    source_commit VARCHAR(64) NOT NULL,
    content_hash CHAR(64) NOT NULL,
    content MEDIUMTEXT NOT NULL,
    metadata JSON NULL,
    active BOOL NOT NULL DEFAULT true,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_definitions_source_type_name_path (source_id, definition_type, name, path),
    KEY idx_definitions_lookup (definition_type, name, active, scope),
    KEY idx_definitions_source_active (source_id, active, updated_at),
    KEY idx_definitions_hash (content_hash)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS definition_sync_runs (
    id VARCHAR(64) NOT NULL,
    source_id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    source_commit VARCHAR(64) NULL,
    definitions_count INT NOT NULL DEFAULT 0,
    error TEXT NULL,
    started_at DATETIME(6) NOT NULL,
    ended_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_definition_sync_runs_source_created (source_id, created_at),
    KEY idx_definition_sync_runs_status_created (status, created_at)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS definition_sync_runs;
DROP TABLE IF EXISTS definitions;
DROP TABLE IF EXISTS definition_sources;
