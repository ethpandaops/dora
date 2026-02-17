-- +goose Up
-- +goose StatementBegin

-- Drop el_tx_events table (event data moves to blockdb in Mode 3)
DROP TABLE IF EXISTS public."el_tx_events";

-- Drop el_internal_transactions table (replaced by trace-based el_transactions_internal)
DROP TABLE IF EXISTS public."el_internal_transactions";

-- Add data tracking columns to el_blocks for blockdb data availability
-- data_status bit flags: 0x01=events, 0x02=call traces, 0x04=state changes
-- data_size: compressed size in bytes of blockdb object (0 = no data)
ALTER TABLE public."el_blocks" ADD COLUMN IF NOT EXISTS data_status SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE public."el_blocks" ADD COLUMN IF NOT EXISTS data_size BIGINT NOT NULL DEFAULT 0;

-- Table for internal transaction index (lightweight index from call traces)
CREATE TABLE IF NOT EXISTS public."el_transactions_internal" (
    block_uid BIGINT NOT NULL,
    tx_hash bytea NOT NULL,
    tx_callidx INT NOT NULL,
    call_type SMALLINT NOT NULL DEFAULT 0,
    from_id BIGINT NOT NULL DEFAULT 0,
    to_id BIGINT NOT NULL DEFAULT 0,
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    value_raw bytea,
    CONSTRAINT el_transactions_internal_pkey PRIMARY KEY (block_uid, tx_hash, tx_callidx)
);

CREATE INDEX IF NOT EXISTS "el_internal_tx_from_idx"
    ON public."el_transactions_internal"
    ("from_id" ASC NULLS FIRST, "block_uid" DESC NULLS LAST);

CREATE INDEX IF NOT EXISTS "el_internal_tx_to_idx"
    ON public."el_transactions_internal"
    ("to_id" ASC NULLS FIRST, "block_uid" DESC NULLS LAST);

-- Table for lightweight event index (searchable index, full data in blockdb)
CREATE TABLE IF NOT EXISTS public."el_event_index" (
    block_uid BIGINT NOT NULL,
    tx_hash bytea NOT NULL,
    event_index INT NOT NULL,
    source_id BIGINT NOT NULL DEFAULT 0,
    topic1 bytea,
    CONSTRAINT el_event_index_pkey PRIMARY KEY (block_uid, tx_hash, event_index)
);

CREATE INDEX IF NOT EXISTS "el_event_index_source_idx"
    ON public."el_event_index"
    ("source_id" ASC NULLS FIRST, "block_uid" DESC NULLS LAST);

CREATE INDEX IF NOT EXISTS "el_event_index_topic1_idx"
    ON public."el_event_index"
    ("topic1" ASC NULLS FIRST, "block_uid" DESC NULLS LAST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
