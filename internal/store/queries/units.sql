-- name: NextUnitSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) + 1 AS INTEGER) AS next FROM units;

-- name: CreateUnit :one
INSERT INTO units (id, seq, project_id, parent_unit_id, kind, title, summary, description, origin, state,
                   workspace_path, created_by, created_at, updated_at)
VALUES (@id, @seq, @project_id, @parent_unit_id, @kind, @title, @summary, @description, @origin, @state,
        @workspace_path, @created_by, @now, @now)
RETURNING *;

-- name: GetUnit :one
SELECT * FROM units WHERE id = @id;

-- name: ListUnits :many
SELECT * FROM units
WHERE (sqlc.narg('project_id') IS NULL OR project_id = sqlc.narg('project_id'))
ORDER BY updated_at DESC
LIMIT @lim;

-- name: ListUnitsInStates :many
SELECT * FROM units WHERE state IN (sqlc.slice('states')) ORDER BY updated_at;

-- name: TransitionUnit :execrows
-- Conditional on the current state, so concurrent actions cannot both apply.
UPDATE units SET state = @to_state, attention = '', attention_detail = '', updated_at = @now
WHERE id = @id AND state = @from_state;

-- name: SetUnitAttention :exec
UPDATE units SET attention = @attention, attention_detail = @attention_detail, updated_at = @now WHERE id = @id;

-- name: UpdateUnitDetails :exec
UPDATE units SET title = @title, kind = @kind, summary = @summary, updated_at = @now WHERE id = @id;

-- name: SetUnitReviewIteration :exec
UPDATE units SET review_iteration = @review_iteration, updated_at = @now WHERE id = @id;

-- name: TouchUnit :exec
UPDATE units SET updated_at = @now WHERE id = @id;

-- name: ListChildUnits :many
SELECT * FROM units WHERE parent_unit_id = @parent_unit_id ORDER BY seq;

-- name: SetUnitMergeStep :exec
UPDATE units SET merge_step = @merge_step, updated_at = @now WHERE id = @id;
