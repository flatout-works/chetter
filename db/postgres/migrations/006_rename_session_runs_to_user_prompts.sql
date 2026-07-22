-- +goose Up
ALTER TABLE chetter_session_runs RENAME TO chetter_user_prompts;
ALTER INDEX uq_session_runs_session_sequence RENAME TO uq_user_prompts_session_sequence;
ALTER INDEX idx_session_runs_task_sequence RENAME TO idx_user_prompts_task_sequence;
ALTER INDEX idx_session_runs_session_created RENAME TO idx_user_prompts_session_created;
ALTER INDEX idx_session_runs_status_created RENAME TO idx_user_prompts_status_created;
ALTER INDEX idx_session_runs_required_runner RENAME TO idx_user_prompts_required_runner;

ALTER TABLE chetter_agent_session_checkpoints
    RENAME COLUMN session_run_id TO user_prompt_id;
ALTER TABLE chetter_task_artifacts
    RENAME COLUMN session_run_id TO user_prompt_id;
ALTER INDEX idx_task_artifacts_session_run RENAME TO idx_task_artifacts_user_prompt;

-- +goose Down
ALTER INDEX idx_task_artifacts_user_prompt RENAME TO idx_task_artifacts_session_run;
ALTER TABLE chetter_task_artifacts
    RENAME COLUMN user_prompt_id TO session_run_id;
ALTER TABLE chetter_agent_session_checkpoints
    RENAME COLUMN user_prompt_id TO session_run_id;

ALTER INDEX uq_user_prompts_session_sequence RENAME TO uq_session_runs_session_sequence;
ALTER INDEX idx_user_prompts_task_sequence RENAME TO idx_session_runs_task_sequence;
ALTER INDEX idx_user_prompts_session_created RENAME TO idx_session_runs_session_created;
ALTER INDEX idx_user_prompts_status_created RENAME TO idx_session_runs_status_created;
ALTER INDEX idx_user_prompts_required_runner RENAME TO idx_session_runs_required_runner;
ALTER TABLE chetter_user_prompts RENAME TO chetter_session_runs;
