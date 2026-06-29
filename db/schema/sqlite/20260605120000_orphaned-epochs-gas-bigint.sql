-- +goose Up
-- +goose StatementBegin

-- No-op for sqlite: INTEGER affinity is already 64-bit. The matching pgsql
-- migration widens orphaned_epochs.eth_gas_used / eth_gas_limit from Int4 to
-- bigint to accommodate per-epoch gas sums above 2.147B.

SELECT 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
