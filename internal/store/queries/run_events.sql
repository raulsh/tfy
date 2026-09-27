-- name: InsertRunEvent :exec
INSERT INTO run_events (run_id, seq, at, type, subtype, tool, summary, payload)
VALUES (@run_id, @seq, @at, @type, @subtype, @tool, @summary, @payload);

-- name: ListRunEvents :many
SELECT * FROM run_events WHERE run_id = @run_id AND seq > @after ORDER BY seq LIMIT @lim;

-- name: ListUnitDenials :many
-- What the permission system and the guard refused during a unit's runs.
SELECT r.kind AS run_kind, e.subtype, e.tool, e.summary, e.payload FROM run_events e
JOIN runs r ON r.id = e.run_id
WHERE r.unit_id = @unit_id AND e.type = 'system'
  AND (e.subtype = 'permission_denied' OR (e.subtype = 'hook_response' AND json_extract(e.payload, '$.exit_code') = 2))
ORDER BY e.at
LIMIT @lim;
