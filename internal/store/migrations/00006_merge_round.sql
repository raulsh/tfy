-- +goose Up
-- Merge plans gave way to the merge run, which decides each next step
-- itself. merge_round counts its decisions while it drives a merge; 0 means
-- no merge is under way.
ALTER TABLE units RENAME COLUMN merge_step TO merge_round;
UPDATE units SET merge_round = 0;

-- +goose Down
ALTER TABLE units RENAME COLUMN merge_round TO merge_step;
