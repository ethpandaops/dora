-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "withdrawals" (
    block_uid INTEGER NOT NULL,
    block_idx INTEGER NOT NULL DEFAULT 0,
    type INTEGER NOT NULL DEFAULT 0,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    validator INTEGER NOT NULL DEFAULT 0,
    account_id INTEGER NULL,
    amount BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT withdrawals_pkey PRIMARY KEY (block_uid, block_idx)
);

CREATE INDEX "withdrawals_block_uid_idx" ON "withdrawals" ("block_uid" ASC);
CREATE INDEX "withdrawals_validator_idx" ON "withdrawals" ("validator" ASC);
CREATE INDEX "withdrawals_account_id_idx" ON "withdrawals" ("account_id" ASC);
CREATE INDEX "withdrawals_type_idx" ON "withdrawals" ("type" ASC);
CREATE INDEX "withdrawals_orphaned_idx" ON "withdrawals" ("orphaned");

-- Seed CL withdrawals from old table (type=0 -> sweep=2), skip fee recipients (type=1)
-- Convert amount from float ETH to Gwei
INSERT INTO withdrawals (block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount)
    SELECT block_uid, block_index, 2, FALSE, 0, COALESCE(validator, 0), account_id,
           CAST(amount * 1000000000 AS INTEGER)
    FROM el_withdrawals
    WHERE type = 0;

-- Add fee columns to el_blocks
ALTER TABLE "el_blocks" ADD COLUMN fee_amount REAL NOT NULL DEFAULT 0;
ALTER TABLE "el_blocks" ADD COLUMN fee_amount_raw BLOB;
ALTER TABLE "el_blocks" ADD COLUMN fee_account_id INTEGER NULL;

CREATE INDEX "el_blocks_fee_account_id_idx" ON "el_blocks" ("fee_account_id" ASC);

-- Migrate fee recipient entries (type=1) from old table to el_blocks
UPDATE el_blocks SET
    fee_amount = (SELECT w.amount FROM el_withdrawals w WHERE w.block_uid = el_blocks.block_uid AND w.type = 1),
    fee_amount_raw = (SELECT w.amount_raw FROM el_withdrawals w WHERE w.block_uid = el_blocks.block_uid AND w.type = 1),
    fee_account_id = (SELECT w.account_id FROM el_withdrawals w WHERE w.block_uid = el_blocks.block_uid AND w.type = 1)
WHERE EXISTS (SELECT 1 FROM el_withdrawals w WHERE w.block_uid = el_blocks.block_uid AND w.type = 1);

DROP TABLE IF EXISTS "el_withdrawals";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS "withdrawals";
-- +goose StatementEnd
