-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN search_text TEXT NULL AFTER session_export;
ALTER TABLE chetter_agent_sessions ADD COLUMN search_text TEXT NULL AFTER error;
ALTER TABLE chetter_audit_log ADD COLUMN search_text TEXT NULL AFTER detail;
ALTER TABLE chetter_task_artifacts ADD COLUMN search_text TEXT NULL AFTER discovery_source;

UPDATE chetter_tasks SET search_text = CONCAT_WS(' ',
    COALESCE(prompt,''), COALESCE(summary,''), COALESCE(error,''),
    COALESCE(agent,''), COALESCE(model_id,''), COALESCE(trigger_name,''),
    COALESCE(git_url,'')
) WHERE search_text IS NULL;

UPDATE chetter_agent_sessions SET search_text = CONCAT_WS(' ',
    COALESCE(id,''), COALESCE(agent,''), COALESCE(model_id,''),
    COALESCE(git_url,''), COALESCE(error,'')
) WHERE search_text IS NULL;

UPDATE chetter_audit_log SET search_text = CONCAT_WS(' ',
    COALESCE(detail,''), COALESCE(source_type,''), COALESCE(source_id,''),
    COALESCE(target_type,''), COALESCE(target_id,''), COALESCE(repo,''),
    COALESCE(event_type,'')
) WHERE search_text IS NULL;

UPDATE chetter_task_artifacts SET search_text = CONCAT_WS(' ',
    COALESCE(task_id,''), COALESCE(repo,''), COALESCE(artifact_type,''),
    COALESCE(ref,'')
) WHERE search_text IS NULL;

-- +goose Down
ALTER TABLE chetter_task_artifacts DROP COLUMN search_text;
ALTER TABLE chetter_audit_log DROP COLUMN search_text;
ALTER TABLE chetter_agent_sessions DROP COLUMN search_text;
ALTER TABLE chetter_tasks DROP COLUMN search_text;
