-- +goose Up
CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    product_context TEXT NOT NULL DEFAULT '',
    settings        TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL
);

CREATE TABLE repos (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    full_name      TEXT NOT NULL,
    default_branch TEXT NOT NULL,
    clone_url      TEXT NOT NULL DEFAULT '',
    clone_path     TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    UNIQUE (project_id, full_name)
);

CREATE TABLE units (
    id               TEXT PRIMARY KEY,
    seq              INTEGER NOT NULL UNIQUE,
    project_id       TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    parent_unit_id   TEXT REFERENCES units (id) ON DELETE SET NULL,
    kind             TEXT NOT NULL,
    title            TEXT NOT NULL,
    summary          TEXT NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    origin           TEXT NOT NULL,
    state            TEXT NOT NULL,
    attention        TEXT NOT NULL DEFAULT '',
    attention_detail TEXT NOT NULL DEFAULT '',
    review_iteration INTEGER NOT NULL DEFAULT 0,
    workspace_path   TEXT NOT NULL DEFAULT '',
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL,
    updated_at       DATETIME NOT NULL
);
CREATE INDEX units_project_state ON units (project_id, state);

CREATE TABLE unit_repos (
    unit_id       TEXT NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    repo_id       TEXT NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    checkout_path TEXT NOT NULL DEFAULT '',
    base_sha      TEXT NOT NULL DEFAULT '',
    is_target     BOOLEAN NOT NULL DEFAULT 0,
    branch        TEXT NOT NULL DEFAULT '',
    head_sha      TEXT NOT NULL DEFAULT '',
    reviewed_sha  TEXT NOT NULL DEFAULT '',
    publish_state TEXT NOT NULL DEFAULT 'pending',
    pr_number     INTEGER NOT NULL DEFAULT 0,
    pr_url        TEXT NOT NULL DEFAULT '',
    pr_state      TEXT NOT NULL DEFAULT '',
    checks_state  TEXT NOT NULL DEFAULT '',
    merge_sha     TEXT NOT NULL DEFAULT '',
    merged_at     DATETIME,
    updated_at    DATETIME NOT NULL,
    PRIMARY KEY (unit_id, repo_id)
);

CREATE TABLE documents (
    id         TEXT PRIMARY KEY,
    unit_id    TEXT NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    version    INTEGER NOT NULL,
    content    TEXT NOT NULL,
    meta       TEXT NOT NULL DEFAULT '{}',
    author     TEXT NOT NULL,
    run_id     TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    UNIQUE (unit_id, kind, version)
);

CREATE TABLE runs (
    id              TEXT PRIMARY KEY,
    unit_id         TEXT REFERENCES units (id) ON DELETE CASCADE,
    project_id      TEXT REFERENCES projects (id) ON DELETE CASCADE,
    parent_run_id   TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL,
    status          TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    session_id      TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    effort          TEXT NOT NULL DEFAULT '',
    permission_mode TEXT NOT NULL DEFAULT '',
    prompt_version  TEXT NOT NULL DEFAULT '',
    cwd             TEXT NOT NULL DEFAULT '',
    pid             INTEGER NOT NULL DEFAULT 0,
    cost_usd        REAL NOT NULL DEFAULT 0,
    cost_total_usd  REAL NOT NULL DEFAULT 0,
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    turns           INTEGER NOT NULL DEFAULT 0,
    denials         INTEGER NOT NULL DEFAULT 0,
    result          TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    started_at      DATETIME,
    ended_at        DATETIME
);
CREATE INDEX runs_unit ON runs (unit_id, created_at);
CREATE INDEX runs_created ON runs (created_at);

CREATE TABLE run_events (
    run_id  TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    seq     INTEGER NOT NULL,
    at      DATETIME NOT NULL,
    type    TEXT NOT NULL,
    subtype TEXT NOT NULL DEFAULT '',
    tool    TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    PRIMARY KEY (run_id, seq)
);

CREATE TABLE jobs (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,
    unit_id     TEXT REFERENCES units (id) ON DELETE CASCADE,
    project_id  TEXT REFERENCES projects (id) ON DELETE CASCADE,
    payload     TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    dedupe_key  TEXT,
    run_after   DATETIME NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL,
    started_at  DATETIME,
    finished_at DATETIME
);
-- One active job per key: a double-clicked action cannot start work twice.
CREATE UNIQUE INDEX jobs_active_dedupe ON jobs (dedupe_key)
    WHERE status IN ('queued', 'running') AND dedupe_key IS NOT NULL;
CREATE INDEX jobs_ready ON jobs (status, run_after);

CREATE TABLE activity (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    unit_id    TEXT REFERENCES units (id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects (id) ON DELETE CASCADE,
    at         DATETIME NOT NULL,
    actor      TEXT NOT NULL,
    kind       TEXT NOT NULL,
    message    TEXT NOT NULL,
    data       TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX activity_unit ON activity (unit_id, id);

CREATE TABLE slack_sources (
    id                   TEXT PRIMARY KEY,
    project_id           TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    channel_id           TEXT NOT NULL UNIQUE,
    channel_name         TEXT NOT NULL,
    auto_triage          BOOLEAN NOT NULL DEFAULT 1,
    exclude_bots         BOOLEAN NOT NULL DEFAULT 1,
    poll_interval_s      INTEGER NOT NULL DEFAULT 120,
    cursor_ts            TEXT NOT NULL DEFAULT '',
    last_polled_at       DATETIME,
    last_thread_sweep_at DATETIME,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           DATETIME NOT NULL
);

CREATE TABLE feedback (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    source_id     TEXT REFERENCES slack_sources (id) ON DELETE SET NULL,
    channel_id    TEXT NOT NULL,
    channel_name  TEXT NOT NULL DEFAULT '',
    ts            TEXT NOT NULL,
    thread_ts     TEXT NOT NULL DEFAULT '',
    author_id     TEXT NOT NULL DEFAULT '',
    author_name   TEXT NOT NULL DEFAULT '',
    text          TEXT NOT NULL,
    permalink     TEXT NOT NULL DEFAULT '',
    reply_count   INTEGER NOT NULL DEFAULT 0,
    edited        BOOLEAN NOT NULL DEFAULT 0,
    posted_at     DATETIME NOT NULL,
    triage_status TEXT NOT NULL DEFAULT 'new',
    triage        TEXT NOT NULL DEFAULT '{}',
    unit_id       TEXT REFERENCES units (id) ON DELETE SET NULL,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    UNIQUE (channel_id, ts)
);
CREATE INDEX feedback_project_status ON feedback (project_id, triage_status);
CREATE INDEX feedback_unit ON feedback (unit_id);

-- +goose Down
DROP TABLE feedback;
DROP TABLE slack_sources;
DROP TABLE activity;
DROP TABLE jobs;
DROP TABLE run_events;
DROP TABLE runs;
DROP TABLE documents;
DROP TABLE unit_repos;
DROP TABLE units;
DROP TABLE repos;
DROP TABLE projects;
