-- name: CreateSlackSource :one
INSERT INTO slack_sources (id, project_id, channel_id, channel_name, auto_triage, exclude_bots, poll_interval_s, cursor_ts, created_at)
VALUES (@id, @project_id, @channel_id, @channel_name, @auto_triage, @exclude_bots, @poll_interval_s, @cursor_ts, @now)
RETURNING *;

-- name: GetSlackSource :one
SELECT * FROM slack_sources WHERE id = @id;

-- name: ListSlackSources :many
SELECT * FROM slack_sources ORDER BY channel_name;

-- name: ListSlackSourcesByProject :many
SELECT * FROM slack_sources WHERE project_id = @project_id ORDER BY channel_name;

-- name: UpdateSlackSource :one
UPDATE slack_sources SET auto_triage = @auto_triage, exclude_bots = @exclude_bots, poll_interval_s = @poll_interval_s
WHERE id = @id
RETURNING *;

-- name: DeleteSlackSource :exec
DELETE FROM slack_sources WHERE id = @id;

-- name: SetSlackSourcePolled :exec
UPDATE slack_sources SET cursor_ts = @cursor_ts, last_polled_at = @now, last_error = @last_error WHERE id = @id;

-- name: SetSlackSourceSwept :exec
UPDATE slack_sources SET last_thread_sweep_at = @now WHERE id = @id;

-- name: UpsertFeedback :one
-- Re-reading a message updates what can change (text, reply count) and
-- never its triage.
INSERT INTO feedback (id, project_id, source_id, channel_id, channel_name, ts, thread_ts, author_id, author_name, text,
                      permalink, reply_count, edited, posted_at, triage_status, created_at, updated_at)
VALUES (@id, @project_id, @source_id, @channel_id, @channel_name, @ts, @thread_ts, @author_id, @author_name, @text,
        @permalink, @reply_count, @edited, @posted_at, @triage_status, @now, @now)
ON CONFLICT (channel_id, ts) DO UPDATE SET
    text = excluded.text,
    edited = excluded.edited,
    reply_count = MAX(feedback.reply_count, excluded.reply_count),
    author_name = CASE WHEN excluded.author_name != '' THEN excluded.author_name ELSE feedback.author_name END,
    permalink = CASE WHEN excluded.permalink != '' THEN excluded.permalink ELSE feedback.permalink END,
    updated_at = excluded.updated_at
RETURNING *;

-- name: GetFeedback :one
SELECT * FROM feedback WHERE id = @id;

-- name: GetFeedbackByTS :one
SELECT * FROM feedback WHERE channel_id = @channel_id AND ts = @ts;

-- name: ListFeedback :many
SELECT feedback.*, COALESCE(units.seq, 0) AS unit_seq, COALESCE(units.title, '') AS unit_title, COALESCE(units.state, '') AS unit_state
FROM feedback LEFT JOIN units ON units.id = feedback.unit_id
WHERE (sqlc.narg('project_id') IS NULL OR feedback.project_id = sqlc.narg('project_id'))
ORDER BY feedback.posted_at DESC
LIMIT @lim;

-- name: ListFeedbackByIDs :many
SELECT * FROM feedback WHERE id IN (sqlc.slice('ids')) ORDER BY posted_at;

-- name: ListFeedbackByUnit :many
SELECT * FROM feedback WHERE unit_id = @unit_id ORDER BY posted_at;

-- name: ListThreadReplies :many
SELECT * FROM feedback WHERE channel_id = @channel_id AND thread_ts = @thread_ts AND ts != @thread_ts ORDER BY posted_at;

-- name: ListNewFeedback :many
SELECT * FROM feedback WHERE project_id = @project_id AND triage_status = 'new' ORDER BY posted_at LIMIT @lim;

-- name: ListProjectsWithNewFeedback :many
SELECT DISTINCT project_id FROM feedback WHERE triage_status = 'new';

-- name: SetFeedbackStatus :exec
UPDATE feedback SET triage_status = @triage_status, updated_at = @now WHERE id IN (sqlc.slice('ids'));

-- name: SetFeedbackTriage :exec
UPDATE feedback SET triage_status = @triage_status, triage = @triage, unit_id = @unit_id, updated_at = @now WHERE id = @id;

-- name: LinkFeedback :exec
UPDATE feedback SET unit_id = @unit_id, triage_status = @triage_status, updated_at = @now WHERE id IN (sqlc.slice('ids'));

-- name: UnlinkFeedbackFromUnit :exec
UPDATE feedback SET unit_id = NULL, triage_status = 'inbox', updated_at = @now WHERE unit_id = @unit_id;

-- name: ResetTriagingFeedback :exec
UPDATE feedback SET triage_status = 'new' WHERE triage_status = 'triaging';

-- name: CountFeedbackByStatus :many
SELECT triage_status, COUNT(*) AS n FROM feedback
WHERE (sqlc.narg('project_id') IS NULL OR project_id = sqlc.narg('project_id'))
GROUP BY triage_status;
