-- +goose Up
-- +goose StatementBegin

-- Table for EL internal transactions (EIP-7708 internal ETH transfers)
CREATE TABLE IF NOT EXISTS public."el_internal_transactions" (
    block_uid BIGINT NOT NULL,
    tx_hash bytea NOT NULL,
    tx_idx INT NOT NULL,
    from_id BIGINT NOT NULL,
    to_id BIGINT NOT NULL,
    amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    amount_raw bytea NOT NULL,
    CONSTRAINT el_internal_transactions_pkey PRIMARY KEY (block_uid, tx_hash, tx_idx)
);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_block_uid_idx"
    ON public."el_internal_transactions"
    ("block_uid" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_tx_hash_idx"
    ON public."el_internal_transactions"
    ("tx_hash" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_from_idx"
    ON public."el_internal_transactions"
    ("from_id" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_to_idx"
    ON public."el_internal_transactions"
    ("to_id" ASC NULLS FIRST);

-- Composite indexes for common query patterns (from_id/to_id + block_uid for sorting)
CREATE INDEX IF NOT EXISTS "el_internal_transactions_from_block_idx"
    ON public."el_internal_transactions"
    ("from_id" ASC NULLS FIRST, "block_uid" DESC NULLS LAST);

CREATE INDEX IF NOT EXISTS "el_internal_transactions_to_block_idx"
    ON public."el_internal_transactions"
    ("to_id" ASC NULLS FIRST, "block_uid" DESC NULLS LAST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
