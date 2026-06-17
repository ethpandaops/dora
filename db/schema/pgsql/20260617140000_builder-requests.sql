-- +goose Up
-- +goose StatementBegin

-- builder deposit requests (CL view, attributed to the processing slot)
CREATE TABLE IF NOT EXISTS public."builder_deposits" (
    slot_number BIGINT NOT NULL,
    slot_root bytea NOT NULL,
    slot_index INT NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    public_key bytea NOT NULL,
    withdrawal_credentials bytea NOT NULL,
    amount BIGINT NOT NULL,
    signature bytea NULL,
    builder_index BIGINT NULL,
    tx_hash bytea NULL,
    block_number BIGINT NOT NULL DEFAULT 0,
    result smallint NOT NULL DEFAULT 0,
    CONSTRAINT builder_deposits_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "builder_deposits_slot_number_idx"
    ON public."builder_deposits" ("slot_number" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposits_public_key_idx"
    ON public."builder_deposits" ("public_key" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposits_builder_index_idx"
    ON public."builder_deposits" ("builder_index" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposits_amount_idx"
    ON public."builder_deposits" ("amount" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposits_block_number_idx"
    ON public."builder_deposits" ("block_number" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposits_fork_idx"
    ON public."builder_deposits" ("fork_id" ASC NULLS FIRST);

-- builder deposit requests (EL view, from the builder deposit system contract)
CREATE TABLE IF NOT EXISTS public."builder_deposit_request_txs" (
    block_number BIGINT NOT NULL,
    block_index INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root bytea NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    public_key bytea NOT NULL,
    withdrawal_credentials bytea NOT NULL,
    amount BIGINT NOT NULL,
    signature bytea NULL,
    builder_index BIGINT NULL,
    tx_hash bytea NULL,
    tx_sender bytea NOT NULL,
    tx_target bytea NOT NULL,
    dequeue_block BIGINT NOT NULL,
    CONSTRAINT builder_deposit_request_txs_pkey PRIMARY KEY (block_root, block_index)
);

CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_block_number_idx"
    ON public."builder_deposit_request_txs" ("block_number" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_public_key_idx"
    ON public."builder_deposit_request_txs" ("public_key" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_builder_index_idx"
    ON public."builder_deposit_request_txs" ("builder_index" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_amount_idx"
    ON public."builder_deposit_request_txs" ("amount" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_tx_hash_idx"
    ON public."builder_deposit_request_txs" ("tx_hash" ASC);
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_fork_idx"
    ON public."builder_deposit_request_txs" ("fork_id" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_dequeue_block_idx"
    ON public."builder_deposit_request_txs" ("dequeue_block" ASC NULLS FIRST);

-- builder exit requests (CL view, attributed to the processing slot)
CREATE TABLE IF NOT EXISTS public."builder_exits" (
    slot_number BIGINT NOT NULL,
    slot_root bytea NOT NULL,
    slot_index INT NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address bytea NOT NULL,
    public_key bytea NOT NULL,
    builder_index BIGINT NULL,
    tx_hash bytea NULL,
    block_number BIGINT NOT NULL DEFAULT 0,
    result smallint NOT NULL DEFAULT 0,
    CONSTRAINT builder_exits_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "builder_exits_slot_number_idx"
    ON public."builder_exits" ("slot_number" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exits_public_key_idx"
    ON public."builder_exits" ("public_key" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exits_source_addr_idx"
    ON public."builder_exits" ("source_address" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exits_builder_index_idx"
    ON public."builder_exits" ("builder_index" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exits_block_number_idx"
    ON public."builder_exits" ("block_number" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exits_fork_idx"
    ON public."builder_exits" ("fork_id" ASC NULLS FIRST);

-- builder exit requests (EL view, from the builder exit system contract)
CREATE TABLE IF NOT EXISTS public."builder_exit_request_txs" (
    block_number BIGINT NOT NULL,
    block_index INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root bytea NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address bytea NOT NULL,
    public_key bytea NOT NULL,
    builder_index BIGINT NULL,
    tx_hash bytea NULL,
    tx_sender bytea NOT NULL,
    tx_target bytea NOT NULL,
    dequeue_block BIGINT NOT NULL,
    CONSTRAINT builder_exit_request_txs_pkey PRIMARY KEY (block_root, block_index)
);

CREATE INDEX IF NOT EXISTS "builder_exit_request_txs_block_number_idx"
    ON public."builder_exit_request_txs" ("block_number" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exit_request_txs_public_key_idx"
    ON public."builder_exit_request_txs" ("public_key" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exit_request_txs_source_addr_idx"
    ON public."builder_exit_request_txs" ("source_address" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exit_request_txs_builder_index_idx"
    ON public."builder_exit_request_txs" ("builder_index" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exit_request_txs_tx_hash_idx"
    ON public."builder_exit_request_txs" ("tx_hash" ASC);
CREATE INDEX IF NOT EXISTS "builder_exit_request_txs_fork_idx"
    ON public."builder_exit_request_txs" ("fork_id" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_exit_request_txs_dequeue_block_idx"
    ON public."builder_exit_request_txs" ("dequeue_block" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
