-- name: InsertRunEvent :exec
INSERT INTO run_events (run_id, seq, at, type, subtype, tool, summary, payload)
VALUES (@run_id, @seq, @at, @type, @subtype, @tool, @summary, @payload);

-- name: ListRunEvents :many
SELECT * FROM run_events WHERE run_id = @run_id AND seq > @after ORDER BY seq LIMIT @lim;
