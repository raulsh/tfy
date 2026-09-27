-- name: CreateProject :one
INSERT INTO projects (id, name, slug, description, product_context, settings, created_at, updated_at)
VALUES (@id, @name, @slug, @description, @product_context, @settings, @now, @now)
RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = @id;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY name;

-- name: UpdateProject :one
UPDATE projects
SET name = @name, description = @description, product_context = @product_context, settings = @settings, updated_at = @now
WHERE id = @id
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = @id;
