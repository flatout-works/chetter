-- +goose Up
-- Multi-repo task support (issue #434). The repos column stores the task's
-- repository set as a JSON array of {url, ref, primary} entries, primary
-- first. It is additive: a task submitted with only git_url/git_ref keeps
-- repos NULL and behaves exactly as before.
--
--  tasks.repos          — the repository set for the submission
--  agent_sessions.repos — the snapshot used by the runner and on resume
--
-- Each column is added in its own statement to stay compatible with TiDB,
-- which rejects multi-column ALTERs whose AFTER clause references a column
-- added in the same statement.
ALTER TABLE tasks ADD COLUMN repos JSON NULL AFTER git_ref;
ALTER TABLE agent_sessions ADD COLUMN repos JSON NULL AFTER git_ref;

-- +goose Down
ALTER TABLE agent_sessions DROP COLUMN repos;
ALTER TABLE tasks DROP COLUMN repos;
