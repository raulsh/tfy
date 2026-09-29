-- +goose Up
-- Files the define and plan runs made for people to look at, such as
-- mockups and diagrams, versioned by path under docs/artifacts/. A version
-- with removed set records that the run deleted the file.
CREATE TABLE artifacts (
    id           TEXT PRIMARY KEY,
    unit_id      TEXT NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    path         TEXT NOT NULL,
    version      INTEGER NOT NULL,
    content      BLOB NOT NULL,
    content_type TEXT NOT NULL,
    size         INTEGER NOT NULL,
    sha256       TEXT NOT NULL,
    removed      BOOLEAN NOT NULL DEFAULT 0,
    author       TEXT NOT NULL,
    run_id       TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL,
    UNIQUE (unit_id, path, version)
);

-- +goose Down
DROP TABLE artifacts;
