-- +goose Up
ALTER TABLE chetter_schedules RENAME TO chetter_triggers;
ALTER TABLE chetter_triggers RENAME INDEX uq_chetter_schedules_name TO uq_chetter_triggers_name;
ALTER TABLE chetter_triggers RENAME INDEX idx_chetter_schedules_enabled_next TO idx_chetter_triggers_enabled_next;
ALTER TABLE chetter_schedule_runs RENAME TO chetter_trigger_runs;
ALTER TABLE chetter_trigger_runs CHANGE COLUMN schedule_id trigger_id VARCHAR(64) NOT NULL;
ALTER TABLE chetter_trigger_runs CHANGE COLUMN scheduled_for triggered_at DATETIME(6) NOT NULL;
ALTER TABLE chetter_trigger_runs RENAME INDEX idx_chetter_schedule_runs_schedule_created TO idx_chetter_trigger_runs_trigger_created;
ALTER TABLE chetter_trigger_runs RENAME INDEX idx_chetter_schedule_runs_task TO idx_chetter_trigger_runs_task;

-- +goose Down
ALTER TABLE chetter_trigger_runs RENAME INDEX idx_chetter_trigger_runs_task TO idx_chetter_schedule_runs_task;
ALTER TABLE chetter_trigger_runs RENAME INDEX idx_chetter_trigger_runs_trigger_created TO idx_chetter_schedule_runs_schedule_created;
ALTER TABLE chetter_trigger_runs CHANGE COLUMN triggered_at scheduled_for DATETIME(6) NOT NULL;
ALTER TABLE chetter_trigger_runs CHANGE COLUMN trigger_id schedule_id VARCHAR(64) NOT NULL;
ALTER TABLE chetter_trigger_runs RENAME TO chetter_schedule_runs;
ALTER TABLE chetter_triggers RENAME INDEX idx_chetter_triggers_enabled_next TO idx_chetter_schedules_enabled_next;
ALTER TABLE chetter_triggers RENAME INDEX uq_chetter_triggers_name TO uq_chetter_schedules_name;
ALTER TABLE chetter_triggers RENAME TO chetter_schedules;
