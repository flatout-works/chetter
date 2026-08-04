-- +goose Up
-- Drop the redundant chetter_ table prefix; see db/migrations/052 for details.
ALTER TABLE chetter_tasks RENAME TO tasks;
ALTER TABLE chetter_task_events RENAME TO task_events;
ALTER TABLE chetter_user_prompts RENAME TO user_prompts;
ALTER TABLE chetter_execution_attempts RENAME TO execution_attempts;
ALTER TABLE chetter_agent_sessions RENAME TO agent_sessions;
ALTER TABLE chetter_agent_session_checkpoints RENAME TO agent_session_checkpoints;
ALTER TABLE chetter_task_artifacts RENAME TO task_artifacts;
ALTER TABLE chetter_runners RENAME TO runners;
ALTER TABLE chetter_triggers RENAME TO triggers;
ALTER TABLE chetter_trigger_runs RENAME TO trigger_runs;
ALTER TABLE chetter_event_callbacks RENAME TO event_callbacks;
ALTER TABLE chetter_model_catalogs RENAME TO model_catalogs;
ALTER TABLE chetter_audit_log RENAME TO audit_log;
ALTER TABLE chetter_webhook_deliveries RENAME TO webhook_deliveries;

-- +goose Down
ALTER TABLE tasks RENAME TO chetter_tasks;
ALTER TABLE task_events RENAME TO chetter_task_events;
ALTER TABLE user_prompts RENAME TO chetter_user_prompts;
ALTER TABLE execution_attempts RENAME TO chetter_execution_attempts;
ALTER TABLE agent_sessions RENAME TO chetter_agent_sessions;
ALTER TABLE agent_session_checkpoints RENAME TO chetter_agent_session_checkpoints;
ALTER TABLE task_artifacts RENAME TO chetter_task_artifacts;
ALTER TABLE runners RENAME TO chetter_runners;
ALTER TABLE triggers RENAME TO chetter_triggers;
ALTER TABLE trigger_runs RENAME TO chetter_trigger_runs;
ALTER TABLE event_callbacks RENAME TO chetter_event_callbacks;
ALTER TABLE model_catalogs RENAME TO chetter_model_catalogs;
ALTER TABLE audit_log RENAME TO chetter_audit_log;
ALTER TABLE webhook_deliveries RENAME TO chetter_webhook_deliveries;
