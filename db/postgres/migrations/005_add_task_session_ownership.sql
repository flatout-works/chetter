-- +goose Up
ALTER TABLE chetter_agent_sessions
    ADD COLUMN IF NOT EXISTS task_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS sequence INTEGER NOT NULL DEFAULT 1;

UPDATE chetter_agent_sessions agent
SET task_id = runs.task_id
FROM (
    SELECT agent_session_id, MIN(task_id) AS task_id
    FROM chetter_session_runs
    GROUP BY agent_session_id
) runs
WHERE runs.agent_session_id = agent.id;

ALTER TABLE chetter_agent_sessions
    ALTER COLUMN task_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_sessions_task_sequence
    ON chetter_agent_sessions (task_id, sequence);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_task
    ON chetter_agent_sessions (task_id, sequence);

ALTER TABLE chetter_session_runs
    ADD COLUMN IF NOT EXISTS sequence INTEGER NOT NULL DEFAULT 1;

DROP INDEX IF EXISTS uq_session_runs_task;

WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (
        PARTITION BY agent_session_id
        ORDER BY created_at, id
    ) AS sequence
    FROM chetter_session_runs
)
UPDATE chetter_session_runs session_run
SET sequence = ranked.sequence
FROM ranked
WHERE ranked.id = session_run.id;

UPDATE chetter_session_runs session_run
SET task_id = agent.task_id
FROM chetter_agent_sessions agent
WHERE agent.id = session_run.agent_session_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_session_runs_session_sequence
    ON chetter_session_runs (agent_session_id, sequence);
CREATE INDEX IF NOT EXISTS idx_session_runs_task_sequence
    ON chetter_session_runs (task_id, sequence);

-- +goose Down
DROP INDEX IF EXISTS uq_session_runs_session_sequence;
DROP INDEX IF EXISTS idx_session_runs_task_sequence;
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_runs_task
    ON chetter_session_runs (task_id);
ALTER TABLE chetter_session_runs DROP COLUMN IF EXISTS sequence;
DROP INDEX IF EXISTS uq_agent_sessions_task_sequence;
DROP INDEX IF EXISTS idx_agent_sessions_task;
ALTER TABLE chetter_agent_sessions
    DROP COLUMN IF EXISTS sequence,
    DROP COLUMN IF EXISTS task_id;
