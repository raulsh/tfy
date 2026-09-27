-- name: CreateRepo :one
INSERT INTO repos (id, project_id, full_name, default_branch, clone_url, clone_path, created_at)
VALUES (@id, @project_id, @full_name, @default_branch, @clone_url, @clone_path, @now)
RETURNING *;

-- name: GetRepo :one
SELECT * FROM repos WHERE id = @id;

-- name: ListReposByProject :many
SELECT * FROM repos WHERE project_id = @project_id ORDER BY full_name;

-- name: ListAllRepos :many
SELECT * FROM repos ORDER BY full_name;

-- name: UpdateRepoClone :exec
UPDATE repos SET clone_url = @clone_url, clone_path = @clone_path, default_branch = @default_branch WHERE id = @id;

-- name: DeleteRepo :exec
DELETE FROM repos WHERE id = @id AND project_id = @project_id;
