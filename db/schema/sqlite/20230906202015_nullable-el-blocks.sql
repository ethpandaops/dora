-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "new_blocks"
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
    "eth_block_number" BIGINT NULL,
    "eth_block_hash" BLOB NULL,
    "sync_participation" REAL NOT NULL DEFAULT 0,
    CONSTRAINT "blocks_pkey" PRIMARY KEY ("root")
);

INSERT INTO "new_blocks" (
    root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
    attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
    proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
)
SELECT 
    root, slot, parent_root, state_root, orphaned, proposer, graffiti, graffiti_text,
    attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
    proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
FROM "blocks";


DROP TABLE "blocks";
ALTER TABLE "new_blocks" RENAME TO "blocks"; 


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

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
