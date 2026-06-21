-- name: InsertDefinitionChangeProposal :exec
INSERT INTO definition_change_proposals (
    id, source_id, task_id, repo, branch, base_branch, pr_number, pr_url,
    title, body, files, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    task_id = VALUES(task_id),
    branch = VALUES(branch),
    base_branch = VALUES(base_branch),
    pr_url = VALUES(pr_url),
    title = VALUES(title),
    body = VALUES(body),
    files = VALUES(files),
    status = VALUES(status),
    updated_at = VALUES(updated_at);

-- name: GetDefinitionChangeProposal :one
SELECT * FROM definition_change_proposals
WHERE id = ?;

-- name: GetDefinitionChangeProposalByPR :one
SELECT * FROM definition_change_proposals
WHERE repo = ? AND pr_number = ?;

-- name: ListDefinitionChangeProposals :many
SELECT * FROM definition_change_proposals
WHERE (? = '' OR source_id = ?)
  AND (? = '' OR status = ?)
ORDER BY created_at DESC
LIMIT ?;

-- name: UpdateDefinitionChangeProposalStatus :exec
UPDATE definition_change_proposals
SET status = ?, updated_at = ?
WHERE id = ?;
