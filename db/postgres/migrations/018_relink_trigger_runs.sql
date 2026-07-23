-- +goose Up
-- Definition sync can replace trigger IDs while retaining task and run history.
-- Keep one run per task before relinking so the deduplication index remains valid.
DELETE FROM chetter_trigger_runs sr
USING chetter_tasks t, chetter_triggers tr, chetter_trigger_runs keep
WHERE t.id = sr.task_id
  AND tr.name = t.trigger_name
  AND sr.trigger_id <> tr.id
  AND keep.task_id = sr.task_id
  AND (keep.trigger_id = tr.id OR keep.id < sr.id);

UPDATE chetter_trigger_runs sr
SET trigger_id = tr.id,
    team_id = tr.team_id
FROM chetter_tasks t
JOIN chetter_triggers tr ON tr.name = t.trigger_name
WHERE sr.task_id = t.id
  AND sr.trigger_id <> tr.id;

-- +goose Down
-- The original trigger IDs may no longer exist, so this data repair is irreversible.
