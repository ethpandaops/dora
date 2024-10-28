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
    source_index BIGINT NULL,
    target_pubkey bytea NULL,
    target_index BIGINT NULL,
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

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_source_idx"
    ON public."consolidation_request_txs"
    ("source_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_target_idx"
    ON public."consolidation_request_txs"
    ("target_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_tx_hash_idx"
    ON public."consolidation_request_txs"
    ("tx_hash" ASC);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_fork_idx"
    ON public."consolidation_request_txs"
    ("fork_id" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_dequeue_block_idx"
    ON public."consolidation_request_txs"
    ("dequeue_block" ASC NULLS FIRST);

-- add block_number to consolidation_requests
ALTER TABLE public."consolidation_requests"
    ADD "block_number" BIGINT NOT NULL DEFAULT 0;

UPDATE public."consolidation_requests"
    SET "block_number" = (
        SELECT eth_block_number 
        FROM public."slots" 
        WHERE public."slots".root = public."consolidation_requests".slot_root
    );

CREATE INDEX IF NOT EXISTS "consolidation_requests_block_number_idx"
    ON public."consolidation_requests"
    ("block_number" ASC NULLS FIRST);

CREATE TABLE IF NOT EXISTS public."withdrawal_request_txs" (
    block_number BIGINT NOT NULL,
    block_index INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root bytea NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address bytea NOT NULL,
    validator_pubkey bytea NOT NULL,
    validator_index BIGINT NULL,
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

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_validator_index_idx"
    ON public."withdrawal_request_txs"
    ("validator_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_amount_idx"
    ON public."withdrawal_request_txs"
    ("amount" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_tx_hash_idx"
    ON public."withdrawal_request_txs"
    ("tx_hash" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_fork_idx"
    ON public."withdrawal_request_txs"
    ("fork_id" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_dequeue_block_idx"
    ON public."withdrawal_request_txs"
    ("dequeue_block" ASC NULLS FIRST);

-- add block_number to withdrawal_requests
ALTER TABLE public."withdrawal_requests"
    ADD "block_number" BIGINT NOT NULL DEFAULT 0;

UPDATE public."withdrawal_requests"
    SET "block_number" = (
        SELECT eth_block_number 
        FROM public."slots" 
        WHERE public."slots".root = public."withdrawal_requests".slot_root
    );

CREATE INDEX IF NOT EXISTS "withdrawal_requests_block_number_idx"
    ON public."withdrawal_requests"
    ("block_number" ASC NULLS FIRST);


-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
