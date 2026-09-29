-- name: NextUnitSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) + 1 AS INTEGER) AS next FROM units;

-- name: CreateUnit :one
INSERT INTO units (id, seq, project_id, parent_unit_id, kind, title, summary, description, origin, state,
                   workspace_path, created_by, run_overrides, subagents, created_at, updated_at)
VALUES (@id, @seq, @project_id, @parent_unit_id, @kind, @title, @summary, @description, @origin, @state,
        @workspace_path, @created_by, @run_overrides, @subagents, @now, @now)
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
-- The merge run's decisions count only while it drives a merge, and a
-- person's choice to merge as an administrator holds for that merge only:
-- moving out of its states (keep_merge 0) resets both.
UPDATE units SET state = @to_state, attention = '', attention_detail = '',
    merge_round = merge_round * @keep_merge, merge_admin = merge_admin * @keep_merge, updated_at = @now
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

-- name: SetUnitMergeRound :exec
UPDATE units SET merge_round = @merge_round, updated_at = @now WHERE id = @id;

-- name: SetUnitMergeAdmin :exec
UPDATE units SET merge_admin = @merge_admin, updated_at = @now WHERE id = @id;

-- name: SetUnitSubagents :exec
UPDATE units SET subagents = @subagents, updated_at = @now WHERE id = @id;
