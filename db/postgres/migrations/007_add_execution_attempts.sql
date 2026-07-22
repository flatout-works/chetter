-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_execution_attempts (
    id VARCHAR(64) PRIMARY KEY,
    user_prompt_id VARCHAR(64) NOT NULL,
    sequence INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL,
    runner_id VARCHAR(64),
    required_runner_id VARCHAR(64),
    claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    workspace_path TEXT,
    container_name VARCHAR(128),
    harness_execution_id VARCHAR(128),
    summary TEXT,
    error TEXT,
    error_category VARCHAR(32),
    session_export TEXT,
    total_input_tokens BIGINT NOT NULL DEFAULT 0,
    total_output_tokens BIGINT NOT NULL DEFAULT 0,
    total_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    total_cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    total_reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    cost_cents BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_prompt_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_execution_attempts_status_lease
    ON chetter_execution_attempts (status, lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_execution_attempts_runner_status
    ON chetter_execution_attempts (runner_id, status);

-- +goose Down
DROP TABLE IF EXISTS chetter_execution_attempts;
