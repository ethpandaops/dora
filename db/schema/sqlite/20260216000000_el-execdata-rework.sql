-- +goose Up
-- +goose StatementBegin

-- Drop el_tx_events table (event data moves to blockdb in Mode 3)
DROP TABLE IF EXISTS "el_tx_events";

-- Drop el_internal_transactions table (replaced by trace-based el_transactions_internal)
DROP TABLE IF EXISTS "el_internal_transactions";

-- Add data tracking columns to el_blocks for blockdb data availability
-- data_status bit flags: 0x01=events, 0x02=call traces, 0x04=state changes
-- data_size: compressed size in bytes of blockdb object (0 = no data)
ALTER TABLE "el_blocks" ADD COLUMN data_status INTEGER NOT NULL DEFAULT 0;
ALTER TABLE "el_blocks" ADD COLUMN data_size INTEGER NOT NULL DEFAULT 0;

-- Table for internal transaction index (lightweight index from call traces)
CREATE TABLE IF NOT EXISTS "el_transactions_internal" (
    block_uid INTEGER NOT NULL,
    tx_hash BLOB NOT NULL,
    tx_callidx INTEGER NOT NULL,
    call_type INTEGER NOT NULL DEFAULT 0,
    from_id INTEGER NOT NULL DEFAULT 0,
    to_id INTEGER NOT NULL DEFAULT 0,
    value REAL NOT NULL DEFAULT 0,
    value_raw BLOB,
    CONSTRAINT el_transactions_internal_pkey PRIMARY KEY (block_uid, tx_hash, tx_callidx)
);

CREATE INDEX IF NOT EXISTS "el_internal_tx_from_idx"
    ON "el_transactions_internal"
    ("from_id" ASC, "block_uid" DESC);

CREATE INDEX IF NOT EXISTS "el_internal_tx_to_idx"
    ON "el_transactions_internal"
    ("to_id" ASC, "block_uid" DESC);

-- Table for lightweight event index (searchable index, full data in blockdb)
CREATE TABLE IF NOT EXISTS "el_event_index" (
    block_uid INTEGER NOT NULL,
    tx_hash BLOB NOT NULL,
    event_index INTEGER NOT NULL,
    source_id INTEGER NOT NULL DEFAULT 0,
    topic1 BLOB,
    CONSTRAINT el_event_index_pkey PRIMARY KEY (block_uid, tx_hash, event_index)
);

CREATE INDEX IF NOT EXISTS "el_event_index_source_idx"
    ON "el_event_index"
    ("source_id" ASC, "block_uid" DESC);

CREATE INDEX IF NOT EXISTS "el_event_index_topic1_idx"
    ON "el_event_index"
    ("topic1" ASC, "block_uid" DESC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
