-- +goose Up
-- Extend the callback_deliveries outbox to cover create_task callback actions
-- (issue #405). action_type selects the execution path (webhook/slack HTTP
-- delivery versus create_task spawn) and child_task_id records the spawned
-- task for replay-safe recovery.
ALTER TABLE callback_deliveries ADD COLUMN IF NOT EXISTS action_type VARCHAR(32) NOT NULL DEFAULT 'webhook';
ALTER TABLE callback_deliveries ADD COLUMN IF NOT EXISTS child_task_id VARCHAR(64) NULL;
-- Backfill the true action type for rows that predate create_task support
-- (only webhook/slack were enqueued before issue #405).
UPDATE callback_deliveries cd
SET action_type = ec.action_type
FROM event_callbacks ec
WHERE ec.id = cd.callback_id;

-- +goose Down
ALTER TABLE callback_deliveries DROP COLUMN IF EXISTS child_task_id;
ALTER TABLE callback_deliveries DROP COLUMN IF EXISTS action_type;
