-- +goose Up
ALTER TABLE chetter_schedules ADD COLUMN source_id VARCHAR(64) NULL AFTER enabled;

-- +goose Down
ALTER TABLE chetter_schedules DROP COLUMN source_id;
