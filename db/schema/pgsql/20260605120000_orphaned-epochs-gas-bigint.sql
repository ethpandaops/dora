-- +goose Up
-- +goose StatementBegin

-- orphaned_epochs.eth_gas_used / eth_gas_limit were created as integer (Int4),
-- but per-epoch sums exceed 2.147B (32 slots * ~100M gas). The matching
-- columns on the canonical "epochs" table are bigint (migration
-- 20250510232604); align orphaned_epochs to match.

ALTER TABLE "orphaned_epochs"
    ALTER COLUMN "eth_gas_used" TYPE bigint,
    ALTER COLUMN "eth_gas_limit" TYPE bigint;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
