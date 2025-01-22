-- +goose Up
-- +goose StatementBegin

UPDATE "withdrawal_requests"
    SET "amount" = "amount" - 9223372036854775808;

UPDATE "withdrawal_request_txs"
    SET "amount" = "amount" - 9223372036854775808;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
