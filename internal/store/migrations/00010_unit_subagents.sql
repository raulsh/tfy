-- +goose Up
-- Whether the unit's runs may start sub-agents (Claude Code's Agent tool).
ALTER TABLE units ADD COLUMN subagents BOOLEAN NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE units DROP COLUMN subagents;
