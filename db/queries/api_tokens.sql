-- name: GetTeamByName :one
SELECT * FROM teams
WHERE name = ?;

-- name: GetTeamByID :one
SELECT * FROM teams
WHERE id = ?;

-- name: CreateTeam :exec
INSERT INTO teams (id, name, created_at, updated_at)
VALUES (?, ?, ?, ?);

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = ?;

-- name: CreateUser :exec
INSERT INTO users (id, name, team_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: CreateToken :exec
INSERT INTO api_tokens (id, name, token_hash, user_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetTokenByHash :one
SELECT t.id, t.name, t.token_hash, t.user_id, t.created_at, t.updated_at,
       u.name AS user_name, u.team_id, tm.name AS team_name
FROM api_tokens t
JOIN users u ON u.id = t.user_id
JOIN teams tm ON tm.id = u.team_id
WHERE t.token_hash = ?;

-- name: ListTokens :many
SELECT t.id, t.name, t.token_hash, t.user_id, t.created_at, t.updated_at,
       u.name AS user_name, u.team_id, tm.name AS team_name
FROM api_tokens t
JOIN users u ON u.id = t.user_id
JOIN teams tm ON tm.id = u.team_id
ORDER BY t.created_at DESC;

-- name: DeleteToken :exec
DELETE FROM api_tokens
WHERE name = ?;
