-- +goose Up
CREATE TABLE IF NOT EXISTS chetter_execution_attempts (
    id VARCHAR(64) NOT NULL,
    user_prompt_id VARCHAR(64) NOT NULL,
    sequence INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    runner_id VARCHAR(64) NULL,
    required_runner_id VARCHAR(64) NULL,
    claimed_at DATETIME(6) NULL,
    lease_expires_at DATETIME(6) NULL,
    started_at DATETIME(6) NULL,
    ended_at DATETIME(6) NULL,
    workspace_path TEXT NULL,
    container_name VARCHAR(128) NULL,
    harness_execution_id VARCHAR(128) NULL,
    summary TEXT NULL,
    error TEXT NULL,
    error_category VARCHAR(32) NULL,
    session_export MEDIUMTEXT NULL,
    total_input_tokens BIGINT NOT NULL DEFAULT 0,
    total_output_tokens BIGINT NOT NULL DEFAULT 0,
    total_cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    total_cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    total_reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    cost_cents BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_execution_attempts_prompt_sequence (user_prompt_id, sequence),
    KEY idx_execution_attempts_status_lease (status, lease_expires_at),
    KEY idx_execution_attempts_runner_status (runner_id, status)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS chetter_execution_attempts;
