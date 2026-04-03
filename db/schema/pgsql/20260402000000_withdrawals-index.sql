-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."withdrawals" (
    block_uid BIGINT NOT NULL,
    block_idx SMALLINT NOT NULL DEFAULT 0,
    type SMALLINT NOT NULL DEFAULT 0,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    validator BIGINT NOT NULL DEFAULT 0,
    account_id BIGINT NOT NULL DEFAULT 0,
    amount BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT withdrawals_pkey PRIMARY KEY (block_uid, block_idx)
);

CREATE INDEX IF NOT EXISTS "withdrawals_block_uid_idx" ON public."withdrawals" ("block_uid" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_validator_idx" ON public."withdrawals" ("validator" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_account_id_idx" ON public."withdrawals" ("account_id" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_type_idx" ON public."withdrawals" ("type" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_orphaned_idx" ON public."withdrawals" ("orphaned");

-- Seed CL withdrawals from old table (type=0 -> sweep=2), skip fee recipients (type=1)
-- Convert amount from float ETH to Gwei
INSERT INTO withdrawals (block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount)
    SELECT block_uid, block_index, 2, FALSE, 0, COALESCE(validator, 0), COALESCE(account_id, 0),
           CAST(amount * 1000000000 AS BIGINT)
    FROM el_withdrawals
    WHERE type = 0;

-- Add fee columns to el_blocks
ALTER TABLE public."el_blocks" ADD COLUMN IF NOT EXISTS fee_amount DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE public."el_blocks" ADD COLUMN IF NOT EXISTS fee_amount_raw bytea;
ALTER TABLE public."el_blocks" ADD COLUMN IF NOT EXISTS fee_account_id BIGINT NULL;

CREATE INDEX IF NOT EXISTS "el_blocks_fee_account_id_idx" ON public."el_blocks" ("fee_account_id" ASC NULLS FIRST);

-- Migrate fee recipient entries (type=1) from old table to el_blocks
UPDATE el_blocks SET
    fee_amount = w.amount,
    fee_amount_raw = w.amount_raw,
    fee_account_id = w.account_id
FROM el_withdrawals w
WHERE w.block_uid = el_blocks.block_uid AND w.type = 1;

DROP TABLE IF EXISTS public."el_withdrawals";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public."withdrawals";
-- +goose StatementEnd
