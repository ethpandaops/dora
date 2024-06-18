-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS slots
(
    slot bigint NOT NULL,
    proposer bigint NOT NULL,
    status smallint NOT NULL,
    root BLOB NOT NULL,
    parent_root BLOB NULL,
    state_root BLOB NULL,
    graffiti BLOB NULL,
    graffiti_text text NULL,
    attestation_count integer NULL DEFAULT 0,
    deposit_count integer NULL DEFAULT 0,
    exit_count integer NULL DEFAULT 0,
    withdraw_count integer NULL DEFAULT 0,
    withdraw_amount bigint NULL DEFAULT 0,
    attester_slashing_count integer NULL DEFAULT 0,
    proposer_slashing_count integer NULL DEFAULT 0,
    bls_change_count integer NULL DEFAULT 0,
    eth_transaction_count integer NULL DEFAULT 0,
    eth_block_number bigint NULL,
    eth_block_hash BLOB NULL,
    eth_block_extra BLOB NULL,
    eth_block_extra_text text NULL,
    sync_participation real NULL DEFAULT 0,
    CONSTRAINT slots_pkey PRIMARY KEY (slot, root)
);

CREATE INDEX IF NOT EXISTS "slots_root_idx"
    ON "slots"
    ("root" ASC);

CREATE INDEX IF NOT EXISTS "slots_graffiti_idx"
    ON "slots"  
    ("graffiti_text" ASC);

CREATE INDEX IF NOT EXISTS "slots_eth_block_extra_idx"
    ON "slots"  
    ("eth_block_extra_text" ASC);

CREATE INDEX IF NOT EXISTS "slots_slot_idx"
    ON "slots" 
    ("slot" ASC);

CREATE INDEX IF NOT EXISTS "slots_state_root_idx"
    ON "slots" 
    ("state_root" ASC );

CREATE INDEX IF NOT EXISTS "slots_eth_block_number_idx"
    ON "slots" 
    ("eth_block_number" ASC );

CREATE INDEX IF NOT EXISTS "slots_eth_block_hash_idx"
    ON "slots" 
    ("eth_block_hash" ASC );

CREATE INDEX IF NOT EXISTS "slots_proposer_idx"
    ON "slots" 
    ("proposer" ASC );

CREATE INDEX IF NOT EXISTS "slots_status_idx"
    ON "slots" 
    ("status" ASC );

CREATE INDEX IF NOT EXISTS "slots_parent_root_idx"
    ON "slots" 
    ("parent_root" ASC );

-- migrate blocks

INSERT INTO "slots" (
    slot, proposer, status, root, parent_root, state_root, graffiti, graffiti_text,
    attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
    proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
)
SELECT 
    slot, proposer, orphaned+1, root, parent_root, state_root, graffiti, graffiti_text,
    attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
    proposer_slashing_count, bls_change_count, eth_transaction_count, eth_block_number, eth_block_hash, sync_participation
FROM "blocks";

-- migrate missing blocks

INSERT INTO "slots" (
    slot, proposer, status, root
)
SELECT slot, proposer, 0, '0x'
FROM (
    SELECT slot, proposer, COALESCE((
        SELECT 
            CASE 
            WHEN blocks.orphaned = 1 THEN 2 
            WHEN blocks.orphaned = 0 THEN 1 
            ELSE 0 END
        FROM blocks
        WHERE blocks.slot = slot_assignments.slot 
        AND blocks.proposer = slot_assignments.proposer
        LIMIT 1
    ), 0) AS block_status
    FROM "slot_assignments"
) AS t1
WHERE t1.block_status = 0;

-- drop blocks

DROP TABLE IF EXISTS "blocks";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
