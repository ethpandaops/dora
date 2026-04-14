-- +goose Up
-- +goose StatementBegin
ALTER TABLE "withdrawals" ADD COLUMN ref_slot BIGINT NULL;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
