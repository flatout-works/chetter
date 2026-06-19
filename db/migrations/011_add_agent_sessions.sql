-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_agent_sessions (
    id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NULL,
    status VARCHAR(32) NOT NULL,
    resume_mode VARCHAR(32) NOT NULL DEFAULT 'none',
    pinned_runner_id VARCHAR(64) NULL,
    pinned_runner_name VARCHAR(128) NULL,
    checkpoint_id VARCHAR(64) NULL,
    workspace_path TEXT NULL,
    container_name VARCHAR(128) NULL,
    harness_session_id VARCHAR(128) NULL,
    git_url TEXT NULL,
    git_ref VARCHAR(255) NULL,
    agent_image VARCHAR(512) NULL,
    agent VARCHAR(128) NULL,
    provider_id VARCHAR(128) NULL,
    model_id VARCHAR(255) NULL,
    variant_id VARCHAR(128) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    paused_at DATETIME(6) NULL,
    expires_at DATETIME(6) NULL,
    pause_reason VARCHAR(64) NULL,
    error TEXT NULL,
    PRIMARY KEY (id),
    KEY idx_agent_sessions_team_status (team_id, status, updated_at),
    KEY idx_agent_sessions_runner_status (pinned_runner_id, status),
    KEY idx_agent_sessions_expires (expires_at)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS chetter_session_runs (
    id VARCHAR(64) NOT NULL,
    agent_session_id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    prompt TEXT NOT NULL,
    required_runner_id VARCHAR(64) NULL,
    summary TEXT NULL,
    error TEXT NULL,
    session_export MEDIUMTEXT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    ended_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_session_runs_task (task_id),
    KEY idx_session_runs_session_created (agent_session_id, created_at),
    KEY idx_session_runs_status_created (status, created_at),
    KEY idx_session_runs_required_runner (required_runner_id, status)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS chetter_agent_session_checkpoints (
    id VARCHAR(64) NOT NULL,
    agent_session_id VARCHAR(64) NOT NULL,
    session_run_id VARCHAR(64) NULL,
    runner_id VARCHAR(64) NOT NULL,
    checkpoint_path TEXT NOT NULL,
    workspace_path TEXT NOT NULL,
    container_name VARCHAR(128) NULL,
    runsc_version VARCHAR(128) NULL,
    agent_image VARCHAR(512) NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    error TEXT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    KEY idx_session_checkpoints_session_created (agent_session_id, created_at),
    KEY idx_session_checkpoints_runner_status (runner_id, status),
    KEY idx_session_checkpoints_expires (expires_at)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE chetter_tasks ADD COLUMN required_runner_id VARCHAR(64) NULL AFTER runner_id;
ALTER TABLE chetter_tasks ADD COLUMN checkpoint_after_success BOOL NOT NULL DEFAULT false AFTER required_runner_id;
ALTER TABLE chetter_tasks ADD KEY idx_chetter_tasks_required_runner (required_runner_id, status, created_at);

ALTER TABLE chetter_task_artifacts ADD COLUMN agent_session_id VARCHAR(64) NULL AFTER task_id;
ALTER TABLE chetter_task_artifacts ADD COLUMN session_run_id VARCHAR(64) NULL AFTER agent_session_id;
ALTER TABLE chetter_task_artifacts ADD KEY idx_task_artifacts_agent_session (agent_session_id);
ALTER TABLE chetter_task_artifacts ADD KEY idx_task_artifacts_session_run (session_run_id);

-- +goose Down
ALTER TABLE chetter_task_artifacts DROP KEY idx_task_artifacts_session_run;
ALTER TABLE chetter_task_artifacts DROP KEY idx_task_artifacts_agent_session;
ALTER TABLE chetter_task_artifacts DROP COLUMN session_run_id;
ALTER TABLE chetter_task_artifacts DROP COLUMN agent_session_id;

ALTER TABLE chetter_tasks DROP KEY idx_chetter_tasks_required_runner;
ALTER TABLE chetter_tasks DROP COLUMN required_runner_id;
ALTER TABLE chetter_tasks DROP COLUMN checkpoint_after_success;

DROP TABLE IF EXISTS chetter_agent_session_checkpoints;
DROP TABLE IF EXISTS chetter_session_runs;
DROP TABLE IF EXISTS chetter_agent_sessions;
