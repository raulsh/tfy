-- name: AddActivity :exec
INSERT INTO activity (unit_id, project_id, at, actor, kind, message, data)
VALUES (@unit_id, @project_id, @at, @actor, @kind, @message, @data);

-- name: ListUnitActivity :many
SELECT * FROM activity WHERE unit_id = @unit_id ORDER BY id DESC LIMIT @lim;

-- name: ListRecentActivity :many
SELECT * FROM activity ORDER BY id DESC LIMIT @lim;

-- name: DailyDone :many
SELECT CAST(date(at) AS TEXT) AS day, COUNT(*) AS n FROM activity
WHERE kind = 'transition' AND data LIKE '%"to":"done"%' AND at >= @since
GROUP BY date(at) ORDER BY day;

-- name: HasUnitActivitySince :one
SELECT CAST(EXISTS(
    SELECT 1 FROM activity
    WHERE unit_id = @unit_id AND kind IN ('transition', 'cleanup') AND at > @since
) AS INTEGER);
