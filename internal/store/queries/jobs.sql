-- name: EnqueueJob :one
INSERT INTO jobs (id, kind, unit_id, project_id, payload, status, dedupe_key, run_after, created_at)
VALUES (@id, @kind, @unit_id, @project_id, @payload, 'queued', @dedupe_key, @run_after, @now)
RETURNING *;

-- name: ClaimJob :one
UPDATE jobs SET status = 'running', attempts = attempts + 1, started_at = @now
WHERE id = (SELECT j.id FROM jobs j
            WHERE j.status = 'queued' AND j.run_after <= @now AND j.kind IN (sqlc.slice('kinds'))
            ORDER BY j.run_after, j.created_at LIMIT 1)
RETURNING *;

-- name: FinishJob :exec
UPDATE jobs SET status = @status, error = @error, finished_at = @now WHERE id = @id;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = @id;

-- name: ListRunningJobs :many
SELECT * FROM jobs WHERE status = 'running';

-- name: RequeueJob :exec
UPDATE jobs SET status = 'queued', run_after = @run_after, error = @error WHERE id = @id;

-- name: ActiveJobForUnit :one
SELECT * FROM jobs WHERE unit_id = @unit_id AND status IN ('queued', 'running') ORDER BY created_at DESC LIMIT 1;

-- name: CancelQueuedJobsForUnit :exec
UPDATE jobs SET status = 'cancelled', finished_at = @now WHERE unit_id = @unit_id AND status = 'queued';


-- name: ReleaseJobDedupe :exec
-- A running job hands its dedupe key over to the successor it enqueues.
UPDATE jobs SET dedupe_key = NULL WHERE id = @id;

-- name: ListBusyUnitIDs :many
SELECT DISTINCT CAST(unit_id AS TEXT) AS unit_id FROM jobs WHERE status IN ('queued', 'running') AND unit_id IS NOT NULL;
