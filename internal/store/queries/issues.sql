-- name: LinkUnitIssue :one
INSERT INTO unit_issues (id, unit_id, repo, number, closes, created_at, updated_at)
VALUES (@id, @unit_id, @repo, @number, @closes, @now, @now)
ON CONFLICT (unit_id, repo, number) DO UPDATE SET closes = excluded.closes, updated_at = excluded.updated_at
RETURNING *;

-- name: GetUnitIssue :one
SELECT * FROM unit_issues WHERE id = @id AND unit_id = @unit_id;

-- name: ListUnitIssues :many
SELECT * FROM unit_issues WHERE unit_id = @unit_id ORDER BY created_at, number;

-- name: UpdateUnitIssueContent :exec
UPDATE unit_issues
SET url = @url, title = @title, state = @state, author = @author, labels = @labels, body = @body,
    comments = @comments, public = @public, issue_updated_at = @issue_updated_at, fetched_at = @now, updated_at = @now
WHERE id = @id;

-- name: SetUnitIssueCloses :exec
UPDATE unit_issues SET closes = @closes, updated_at = @now WHERE id = @id;

-- name: SetUnitIssueSuggestion :exec
UPDATE unit_issues SET suggestion = @suggestion, suggestion_state = @suggestion_state, updated_at = @now WHERE id = @id;

-- name: DeleteUnitIssue :exec
DELETE FROM unit_issues WHERE id = @id AND unit_id = @unit_id;

-- name: ListIssueUnits :many
-- The units an issue is linked to, for the picker.
SELECT ui.repo, ui.number, u.id AS unit_id, u.seq, u.state FROM unit_issues ui
JOIN units u ON u.id = ui.unit_id
WHERE u.project_id = @project_id;
