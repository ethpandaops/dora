-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."consolidation_request_txs" (
    block_number BIGINT NOT NULL,
    block_index INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root bytea NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address bytea NOT NULL,
    source_pubkey bytea NULL,
    target_pubkey bytea NULL,
    tx_hash bytea NULL,
    tx_sender bytea NOT NULL,
    tx_target bytea NOT NULL,
    dequeue_block BIGINT NOT NULL,
    CONSTRAINT consolidation_pkey PRIMARY KEY (block_root, block_index)
);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_block_number_idx"
    ON public."consolidation_request_txs"
    ("block_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_source_addr_idx"
    ON public."consolidation_request_txs"
    ("source_address" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_fork_idx"
    ON public."consolidation_request_txs"
    ("fork_id" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_dequeue_block_idx"
    ON public."consolidation_request_txs"
    ("dequeue_block" ASC NULLS FIRST);

CREATE TABLE IF NOT EXISTS public."withdrawal_request_txs" (
    block_number BIGINT NOT NULL,
    block_index INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root bytea NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address bytea NOT NULL,
    validator_pubkey bytea NOT NULL,
    amount BIGINT NOT NULL,
    tx_hash bytea NULL,
    tx_sender bytea NOT NULL,
    tx_target bytea NOT NULL,
    dequeue_block BIGINT NOT NULL,
    CONSTRAINT withdrawal_request_txs_pkey PRIMARY KEY (block_root, block_index)
);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_block_number_idx"
    ON public."withdrawal_request_txs"
    ("block_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_source_addr_idx"
    ON public."withdrawal_request_txs"
    ("source_address" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_fork_idx"
    ON public."withdrawal_request_txs"
    ("fork_id" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_dequeue_block_idx"
    ON public."withdrawal_request_txs"
    ("dequeue_block" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
