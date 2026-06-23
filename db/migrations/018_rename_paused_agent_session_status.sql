-- +goose Up
UPDATE chetter_agent_sessions
SET status = 'paused'
WHERE status = 'paused_waiting_review';

-- +goose Down
UPDATE chetter_agent_sessions
SET status = 'paused_waiting_review'
WHERE status = 'paused';
