-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS deposit_txs (
    deposit_index INT NOT NULL,
    block_number INT NOT NULL,
    block_root BLOB NOT NULL,
    publickey BLOB NOT NULL,
    withdrawalcredentials BLOB NOT NULL,
    amount BIGINT NOT NULL,
    signature BLOB NOT NULL,
    valid_signature bool NOT NULL DEFAULT TRUE,
    orphaned bool NOT NULL DEFAULT FALSE,
    tx_hash BLOB NOT NULL,
    tx_sender BLOB NOT NULL,
    tx_target BLOB NOT NULL,
    CONSTRAINT deposit_txs_pkey PRIMARY KEY (deposit_index, block_root)
);

CREATE INDEX IF NOT EXISTS "deposit_txs_deposit_index_idx"
    ON "deposit_txs"
    ("deposit_index" ASC);

CREATE INDEX IF NOT EXISTS "deposit_txs_publickey_idx"
    ON "deposit_txs"
    ("publickey" ASC);

CREATE INDEX IF NOT EXISTS "deposit_txs_tx_sender_idx"
    ON "deposit_txs"
    ("tx_sender" ASC);

CREATE INDEX IF NOT EXISTS "deposit_txs_tx_target_idx"
    ON "deposit_txs"
    ("tx_target" ASC);

CREATE TABLE IF NOT EXISTS deposits (
    deposit_index INT NULL,
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root BLOB NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    publickey BLOB NOT NULL,
    withdrawalcredentials BLOB NOT NULL,
    amount BIGINT NOT NULL,
    CONSTRAINT deposit_pkey PRIMARY KEY (slot_index, slot_root)
);

CREATE INDEX IF NOT EXISTS "deposits_deposit_index_idx"
    ON "deposits"
    ("deposit_index" ASC);

CREATE INDEX IF NOT EXISTS "deposits_slot_number_idx"
    ON "deposits"
    ("slot_number" ASC);

CREATE INDEX IF NOT EXISTS "deposits_publickey_idx"
    ON "deposits"
    ("publickey" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
