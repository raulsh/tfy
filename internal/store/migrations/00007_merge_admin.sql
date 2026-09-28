-- +goose Up
-- A person chose to merge the unit's pull requests as a GitHub
-- administrator, bypassing the base branch's rules, for as long as the merge
-- run drives this merge.
ALTER TABLE units ADD COLUMN merge_admin BOOLEAN NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE units DROP COLUMN merge_admin;
