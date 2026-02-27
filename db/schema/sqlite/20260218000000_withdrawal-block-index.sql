-- +goose Up
-- +goose StatementBegin

-- Recreate el_withdrawals with block_index column and new primary key.
-- SQLite does not support ALTER TABLE for changing primary keys.
CREATE TABLE IF NOT EXISTS "el_withdrawals_new" (
    block_uid INTEGER NOT NULL,
    block_index INTEGER NOT NULL DEFAULT 0,
    account_id INTEGER NOT NULL,
    type INTEGER NOT NULL DEFAULT 0,
    amount REAL NOT NULL DEFAULT 0,
    amount_raw BLOB NOT NULL,
    validator INTEGER NULL,
    CONSTRAINT el_withdrawals_pkey PRIMARY KEY (block_uid, block_index)
);

-- Assign sequential block_index values per block_uid using ROW_NUMBER()
INSERT INTO "el_withdrawals_new" (block_uid, block_index, account_id, type, amount, amount_raw, validator)
    SELECT block_uid,
           (ROW_NUMBER() OVER (PARTITION BY block_uid ORDER BY type, account_id) - 1),
           account_id, type, amount, amount_raw, validator
    FROM "el_withdrawals";

DROP TABLE IF EXISTS "el_withdrawals";
ALTER TABLE "el_withdrawals_new" RENAME TO "el_withdrawals";

CREATE INDEX IF NOT EXISTS "el_withdrawals_block_uid_idx"
    ON "el_withdrawals"
    ("block_uid" ASC);

CREATE INDEX IF NOT EXISTS "el_withdrawals_account_id_idx"
    ON "el_withdrawals"
    ("account_id" ASC);

CREATE INDEX IF NOT EXISTS "el_withdrawals_type_idx"
    ON "el_withdrawals"
    ("type" ASC);

CREATE INDEX IF NOT EXISTS "el_withdrawals_validator_idx"
    ON "el_withdrawals"
    ("validator" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
