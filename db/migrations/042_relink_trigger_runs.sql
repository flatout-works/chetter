-- +goose Up
-- Definition sync can replace trigger IDs while retaining task and run history.
-- Keep one run per task before relinking so the deduplication index remains valid.
DELETE sr
FROM chetter_trigger_runs sr
JOIN chetter_tasks t ON t.id = sr.task_id
JOIN chetter_triggers tr ON tr.name = t.trigger_name
JOIN chetter_trigger_runs keep ON keep.task_id = sr.task_id
WHERE sr.trigger_id <> tr.id
  AND (keep.trigger_id = tr.id OR keep.id < sr.id);

UPDATE chetter_trigger_runs sr
JOIN chetter_tasks t ON t.id = sr.task_id
JOIN chetter_triggers tr ON tr.name = t.trigger_name
SET sr.trigger_id = tr.id,
    sr.team_id = tr.team_id
WHERE sr.trigger_id <> tr.id;

-- +goose Down
-- The original trigger IDs may no longer exist, so this data repair is irreversible.
