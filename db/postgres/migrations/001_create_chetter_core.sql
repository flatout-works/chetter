-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_tasks (
    id VARCHAR(64) PRIMARY KEY,
    team_id VARCHAR(64),
    status VARCHAR(32) NOT NULL,
    prompt TEXT NOT NULL,
    git_url TEXT,
    git_ref VARCHAR(255),
    agent_image VARCHAR(512),
    agent VARCHAR(128),
    provider_id VARCHAR(128),
    model_id VARCHAR(255),
    variant_id VARCHAR(128),
    opencode_session_id VARCHAR(128),
    runner_image_digest VARCHAR(255),
    commit_author_name VARCHAR(128),
    commit_author_email VARCHAR(255),
    runner_id VARCHAR(64),
    required_runner_id VARCHAR(64),
    checkpoint_after_success BOOLEAN NOT NULL DEFAULT false,
    trigger_name VARCHAR(128),
    trigger_type VARCHAR(32),
    submission_source VARCHAR(32) NOT NULL DEFAULT 'manual',
    claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    skills JSONB NOT NULL,
    env JSONB NOT NULL,
    timeout_sec INTEGER NOT NULL,
    summary TEXT,
    error TEXT,
    error_category VARCHAR(32),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_event_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    session_export TEXT,
    search_text TEXT,
    total_input_tokens BIGINT NOT NULL DEFAULT 0,
    total_output_tokens BIGINT NOT NULL DEFAULT 0,
    total_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    total_cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    total_reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    cost_cents BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS chetter_agent_sessions (
    id VARCHAR(64) PRIMARY KEY,
    team_id VARCHAR(64),
    status VARCHAR(32) NOT NULL,
    resume_mode VARCHAR(32) NOT NULL DEFAULT 'none',
    pinned_runner_id VARCHAR(64),
    pinned_runner_name VARCHAR(128),
    checkpoint_id VARCHAR(64),
    workspace_path TEXT,
    container_name VARCHAR(128),
    harness_session_id VARCHAR(128),
    git_url TEXT,
    git_ref VARCHAR(255),
    agent_image VARCHAR(512),
    agent VARCHAR(128),
    provider_id VARCHAR(128),
    model_id VARCHAR(255),
    variant_id VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    paused_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    pause_reason VARCHAR(64),
    error TEXT,
    search_text TEXT
);

CREATE TABLE IF NOT EXISTS chetter_session_runs (
    id VARCHAR(64) PRIMARY KEY,
    agent_session_id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    prompt TEXT NOT NULL,
    required_runner_id VARCHAR(64),
    summary TEXT,
    error TEXT,
    session_export TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS chetter_agent_session_checkpoints (
    id VARCHAR(64) PRIMARY KEY,
    agent_session_id VARCHAR(64) NOT NULL,
    session_run_id VARCHAR(64),
    runner_id VARCHAR(64) NOT NULL,
    checkpoint_path TEXT NOT NULL,
    workspace_path TEXT NOT NULL,
    container_name VARCHAR(128),
    runsc_version VARCHAR(128),
    agent_image VARCHAR(512),
    size_bytes BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS chetter_task_events (
    id VARCHAR(64) PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    event_type VARCHAR(64) NOT NULL DEFAULT 'task.progress',
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS chetter_event_callbacks (
    id VARCHAR(64) PRIMARY KEY,
    team_id VARCHAR(64),
    name VARCHAR(255) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    action_type VARCHAR(32) NOT NULL,
    action_config JSONB NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS chetter_runners (
    id VARCHAR(64) PRIMARY KEY,
    status VARCHAR(32) NOT NULL,
    image_ref VARCHAR(512),
    image_digest VARCHAR(255),
    version VARCHAR(128),
    max_concurrent INTEGER NOT NULL DEFAULT 0,
    running_tasks INTEGER NOT NULL DEFAULT 0,
    available_slots INTEGER NOT NULL DEFAULT 0,
    total_started BIGINT NOT NULL DEFAULT 0,
    total_completed BIGINT NOT NULL DEFAULT 0,
    total_errors BIGINT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    metadata JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS chetter_triggers (
    id VARCHAR(64) PRIMARY KEY,
    team_id VARCHAR(64),
    name VARCHAR(128) NOT NULL,
    trigger_type VARCHAR(32) NOT NULL DEFAULT 'cron',
    trigger_config JSONB NOT NULL,
    cron_expr VARCHAR(128) NOT NULL,
    prompt TEXT NOT NULL,
    git_url TEXT,
    git_ref VARCHAR(255),
    agent_image VARCHAR(512),
    agent VARCHAR(128),
    provider_id VARCHAR(128),
    model_id VARCHAR(255),
    variant_id VARCHAR(128),
    harness VARCHAR(64),
    skills JSONB NOT NULL,
    timeout_sec INTEGER NOT NULL,
    enabled BOOLEAN NOT NULL,
    source_id VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS chetter_trigger_runs (
    id VARCHAR(64) PRIMARY KEY,
    trigger_id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64),
    task_id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    triggered_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS teams (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    okta_group_id VARCHAR(255),
    okta_group_name VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    team_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    token_hash CHAR(64) NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS user_team_memberships (
    user_id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NOT NULL,
    source VARCHAR(32) NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, team_id)
);

CREATE TABLE IF NOT EXISTS api_token_teams (
    token_id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (token_id, team_id)
);

CREATE TABLE IF NOT EXISTS chetter_audit_log (
    id VARCHAR(64) PRIMARY KEY,
    event_type VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    source_type VARCHAR(32),
    source_id VARCHAR(255),
    target_type VARCHAR(32),
    target_id VARCHAR(255),
    repo VARCHAR(255),
    github_event VARCHAR(64),
    github_action VARCHAR(64),
    github_delivery_id VARCHAR(64),
    parent_event_id VARCHAR(64),
    detail TEXT,
    search_text TEXT,
    payload JSONB,
    token_id VARCHAR(64),
    token_name VARCHAR(128)
);

CREATE TABLE IF NOT EXISTS chetter_task_artifacts (
    id VARCHAR(64) PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL,
    agent_session_id VARCHAR(64),
    session_run_id VARCHAR(64),
    artifact_type VARCHAR(32) NOT NULL,
    repo VARCHAR(255) NOT NULL,
    number INTEGER,
    url TEXT,
    ref VARCHAR(255),
    sha VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL,
    discovered_at TIMESTAMPTZ NOT NULL,
    discovery_source VARCHAR(32) NOT NULL,
    search_text TEXT
);

CREATE TABLE IF NOT EXISTS chetter_model_catalogs (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT false,
    source VARCHAR(255),
    checksum CHAR(64) NOT NULL,
    yaml TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS definition_sources (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    team_id VARCHAR(64),
    repo VARCHAR(255),
    repo_url TEXT NOT NULL,
    branch VARCHAR(255) NOT NULL,
    path VARCHAR(512) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_sync_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS definitions (
    id VARCHAR(64) PRIMARY KEY,
    source_id VARCHAR(64) NOT NULL,
    definition_type VARCHAR(32) NOT NULL,
    name VARCHAR(128) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    team_id VARCHAR(64),
    repo VARCHAR(255),
    path VARCHAR(512) NOT NULL,
    source_commit VARCHAR(64) NOT NULL,
    content_hash CHAR(64) NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS definition_sync_runs (
    id VARCHAR(64) PRIMARY KEY,
    source_id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    source_commit VARCHAR(64),
    definitions_count INTEGER NOT NULL DEFAULT 0,
    error TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS definition_change_proposals (
    id VARCHAR(64) PRIMARY KEY,
    source_id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64),
    repo VARCHAR(255) NOT NULL,
    branch VARCHAR(255) NOT NULL,
    base_branch VARCHAR(255) NOT NULL,
    pr_number INTEGER NOT NULL,
    pr_url TEXT NOT NULL,
    title VARCHAR(255) NOT NULL,
    body TEXT NOT NULL,
    files JSONB NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chetter_tasks_status_created ON chetter_tasks USING btree (status, created_at);
CREATE INDEX IF NOT EXISTS idx_chetter_tasks_created ON chetter_tasks USING btree (created_at);
CREATE INDEX IF NOT EXISTS idx_chetter_tasks_claim ON chetter_tasks USING btree (status, lease_expires_at, created_at);
CREATE INDEX IF NOT EXISTS idx_chetter_tasks_runner ON chetter_tasks USING btree (runner_id, status);
CREATE INDEX IF NOT EXISTS idx_chetter_tasks_trigger_created ON chetter_tasks USING btree (trigger_name, created_at);
CREATE INDEX IF NOT EXISTS idx_chetter_tasks_required_runner ON chetter_tasks USING btree (required_runner_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_search ON chetter_tasks USING gin (to_tsvector('simple', COALESCE(search_text, '')));
CREATE INDEX IF NOT EXISTS idx_agent_sessions_team_status ON chetter_agent_sessions USING btree (team_id, status, updated_at);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_runner_status ON chetter_agent_sessions USING btree (pinned_runner_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_expires ON chetter_agent_sessions USING btree (expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_search ON chetter_agent_sessions USING gin (to_tsvector('simple', COALESCE(search_text, '')));
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_runs_task ON chetter_session_runs USING btree (task_id);
CREATE INDEX IF NOT EXISTS idx_session_runs_session_created ON chetter_session_runs USING btree (agent_session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_session_runs_status_created ON chetter_session_runs USING btree (status, created_at);
CREATE INDEX IF NOT EXISTS idx_session_runs_required_runner ON chetter_session_runs USING btree (required_runner_id, status);
CREATE INDEX IF NOT EXISTS idx_session_checkpoints_session_created ON chetter_agent_session_checkpoints USING btree (agent_session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_session_checkpoints_runner_status ON chetter_agent_session_checkpoints USING btree (runner_id, status);
CREATE INDEX IF NOT EXISTS idx_session_checkpoints_expires ON chetter_agent_session_checkpoints USING btree (expires_at);
CREATE INDEX IF NOT EXISTS idx_chetter_task_events_task_created ON chetter_task_events USING btree (task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_chetter_task_events_type_created ON chetter_task_events USING btree (event_type, created_at);
CREATE INDEX IF NOT EXISTS idx_chetter_task_events_created ON chetter_task_events USING btree (created_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_event_callbacks_team_name ON chetter_event_callbacks USING btree (team_id, name);
CREATE INDEX IF NOT EXISTS idx_event_callbacks_team_enabled ON chetter_event_callbacks USING btree (team_id, enabled);
CREATE INDEX IF NOT EXISTS idx_event_callbacks_event_type ON chetter_event_callbacks USING btree (event_type, enabled);
CREATE INDEX IF NOT EXISTS idx_chetter_runners_status_seen ON chetter_runners USING btree (status, last_seen_at);
CREATE INDEX IF NOT EXISTS idx_chetter_runners_digest_seen ON chetter_runners USING btree (image_digest, last_seen_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_chetter_triggers_name ON chetter_triggers USING btree (name);
CREATE INDEX IF NOT EXISTS idx_chetter_triggers_enabled_next ON chetter_triggers USING btree (enabled, next_run_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_trigger_runs_dedup ON chetter_trigger_runs USING btree (trigger_id, task_id);
CREATE INDEX IF NOT EXISTS idx_chetter_trigger_runs_trigger_created ON chetter_trigger_runs USING btree (trigger_id, created_at);
CREATE INDEX IF NOT EXISTS idx_chetter_trigger_runs_task ON chetter_trigger_runs USING btree (task_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_teams_name ON teams USING btree (name);
CREATE UNIQUE INDEX IF NOT EXISTS uq_teams_okta_group_id ON teams USING btree (okta_group_id);
CREATE INDEX IF NOT EXISTS idx_users_team ON users USING btree (team_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_api_tokens_hash ON api_tokens USING btree (token_hash);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens USING btree (user_id);
CREATE INDEX IF NOT EXISTS idx_user_team_memberships_team ON user_team_memberships USING btree (team_id);
CREATE INDEX IF NOT EXISTS idx_api_token_teams_team ON api_token_teams USING btree (team_id);
CREATE INDEX IF NOT EXISTS idx_audit_event_type ON chetter_audit_log USING btree (event_type, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_source ON chetter_audit_log USING btree (source_type, source_id);
CREATE INDEX IF NOT EXISTS idx_audit_target ON chetter_audit_log USING btree (target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_audit_created ON chetter_audit_log USING btree (created_at);
CREATE INDEX IF NOT EXISTS idx_audit_token ON chetter_audit_log USING btree (token_id);
CREATE INDEX IF NOT EXISTS idx_audit_search ON chetter_audit_log USING gin (to_tsvector('simple', COALESCE(search_text, '')));
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_artifacts_dedup ON chetter_task_artifacts USING btree (task_id, artifact_type, repo, number);
CREATE INDEX IF NOT EXISTS idx_task_artifacts_task ON chetter_task_artifacts USING btree (task_id);
CREATE INDEX IF NOT EXISTS idx_task_artifacts_agent_session ON chetter_task_artifacts USING btree (agent_session_id);
CREATE INDEX IF NOT EXISTS idx_task_artifacts_session_run ON chetter_task_artifacts USING btree (session_run_id);
CREATE INDEX IF NOT EXISTS idx_task_artifacts_type_repo ON chetter_task_artifacts USING btree (artifact_type, repo);
CREATE INDEX IF NOT EXISTS idx_task_artifacts_number ON chetter_task_artifacts USING btree (repo, number);
CREATE INDEX IF NOT EXISTS idx_artifacts_search ON chetter_task_artifacts USING gin (to_tsvector('simple', COALESCE(search_text, '')));
CREATE UNIQUE INDEX IF NOT EXISTS uq_model_catalogs_name ON chetter_model_catalogs USING btree (name);
CREATE INDEX IF NOT EXISTS idx_model_catalogs_active_updated ON chetter_model_catalogs USING btree (active, updated_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_definition_sources_name ON definition_sources USING btree (name);
CREATE INDEX IF NOT EXISTS idx_definition_sources_scope ON definition_sources USING btree (scope, team_id, repo);
CREATE INDEX IF NOT EXISTS idx_definition_sources_enabled_updated ON definition_sources USING btree (enabled, updated_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_definitions_source_type_name_path ON definitions USING btree (source_id, definition_type, name, path);
CREATE INDEX IF NOT EXISTS idx_definitions_lookup ON definitions USING btree (definition_type, name, active, scope);
CREATE INDEX IF NOT EXISTS idx_definitions_source_active ON definitions USING btree (source_id, active, updated_at);
CREATE INDEX IF NOT EXISTS idx_definitions_hash ON definitions USING btree (content_hash);
CREATE INDEX IF NOT EXISTS idx_definition_sync_runs_source_created ON definition_sync_runs USING btree (source_id, created_at);
CREATE INDEX IF NOT EXISTS idx_definition_sync_runs_status_created ON definition_sync_runs USING btree (status, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_definition_proposals_repo_pr ON definition_change_proposals USING btree (repo, pr_number);
CREATE INDEX IF NOT EXISTS idx_definition_proposals_source_created ON definition_change_proposals USING btree (source_id, created_at);
CREATE INDEX IF NOT EXISTS idx_definition_proposals_status_created ON definition_change_proposals USING btree (status, created_at);
CREATE INDEX IF NOT EXISTS idx_definition_proposals_task ON definition_change_proposals USING btree (task_id);

-- +goose Down
DROP TABLE IF EXISTS definition_change_proposals;
DROP TABLE IF EXISTS definition_sync_runs;
DROP TABLE IF EXISTS definitions;
DROP TABLE IF EXISTS definition_sources;
DROP TABLE IF EXISTS chetter_model_catalogs;
DROP TABLE IF EXISTS chetter_task_artifacts;
DROP TABLE IF EXISTS chetter_audit_log;
DROP TABLE IF EXISTS api_token_teams;
DROP TABLE IF EXISTS user_team_memberships;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS chetter_trigger_runs;
DROP TABLE IF EXISTS chetter_triggers;
DROP TABLE IF EXISTS chetter_runners;
DROP TABLE IF EXISTS chetter_event_callbacks;
DROP TABLE IF EXISTS chetter_task_events;
DROP TABLE IF EXISTS chetter_agent_session_checkpoints;
DROP TABLE IF EXISTS chetter_session_runs;
DROP TABLE IF EXISTS chetter_agent_sessions;
DROP TABLE IF EXISTS chetter_tasks;
