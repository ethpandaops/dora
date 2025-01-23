-- +goose Up
-- +goose StatementBegin

UPDATE public."withdrawal_requests"
    SET "amount" = "amount" - 9223372036854775808;

UPDATE public."withdrawal_request_txs"
    SET "amount" = "amount" - 9223372036854775808;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
