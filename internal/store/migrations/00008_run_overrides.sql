-- +goose Up
-- The models and effort levels chosen for the unit's runs when it was
-- created, by kind of run, over the configuration's.
ALTER TABLE units ADD COLUMN run_overrides TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE units DROP COLUMN run_overrides;
