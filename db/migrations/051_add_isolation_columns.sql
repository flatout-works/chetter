-- +goose Up
-- Isolation policy columns (issue #291):
--   chetter_agent_sessions.isolation_required — task must run inside an
--     enforced sandbox (gVisor/runsc); admission refuses to place it on a
--     runner without isolation_enabled.
--   chetter_runners.isolation_enabled — runner advertises enforced isolation
--     (gVisor configured and runsc available) via claim/heartbeat metadata.
-- One ALTER per column: a later AFTER clause must never reference a column
-- added in the same statement (TiDB rejects it with Error 1054).
ALTER TABLE chetter_agent_sessions
    ADD COLUMN isolation_required TINYINT(1) NOT NULL DEFAULT 0 AFTER resume_mode;
ALTER TABLE chetter_runners
    ADD COLUMN isolation_enabled TINYINT(1) NOT NULL DEFAULT 0 AFTER available_slots;

-- +goose Down
ALTER TABLE chetter_runners
    DROP COLUMN isolation_enabled;
ALTER TABLE chetter_agent_sessions
    DROP COLUMN isolation_required;
