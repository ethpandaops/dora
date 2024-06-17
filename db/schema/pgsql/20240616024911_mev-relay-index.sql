-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS mev_blocks (
    slot_number BIGINT NOT NULL,
    block_hash bytea NOT NULL,
    block_number INT NOT NULL,
    builder_pubkey bytea NOT NULL,
    proposer_index BIGINT NULL,
    proposed INT NOT NULL,
    seenby_relays BIGINT NOT NULL,
    fee_recipient bytea NOT NULL,
    tx_count INT NOT NULL,
    gas_used INT NOT NULL,
    block_value bytea NOT NULL,
    block_value_gwei BIGINT NOT NULL,
    CONSTRAINT mev_blocks_pkey PRIMARY KEY (block_hash)
);

CREATE INDEX IF NOT EXISTS "mev_blocks_slot_idx"
    ON public."mev_blocks"
    ("slot_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "mev_blocks_builder_pubkey_idx"
    ON public."mev_blocks"
    ("builder_pubkey" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "mev_blocks_proposer_index_idx"
    ON public."mev_blocks"
    ("proposer_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "mev_blocks_proposed_idx"
    ON public."mev_blocks"
    ("proposed" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "mev_blocks_seenby_relays_idx"
    ON public."mev_blocks"
    ("seenby_relays" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "mev_blocks_fee_recipient_idx"
    ON public."mev_blocks"
    ("fee_recipient" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "mev_blocks_block_value_gwei_idx"
    ON public."mev_blocks"
    ("block_value_gwei" ASC NULLS FIRST);


CREATE TABLE IF NOT EXISTS mev_blocks_seenby (
    block_hash bytea NOT NULL,
    mev_relay INT NOT NULL,
    CONSTRAINT mev_blocks_pkey PRIMARY KEY (block_hash)
);

CREATE INDEX IF NOT EXISTS "mev_blocks_seenby_mev_relay_idx"
    ON public."mev_blocks_seenby"
    ("mev_relay" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
