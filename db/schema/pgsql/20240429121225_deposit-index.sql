-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS deposit_txs (
    deposit_index INT NOT NULL,
    block_number INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root bytea NOT NULL,
    publickey bytea NOT NULL,
    withdrawalcredentials bytea NOT NULL,
    amount BIGINT NOT NULL,
    signature bytea NOT NULL,
    valid_signature bool NOT NULL DEFAULT TRUE,
    orphaned bool NOT NULL DEFAULT FALSE,
    tx_hash bytea NOT NULL,
    tx_sender bytea NOT NULL,
    tx_target bytea NOT NULL,
    CONSTRAINT deposit_txs_pkey PRIMARY KEY (deposit_index, block_root)
);

CREATE INDEX IF NOT EXISTS "deposit_txs_deposit_index_idx"
    ON public."deposit_txs"
    ("deposit_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_block_number_idx"
    ON public."deposit_txs"
    ("block_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_publickey_idx"
    ON public."deposit_txs"
    ("publickey" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_tx_sender_idx"
    ON public."deposit_txs"
    ("tx_sender" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_tx_target_idx"
    ON public."deposit_txs"
    ("tx_target" ASC NULLS FIRST);

CREATE TABLE IF NOT EXISTS deposits (
    deposit_index INT NULL,
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root bytea NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    publickey bytea NOT NULL,
    withdrawalcredentials bytea NOT NULL,
    amount BIGINT NOT NULL,
    CONSTRAINT deposit_pkey PRIMARY KEY (slot_index, slot_root)
);

CREATE INDEX IF NOT EXISTS "deposits_deposit_index_idx"
    ON public."deposits"
    ("deposit_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposits_slot_number_idx"
    ON public."deposits"
    ("slot_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposits_publickey_idx"
    ON public."deposits"
    ("publickey" ASC NULLS FIRST);


-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
