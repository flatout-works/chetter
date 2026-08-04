-- +goose Up
-- Drop the redundant chetter_ table prefix. The database itself is already
-- named chetter, so the prefix only added noise. Renames preserve all data,
-- indexes, and constraints; index names keep their historical names, which is
-- harmless in MySQL/TiDB (index names are scoped per table).
RENAME TABLE
    chetter_tasks TO tasks,
    chetter_task_events TO task_events,
    chetter_user_prompts TO user_prompts,
    chetter_execution_attempts TO execution_attempts,
    chetter_agent_sessions TO agent_sessions,
    chetter_agent_session_checkpoints TO agent_session_checkpoints,
    chetter_task_artifacts TO task_artifacts,
    chetter_runners TO runners,
    chetter_triggers TO triggers,
    chetter_trigger_runs TO trigger_runs,
    chetter_event_callbacks TO event_callbacks,
    chetter_model_catalogs TO model_catalogs,
    chetter_audit_log TO audit_log,
    chetter_webhook_deliveries TO webhook_deliveries;

-- +goose Down
RENAME TABLE
    tasks TO chetter_tasks,
    task_events TO chetter_task_events,
    user_prompts TO chetter_user_prompts,
    execution_attempts TO chetter_execution_attempts,
    agent_sessions TO chetter_agent_sessions,
    agent_session_checkpoints TO chetter_agent_session_checkpoints,
    task_artifacts TO chetter_task_artifacts,
    runners TO chetter_runners,
    triggers TO chetter_triggers,
    trigger_runs TO chetter_trigger_runs,
    event_callbacks TO chetter_event_callbacks,
    model_catalogs TO chetter_model_catalogs,
    audit_log TO chetter_audit_log,
    webhook_deliveries TO chetter_webhook_deliveries;
