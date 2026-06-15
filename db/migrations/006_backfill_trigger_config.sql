-- +goose Up
-- Existing schedules created before trigger_type/trigger_config columns were
-- added may have NULL trigger_config because the ALTER TABLE used JSON NULL.
-- The sqlc model (json.RawMessage) cannot scan NULL, so backfill with '{}'.
UPDATE chetter_schedules SET trigger_config = '{}' WHERE trigger_config IS NULL;

-- +goose Down
-- Not reversible: no reason to put NULLs back.
