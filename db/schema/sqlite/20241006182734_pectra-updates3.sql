-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "consolidation_request_txs" (
    block_number BIGINT NOT NULL,
    block_index INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root BLOB NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address BLOB NOT NULL,
    source_pubkey BLOB NULL,
    source_index BIGINT NULL,
    target_pubkey BLOB NULL,
    target_index BIGINT NULL,
    tx_hash BLOB NULL,
    tx_sender BLOB NOT NULL,
    tx_target BLOB NOT NULL,
    dequeue_block BIGINT NOT NULL,
    CONSTRAINT consolidation_pkey PRIMARY KEY (block_root, block_index)
);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_block_number_idx"
    ON "consolidation_request_txs"
    ("block_number" ASC);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_source_addr_idx"
    ON "consolidation_request_txs"
    ("source_address" ASC);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_source_idx"
    ON "consolidation_request_txs"
    ("source_index" ASC);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_target_idx"
    ON "consolidation_request_txs"
    ("target_index" ASC);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_fork_idx"
    ON "consolidation_request_txs"
    ("fork_id" ASC);

CREATE INDEX IF NOT EXISTS "consolidation_request_txs_dequeue_block_idx"
    ON "consolidation_request_txs"
    ("dequeue_block" ASC);

-- add block_number to consolidation_requests
ALTER TABLE "consolidation_requests"
    ADD "block_number" BIGINT NOT NULL DEFAULT 0;

UPDATE "consolidation_requests"
    SET "block_number" = (
        SELECT eth_block_number 
        FROM "slots" 
        WHERE "slots".root = "consolidation_requests".slot_root
    );

CREATE INDEX IF NOT EXISTS "consolidation_requests_block_number_idx"
    ON "consolidation_requests"
    ("block_number" ASC);

CREATE TABLE IF NOT EXISTS "withdrawal_request_txs" (
    block_number BIGINT NOT NULL,
    block_index INT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root BLOB NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address BLOB NOT NULL,
    validator_pubkey BLOB NOT NULL,
    validator_index BIGINT NULL,
    amount BIGINT NOT NULL,
    tx_hash BLOB NULL,
    tx_sender BLOB NOT NULL,
    tx_target BLOB NOT NULL,
    dequeue_block BIGINT NOT NULL,
    CONSTRAINT withdrawal_request_txs_pkey PRIMARY KEY (block_root, block_index)
);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_block_number_idx"
    ON "withdrawal_request_txs"
    ("block_number" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_source_addr_idx"
    ON "withdrawal_request_txs"
    ("source_address" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_validator_index_idx"
    ON "withdrawal_request_txs"
    ("validator_index" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_amount_idx"
    ON "withdrawal_request_txs"
    ("amount" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_fork_idx"
    ON "withdrawal_request_txs"
    ("fork_id" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_request_txs_dequeue_block_idx"
    ON "withdrawal_request_txs"
    ("dequeue_block" ASC);

-- add block_number to withdrawal_requests
ALTER TABLE "withdrawal_requests"
    ADD "block_number" BIGINT NOT NULL DEFAULT 0;

UPDATE "withdrawal_requests"
    SET "block_number" = (
        SELECT eth_block_number 
        FROM "slots" 
        WHERE "slots".root = "withdrawal_requests".slot_root
    );

CREATE INDEX IF NOT EXISTS "withdrawal_requests_block_number_idx"
    ON "withdrawal_requests"
    ("block_number" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
