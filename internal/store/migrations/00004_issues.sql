-- +goose Up
-- GitHub issues linked to units. tfy keeps a copy of each issue for its
-- runs, and writes to an issue only when a person applies a suggestion.
CREATE TABLE unit_issues (
    id               TEXT PRIMARY KEY,
    unit_id          TEXT NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    repo             TEXT NOT NULL, -- owner/name
    number           INTEGER NOT NULL,
    url              TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT '', -- open | closed
    author           TEXT NOT NULL DEFAULT '',
    labels           TEXT NOT NULL DEFAULT '[]',
    body             TEXT NOT NULL DEFAULT '',
    comments         TEXT NOT NULL DEFAULT '[]', -- [{author, at, body}]
    public           BOOLEAN NOT NULL DEFAULT 0,
    -- The unit's pull requests say "Closes" (merging closes the issue)
    -- rather than "Refs".
    closes           BOOLEAN NOT NULL DEFAULT 1,
    issue_updated_at DATETIME,
    fetched_at       DATETIME,
    suggestion       TEXT NOT NULL DEFAULT '', -- JSON: pipeline.IssueSuggestion
    suggestion_state TEXT NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL,
    updated_at       DATETIME NOT NULL,
    UNIQUE (unit_id, repo, number)
);
CREATE INDEX unit_issues_unit ON unit_issues (unit_id, created_at);

-- +goose Down
DROP TABLE unit_issues;
