-- +goose Up
-- Event-callback provenance columns (issue #312):
--   tasks.callback_parent_task_id — the task whose lifecycle event triggered
--     the create_task callback that spawned this task (NULL for tasks not
--     created by a callback).
--   tasks.callback_depth — number of create_task callback hops from the
--     originating task (0 = not spawned by a callback; 1 = spawned directly
--     from a source task; ...). Enforced against CHETTER_CALLBACK_MAX_DEPTH
--     at callback dispatch time to stop unbounded recursion loops.
-- One ALTER per column: a later AFTER clause must never reference a column
-- added in the same statement (TiDB rejects it with Error 1054).
ALTER TABLE tasks
    ADD COLUMN callback_parent_task_id VARCHAR(64) NULL AFTER self_test_nonce;
ALTER TABLE tasks
    ADD COLUMN callback_depth INT NOT NULL DEFAULT 0 AFTER callback_parent_task_id;

-- +goose Down
ALTER TABLE tasks
    DROP COLUMN callback_depth;
ALTER TABLE tasks
    DROP COLUMN callback_parent_task_id;
