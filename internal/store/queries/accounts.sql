-- name: ListAccounts :many
SELECT * FROM accounts ORDER BY created_at, id;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = @id;

-- name: CreateAccount :one
INSERT INTO accounts (id, name, cli, kind, settings, created_at, updated_at)
VALUES (@id, @name, @cli, @kind, @settings, @now, @now)
RETURNING *;

-- name: UpdateAccount :one
UPDATE accounts SET name = @name, settings = @settings, updated_at = @now WHERE id = @id
RETURNING *;

-- name: DeleteAccount :exec
DELETE FROM accounts WHERE id = @id;

-- name: PauseAccount :exec
-- A pause only ever moves later. Times are stored in UTC, so they compare
-- as text.
UPDATE accounts SET paused_until = @until, pause_reason = @reason
WHERE id = @id AND (paused_until IS NULL OR paused_until < @until);

-- name: ResumeAccounts :exec
-- Lifts the accounts' pauses and the blocks on their models alike.
UPDATE accounts SET paused_until = NULL, pause_reason = '', model_blocks = '{}' WHERE id IN (sqlc.slice('ids'));

-- name: SetAccountModelBlocks :exec
UPDATE accounts SET model_blocks = @model_blocks WHERE id = @id;

-- name: SetAccountQuota :exec
UPDATE accounts SET quota = @quota, quota_at = @now WHERE id = @id;

-- name: GetAppSetting :one
SELECT value FROM app_settings WHERE key = @key;

-- name: PutAppSetting :exec
INSERT INTO app_settings (key, value, updated_at) VALUES (@key, @value, @now)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;
