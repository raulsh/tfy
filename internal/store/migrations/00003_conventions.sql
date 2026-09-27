-- +goose Up
-- Conventions that apply to every repository of a project. tfy writes them
-- into the CLAUDE.md of each unit workspace, next to the repositories' own.
ALTER TABLE projects ADD COLUMN conventions TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE projects DROP COLUMN conventions;
