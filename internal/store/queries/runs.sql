-- name: CreateRun :one
INSERT INTO runs (id, unit_id, project_id, parent_run_id, kind, status, model, effort, permission_mode, prompt_version, cwd, created_at)
VALUES (@id, @unit_id, @project_id, @parent_run_id, @kind, 'queued', @model, @effort, @permission_mode, @prompt_version, @cwd, @now)
RETURNING *;

-- name: GetRun :one
SELECT * FROM runs WHERE id = @id;

-- name: StartRun :exec
UPDATE runs SET status = 'running', pid = @pid, started_at = @now WHERE id = @id;

-- name: SetRunSession :exec
UPDATE runs SET session_id = @session_id WHERE id = @id;

-- name: FinishRun :exec
UPDATE runs
SET status = @status, reason = @reason, ended_at = @now, session_id = @session_id,
    cost_usd = @cost_usd, cost_total_usd = @cost_total_usd, input_tokens = @input_tokens, output_tokens = @output_tokens,
    turns = @turns, denials = @denials, result = @result, pid = 0
WHERE id = @id;

-- name: ListRuns :many
SELECT runs.*, COALESCE(units.seq, 0) AS unit_seq, COALESCE(units.title, '') AS unit_title
FROM runs LEFT JOIN units ON units.id = runs.unit_id
WHERE (sqlc.narg('unit_id') IS NULL OR runs.unit_id = sqlc.narg('unit_id'))
  AND (sqlc.narg('project_id') IS NULL OR runs.project_id = sqlc.narg('project_id'))
ORDER BY runs.created_at DESC
LIMIT @lim;

-- name: LastSessionRun :one
-- The most recent run of a kind that left a resumable session.
SELECT * FROM runs
WHERE unit_id = @unit_id AND kind = @kind AND session_id != '' AND status IN ('succeeded', 'failed', 'cancelled', 'timed_out', 'budget_exceeded', 'interrupted')
ORDER BY created_at DESC LIMIT 1;

-- name: ListLiveRuns :many
SELECT * FROM runs WHERE status IN ('queued', 'running');

-- name: InterruptRun :exec
UPDATE runs SET status = 'interrupted', reason = @reason, ended_at = @now, pid = 0 WHERE id = @id;

-- name: SumCost :one
SELECT CAST(COALESCE(SUM(cost_usd), 0) AS REAL) AS total FROM runs WHERE created_at >= @since;

-- name: DailyCost :many
SELECT CAST(date(created_at) AS TEXT) AS day, CAST(SUM(cost_usd) AS REAL) AS cost, COUNT(*) AS runs
FROM runs WHERE created_at >= @since
GROUP BY date(created_at) ORDER BY day;
