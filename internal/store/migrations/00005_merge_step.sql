-- +goose Up
-- The step of the unit's merge plan being merged or prepared, from 0. A
-- unit without a merge plan has one step.
ALTER TABLE units ADD COLUMN merge_step INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE units DROP COLUMN merge_step;
