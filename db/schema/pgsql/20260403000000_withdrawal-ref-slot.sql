-- +goose Up
-- +goose StatementBegin
ALTER TABLE public."withdrawals" ADD COLUMN IF NOT EXISTS ref_slot BIGINT NULL;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
