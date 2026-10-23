-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS "tx_pending_signatures_queuetime_idx"
    ON "tx_pending_signatures" ("queuetime" ASC);

CREATE INDEX IF NOT EXISTS "withdrawals_type_block_uid_idx"
    ON "withdrawals" ("type" ASC, "block_uid" ASC);
DROP INDEX IF EXISTS "withdrawals_type_idx";
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS "withdrawals_type_idx"
    ON "withdrawals" ("type" ASC);
DROP INDEX IF EXISTS "withdrawals_type_block_uid_idx";
DROP INDEX IF EXISTS "tx_pending_signatures_queuetime_idx";
-- +goose StatementEnd
