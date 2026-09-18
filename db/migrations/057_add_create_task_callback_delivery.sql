-- +goose Up
-- Extend the callback_deliveries outbox to cover create_task callback actions
-- (issue #405) so a spawned task can never be lost to a replica crash. The
-- existing table already carries the event provenance and retry/backoff
-- state; action_type selects the execution path (webhook/slack HTTP delivery
-- versus create_task spawn) and child_task_id records the spawned task.
-- Each column is added in its own statement to stay compatible with TiDB,
-- which rejects multi-column ALTERs whose AFTER clause references a column
-- added in the same statement.
ALTER TABLE callback_deliveries ADD COLUMN action_type VARCHAR(32) NOT NULL DEFAULT 'webhook';
ALTER TABLE callback_deliveries ADD COLUMN child_task_id VARCHAR(64) NULL;
-- Backfill the true action type for rows that predate create_task support
-- (only webhook/slack were enqueued before issue #405), so the list tool and
-- any future action-type-specific logic see the correct value.
UPDATE callback_deliveries cd
JOIN event_callbacks ec ON ec.id = cd.callback_id
SET cd.action_type = ec.action_type;

-- +goose Down
ALTER TABLE callback_deliveries DROP COLUMN child_task_id;
ALTER TABLE callback_deliveries DROP COLUMN action_type;
