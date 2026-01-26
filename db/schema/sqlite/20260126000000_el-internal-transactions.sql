-- +goose Up
-- +goose StatementBegin

-- Table for EL internal transactions (EIP-7708 internal ETH transfers)
CREATE TABLE IF NOT EXISTS "el_internal_transactions" (
    block_uid INTEGER NOT NULL,
    tx_hash BLOB NOT NULL,
    tx_idx INTEGER NOT NULL,
    from_id INTEGER NOT NULL,
    to_id INTEGER NOT NULL,
    amount REAL NOT NULL DEFAULT 0,
    amount_raw BLOB NOT NULL,
    CONSTRAINT el_internal_transactions_pkey PRIMARY KEY (block_uid, tx_hash, tx_idx)
);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_block_uid_idx"
    ON "el_internal_transactions"
    ("block_uid" ASC);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_tx_hash_idx"
    ON "el_internal_transactions"
    ("tx_hash" ASC);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_from_idx"
    ON "el_internal_transactions"
    ("from_id" ASC);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_to_idx"
    ON "el_internal_transactions"
    ("to_id" ASC);

-- Composite indexes for common query patterns (from_id/to_id + block_uid for sorting)
CREATE INDEX IF NOT EXISTS "el_internal_transactions_from_block_idx"
    ON "el_internal_transactions"
    ("from_id" ASC, "block_uid" DESC);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_to_block_idx"
    ON "el_internal_transactions"
    ("to_id" ASC, "block_uid" DESC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
