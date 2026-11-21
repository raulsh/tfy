-- +goose Up
-- An account is a coding CLI and how it signs in. Secrets are kept in files
-- under the data directory, never here.
CREATE TABLE accounts (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    cli          TEXT NOT NULL,
    kind         TEXT NOT NULL,
    settings     TEXT NOT NULL DEFAULT '{}',
    paused_until DATETIME,
    pause_reason TEXT NOT NULL DEFAULT '',
    -- The last usage report, as JSON: utilization and reset per window.
    quota        TEXT NOT NULL DEFAULT '',
    quota_at     DATETIME,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);
-- This machine's Claude Code login, which every run used until now.
INSERT INTO accounts (id, name, cli, kind, created_at, updated_at)
VALUES ('machine', 'This machine''s Claude login', 'claude', 'machine', datetime('now'), datetime('now'));

-- Settings that are not per project, as JSON by key.
CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL
);
INSERT INTO app_settings (key, value, updated_at)
VALUES ('runs', '{"accounts":["machine"],"stages":{}}', datetime('now'));

ALTER TABLE runs ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN engine TEXT NOT NULL DEFAULT 'claude';
UPDATE runs SET account_id = 'machine';

-- What a queued job waits for, such as a free account, so it can be woken
-- as soon as that changes.
ALTER TABLE jobs ADD COLUMN waiting_on TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN waiting_on;
ALTER TABLE runs DROP COLUMN engine;
ALTER TABLE runs DROP COLUMN account_id;
DROP TABLE app_settings;
DROP TABLE accounts;
