-- +goose Up
ALTER TABLE chetter_schedule_runs ADD COLUMN team_id VARCHAR(64) NULL AFTER schedule_id;

-- +goose Down
ALTER TABLE chetter_schedule_runs DROP COLUMN team_id;
