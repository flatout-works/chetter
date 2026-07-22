-- +goose Up
ALTER TABLE chetter_agent_sessions
    ADD COLUMN task_id VARCHAR(64) NULL AFTER id,
    ADD COLUMN sequence INT NOT NULL DEFAULT 1 AFTER task_id;

UPDATE chetter_agent_sessions agent
JOIN (
    SELECT agent_session_id, MIN(task_id) AS task_id
    FROM chetter_session_runs
    GROUP BY agent_session_id
) runs ON runs.agent_session_id = agent.id
SET agent.task_id = runs.task_id;

ALTER TABLE chetter_agent_sessions
    MODIFY COLUMN task_id VARCHAR(64) NOT NULL,
    ADD UNIQUE KEY uq_agent_sessions_task_sequence (task_id, sequence),
    ADD KEY idx_agent_sessions_task (task_id, sequence);

ALTER TABLE chetter_session_runs
    ADD COLUMN sequence INT NOT NULL DEFAULT 1 AFTER task_id;

ALTER TABLE chetter_session_runs
    DROP INDEX uq_session_runs_task;

UPDATE chetter_session_runs session_run
JOIN (
    SELECT id, ROW_NUMBER() OVER (
        PARTITION BY agent_session_id
        ORDER BY created_at, id
    ) AS sequence
    FROM chetter_session_runs
) ranked ON ranked.id = session_run.id
SET session_run.sequence = ranked.sequence;

UPDATE chetter_session_runs session_run
JOIN chetter_agent_sessions agent ON agent.id = session_run.agent_session_id
SET session_run.task_id = agent.task_id;

ALTER TABLE chetter_session_runs
    ADD UNIQUE KEY uq_session_runs_session_sequence (agent_session_id, sequence),
    ADD KEY idx_session_runs_task_sequence (task_id, sequence);

-- +goose Down
ALTER TABLE chetter_session_runs
    DROP INDEX uq_session_runs_session_sequence,
    DROP INDEX idx_session_runs_task_sequence,
    ADD UNIQUE KEY uq_session_runs_task (task_id),
    DROP COLUMN sequence;

ALTER TABLE chetter_agent_sessions
    DROP INDEX uq_agent_sessions_task_sequence,
    DROP INDEX idx_agent_sessions_task,
    DROP COLUMN sequence,
    DROP COLUMN task_id;
