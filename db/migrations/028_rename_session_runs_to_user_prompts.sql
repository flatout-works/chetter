-- +goose Up
RENAME TABLE chetter_session_runs TO chetter_user_prompts;

ALTER TABLE chetter_user_prompts
    RENAME INDEX uq_session_runs_session_sequence TO uq_user_prompts_session_sequence,
    RENAME INDEX idx_session_runs_task_sequence TO idx_user_prompts_task_sequence,
    RENAME INDEX idx_session_runs_session_created TO idx_user_prompts_session_created,
    RENAME INDEX idx_session_runs_status_created TO idx_user_prompts_status_created,
    RENAME INDEX idx_session_runs_required_runner TO idx_user_prompts_required_runner;

ALTER TABLE chetter_agent_session_checkpoints
    CHANGE COLUMN session_run_id user_prompt_id VARCHAR(64) NULL;

ALTER TABLE chetter_task_artifacts
    RENAME INDEX idx_task_artifacts_session_run TO idx_task_artifacts_user_prompt,
    CHANGE COLUMN session_run_id user_prompt_id VARCHAR(64) NULL;

-- +goose Down
ALTER TABLE chetter_task_artifacts
    RENAME INDEX idx_task_artifacts_user_prompt TO idx_task_artifacts_session_run,
    CHANGE COLUMN user_prompt_id session_run_id VARCHAR(64) NULL;

ALTER TABLE chetter_agent_session_checkpoints
    CHANGE COLUMN user_prompt_id session_run_id VARCHAR(64) NULL;

ALTER TABLE chetter_user_prompts
    RENAME INDEX uq_user_prompts_session_sequence TO uq_session_runs_session_sequence,
    RENAME INDEX idx_user_prompts_task_sequence TO idx_session_runs_task_sequence,
    RENAME INDEX idx_user_prompts_session_created TO idx_session_runs_session_created,
    RENAME INDEX idx_user_prompts_status_created TO idx_session_runs_status_created,
    RENAME INDEX idx_user_prompts_required_runner TO idx_session_runs_required_runner;

RENAME TABLE chetter_user_prompts TO chetter_session_runs;
