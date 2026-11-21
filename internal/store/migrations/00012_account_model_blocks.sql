-- +goose Up
-- Models an account cannot run for now, such as Fable on a subscription
-- without usage credits: a JSON object of model to the time it may be tried
-- again. The account runs other models meanwhile.
ALTER TABLE accounts ADD COLUMN model_blocks TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE accounts DROP COLUMN model_blocks;
