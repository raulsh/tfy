-- +goose Up
-- CI and deploy runs on the merge commit, tracked during release.
ALTER TABLE unit_repos ADD COLUMN release_state TEXT NOT NULL DEFAULT '';
ALTER TABLE unit_repos ADD COLUMN release_runs TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE unit_repos DROP COLUMN release_runs;
ALTER TABLE unit_repos DROP COLUMN release_state;
