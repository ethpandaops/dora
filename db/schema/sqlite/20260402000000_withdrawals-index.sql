-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "withdrawals" (
    block_uid INTEGER NOT NULL,
    block_idx INTEGER NOT NULL DEFAULT 0,
    type INTEGER NOT NULL DEFAULT 0,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    validator INTEGER NULL,
    account_id INTEGER NULL,
    amount REAL NOT NULL DEFAULT 0,
    amount_raw BLOB NOT NULL,
    CONSTRAINT withdrawals_pkey PRIMARY KEY (block_uid, block_idx)
);

CREATE INDEX "withdrawals_block_uid_idx" ON "withdrawals" ("block_uid" ASC);
CREATE INDEX "withdrawals_validator_idx" ON "withdrawals" ("validator" ASC);
CREATE INDEX "withdrawals_account_id_idx" ON "withdrawals" ("account_id" ASC);
CREATE INDEX "withdrawals_type_idx" ON "withdrawals" ("type" ASC);
CREATE INDEX "withdrawals_orphaned_idx" ON "withdrawals" ("orphaned");

INSERT INTO withdrawals (block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount, amount_raw)
    SELECT block_uid, block_index, type, FALSE, 0, validator, account_id, amount, amount_raw
    FROM el_withdrawals;

DROP TABLE IF EXISTS "el_withdrawals";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS "withdrawals";
-- +goose StatementEnd
