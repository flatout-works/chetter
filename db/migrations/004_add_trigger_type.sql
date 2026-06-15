-- +goose Up
-- trigger_type and trigger_config columns are added to chetter_schedules by
-- store.ensureTriggerColumns() on every startup for zero-downtime deployments.
-- This migration records the schema version. The CREATE TABLE in
-- 001_create_chetter_core.sql already includes these columns for sqlc.

-- +goose Down
-- Not reversible: columns are managed by ensureTriggerColumns().
