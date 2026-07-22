-- +goose Up
ALTER TABLE chetter_tasks ADD COLUMN error_category VARCHAR(32) NULL AFTER error;
ALTER TABLE chetter_task_events ADD COLUMN event_type VARCHAR(64) NOT NULL DEFAULT 'task.progress' AFTER status;
ALTER TABLE chetter_task_events ADD KEY idx_chetter_task_events_type_created (event_type, created_at);

CREATE TABLE IF NOT EXISTS chetter_event_callbacks (
    id VARCHAR(64) NOT NULL,
    team_id VARCHAR(64) NULL,
    name VARCHAR(255) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    action_type VARCHAR(32) NOT NULL,
    action_config JSON NOT NULL,
    enabled BOOL NOT NULL DEFAULT true,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_event_callbacks_team_name (team_id, name),
    KEY idx_event_callbacks_team_enabled (team_id, enabled),
    KEY idx_event_callbacks_event_type (event_type, enabled)
) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS chetter_event_callbacks;
ALTER TABLE chetter_task_events DROP KEY idx_chetter_task_events_type_created;
ALTER TABLE chetter_task_events DROP COLUMN event_type;
ALTER TABLE chetter_tasks DROP COLUMN error_category;
