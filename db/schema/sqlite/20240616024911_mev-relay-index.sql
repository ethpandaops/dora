-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS mev_blocks (
    slot_number BIGINT NOT NULL,
    block_hash BLOB NOT NULL,
    block_number INT NOT NULL,
    builder_pubkey BLOB NOT NULL,
    proposer_index BIGINT NULL,
    proposed INT NOT NULL,
    seenby_relays BIGINT NOT NULL,
    fee_recipient BLOB NOT NULL,
    tx_count INT NOT NULL,
    gas_used INT NOT NULL,
    block_value BLOB NOT NULL,
    block_value_gwei BIGINT NOT NULL,
    CONSTRAINT mev_blocks_pkey PRIMARY KEY (block_hash)
);

CREATE INDEX IF NOT EXISTS "mev_blocks_slot_idx"
    ON "mev_blocks"
    ("slot_number" ASC);

CREATE INDEX IF NOT EXISTS "mev_blocks_builder_pubkey_idx"
    ON "mev_blocks"
    ("builder_pubkey" ASC);

CREATE INDEX IF NOT EXISTS "mev_blocks_proposer_index_idx"
    ON "mev_blocks"
    ("proposer_index" ASC);

CREATE INDEX IF NOT EXISTS "mev_blocks_proposed_idx"
    ON "mev_blocks"
    ("proposed" ASC);

CREATE INDEX IF NOT EXISTS "mev_blocks_seenby_relays_idx"
    ON "mev_blocks"
    ("seenby_relays" ASC);

CREATE INDEX IF NOT EXISTS "mev_blocks_fee_recipient_idx"
    ON "mev_blocks"
    ("fee_recipient" ASC);

CREATE INDEX IF NOT EXISTS "mev_blocks_block_value_gwei_idx"
    ON "mev_blocks"
    ("block_value_gwei" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
