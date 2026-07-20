-- name: InsertDefinitionChangeProposal :exec
INSERT INTO definition_change_proposals (
    id, source_id, task_id, repo, branch, base_branch, pr_number, pr_url,
    title, body, files, status, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (repo, pr_number) DO UPDATE SET
    task_id = EXCLUDED.task_id,
    branch = EXCLUDED.branch,
    base_branch = EXCLUDED.base_branch,
    pr_url = EXCLUDED.pr_url,
    title = EXCLUDED.title,
    body = EXCLUDED.body,
    files = EXCLUDED.files,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at;

-- name: GetDefinitionChangeProposal :one
SELECT * FROM definition_change_proposals WHERE id = $1;

-- name: GetDefinitionChangeProposalByPR :one
SELECT * FROM definition_change_proposals WHERE repo = $1 AND pr_number = $2;

-- name: ListDefinitionChangeProposals :many
SELECT * FROM definition_change_proposals
WHERE (sqlc.arg(source_id_filter) = '' OR source_id = sqlc.arg(source_id_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit);

-- name: UpdateDefinitionChangeProposalStatus :exec
UPDATE definition_change_proposals SET status = $1, updated_at = $2 WHERE id = $3;
