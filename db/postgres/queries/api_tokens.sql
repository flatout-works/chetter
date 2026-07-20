-- name: GetTeamByName :one
SELECT * FROM teams WHERE name = $1;

-- name: GetTeamByID :one
SELECT * FROM teams WHERE id = $1;

-- name: CreateTeam :exec
INSERT INTO teams (id, name, okta_group_id, okta_group_name, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: CreateUser :exec
INSERT INTO users (id, name, team_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5);

-- name: CreateToken :exec
INSERT INTO api_tokens (id, name, token_hash, user_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: AddUserTeamMembership :exec
INSERT INTO user_team_memberships (user_id, team_id, source, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, team_id) DO NOTHING;

-- name: AddTokenTeam :exec
INSERT INTO api_token_teams (token_id, team_id, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (token_id, team_id) DO NOTHING;

-- name: GetTokenByHash :one
SELECT t.id, t.name, t.token_hash, t.user_id, t.created_at, t.updated_at,
       u.name AS user_name, u.team_id, tm.name AS team_name
FROM api_tokens t
JOIN users u ON u.id = t.user_id
JOIN teams tm ON tm.id = u.team_id
WHERE t.token_hash = $1;

-- name: ListTokens :many
SELECT t.id, t.name, t.token_hash, t.user_id, t.created_at, t.updated_at,
       u.name AS user_name, u.team_id, tm.name AS team_name,
       COALESCE(string_agg(ttm.name, ',' ORDER BY ttm.name), tm.name) AS team_names
FROM api_tokens t
JOIN users u ON u.id = t.user_id
JOIN teams tm ON tm.id = u.team_id
LEFT JOIN api_token_teams tt ON tt.token_id = t.id
LEFT JOIN teams ttm ON ttm.id = tt.team_id
GROUP BY t.id, t.name, t.token_hash, t.user_id, t.created_at, t.updated_at, u.name, u.team_id, tm.name
ORDER BY t.created_at DESC;

-- name: ListTeams :many
SELECT * FROM teams ORDER BY name ASC;

-- name: DeleteTeam :exec
DELETE FROM teams WHERE name = $1;

-- name: ListUsers :many
SELECT u.id, u.name, u.team_id, tm.name AS team_name, u.created_at, u.updated_at
FROM users u
JOIN teams tm ON tm.id = u.team_id
ORDER BY u.name ASC;

-- name: ListUsersByTeam :many
SELECT u.id, u.name, u.team_id, tm.name AS team_name, u.created_at, u.updated_at
FROM users u
JOIN teams tm ON tm.id = u.team_id
WHERE u.team_id = $1
ORDER BY u.name ASC;

-- name: DeleteUsersByTeam :exec
DELETE FROM users WHERE team_id = $1;

-- name: DeleteTokensByTeam :exec
DELETE FROM api_tokens
WHERE id IN (SELECT token_id FROM api_token_teams WHERE api_token_teams.team_id = $1)
   OR user_id IN (SELECT id FROM users WHERE users.team_id = $2);

-- name: DeleteTokenTeamsByTeam :exec
DELETE FROM api_token_teams WHERE team_id = $1;

-- name: DeleteUserTeamMembershipsByTeam :exec
DELETE FROM user_team_memberships WHERE team_id = $1;

-- name: DeleteToken :exec
DELETE FROM api_tokens WHERE name = $1;
