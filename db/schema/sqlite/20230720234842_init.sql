-- +goose Up
-- +goose StatementBegin


CREATE TABLE IF NOT EXISTS "explorer_state"
(
    "key" TEXT NOT NULL UNIQUE,
    "value" TEXT,
    CONSTRAINT "explorer_state_pkey" PRIMARY KEY("key")
);

CREATE TABLE IF NOT EXISTS "blocks"
(
    "root" BLOB NOT NULL UNIQUE,
    "slot" BIGINT NOT NULL,
    "parent_root" BLOB NOT NULL,
    "state_root" BLOB NOT NULL,
    "orphaned" INTEGER NOT NULL,
    "proposer" BIGINT NOT NULL,
    "graffiti" BLOB NOT NULL,
    "graffiti_text" TEXT NULL,
    "attestation_count" INTEGER NOT NULL DEFAULT 0,
    "deposit_count" INTEGER NOT NULL DEFAULT 0,
    "exit_count" INTEGER NOT NULL DEFAULT 0,
    "withdraw_count" INTEGER NOT NULL DEFAULT 0,
    "withdraw_amount" BIGINT NOT NULL DEFAULT 0,
    "attester_slashing_count" INTEGER NOT NULL DEFAULT 0,
    "proposer_slashing_count" INTEGER NOT NULL DEFAULT 0,
    "bls_change_count" INTEGER NOT NULL DEFAULT 0,
    "eth_transaction_count" INTEGER NOT NULL DEFAULT 0,
    "eth_block_number" BIGINT NOT NULL DEFAULT 0,
    "eth_block_hash" BLOB NOT NULL,
    "sync_participation" REAL NOT NULL DEFAULT 0,
    CONSTRAINT "blocks_pkey" PRIMARY KEY ("root")
);

CREATE INDEX IF NOT EXISTS "blocks_graffiti_idx"
    ON "blocks"
    ("graffiti_text" ASC);

CREATE INDEX IF NOT EXISTS "blocks_slot_idx"
    ON "blocks" 
    ("slot" ASC);

CREATE INDEX IF NOT EXISTS "blocks_state_root_idx"
    ON "blocks" 
    ("state_root" ASC);

CREATE INDEX IF NOT EXISTS "blocks_eth_block_number_idx"
    ON "blocks" 
    ("eth_block_number" ASC);

CREATE INDEX IF NOT EXISTS "blocks_eth_block_hash_idx"
    ON "blocks" 
    ("eth_block_hash" ASC);

CREATE INDEX IF NOT EXISTS "blocks_proposer_idx"
    ON "blocks" 
    ("proposer" ASC);

CREATE INDEX IF NOT EXISTS "blocks_parent_root_idx"
    ON "blocks" 
    ("parent_root" ASC);

CREATE TABLE IF NOT EXISTS "orphaned_blocks"
(
    "root" BLOB NOT NULL UNIQUE,
    "header" TEXT NOT NULL,
    "block" TEXT NOT NULL,
    CONSTRAINT "orphaned_blocks_pkey" PRIMARY KEY ("root")
);

CREATE TABLE IF NOT EXISTS "slot_assignments"
(
    "slot" BIGINT NOT NULL,
    "proposer" BIGINT NOT NULL,
    CONSTRAINT "slot_assignments_pkey" PRIMARY KEY ("slot")
);

CREATE INDEX IF NOT EXISTS "slot_assignments_proposer_idx"
    ON "slot_assignments" 
    ("proposer" ASC);

CREATE TABLE IF NOT EXISTS "epochs"
(
    "epoch" BIGINT NOT NULL,
    "validator_count" BIGINT NOT NULL DEFAULT 0,
    "validator_balance" BIGINT NOT NULL DEFAULT 0,
    "eligible" BIGINT NOT NULL DEFAULT 0,
    "voted_target" BIGINT NOT NULL DEFAULT 0,
    "voted_head" BIGINT NOT NULL DEFAULT 0,
    "voted_total" BIGINT NOT NULL DEFAULT 0,
    "block_count" INTEGER NOT NULL DEFAULT 0,
    "orphaned_count" INTEGER NOT NULL DEFAULT 0,
    "attestation_count" INTEGER NOT NULL DEFAULT 0,
    "deposit_count" INTEGER NOT NULL DEFAULT 0,
    "exit_count" INTEGER NOT NULL DEFAULT 0,
    "withdraw_count" INTEGER NOT NULL DEFAULT 0,
    "withdraw_amount" BIGINT NOT NULL DEFAULT 0,
    "attester_slashing_count" INTEGER NOT NULL DEFAULT 0,
    "proposer_slashing_count" INTEGER NOT NULL DEFAULT 0,
    "bls_change_count" INTEGER NOT NULL DEFAULT 0,
    "eth_transaction_count" INTEGER NOT NULL DEFAULT 0,
    "sync_participation" REAL NOT NULL DEFAULT 0,
    CONSTRAINT "epochs_pkey" PRIMARY KEY ("epoch")
);



-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
