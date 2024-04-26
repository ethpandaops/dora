-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public.slots
(
    slot bigint NOT NULL,
    proposer bigint NOT NULL,
    status smallint NOT NULL,
    root bytea NOT NULL,
    parent_root bytea NULL,
    state_root bytea NULL,
    graffiti bytea NULL,
    graffiti_text text NULL COLLATE pg_catalog."default",
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
    eth_block_hash bytea NULL,
    eth_block_extra text NULL COLLATE pg_catalog."default",
    sync_participation real NULL DEFAULT 0,
    CONSTRAINT slots_pkey PRIMARY KEY (slot, root)
);

CREATE INDEX IF NOT EXISTS "slots_root_idx"
    ON public."slots"
    ("root" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "slots_graffiti_idx"
    ON public."slots" USING gin 
    ("graffiti_text" gin_trgm_ops);

CREATE INDEX IF NOT EXISTS "slots_eth_block_extra_idx"
    ON public."slots" USING gin 
    ("eth_block_extra" gin_trgm_ops);

CREATE INDEX IF NOT EXISTS "slots_slot_idx"
    ON public."slots" 
    ("slot" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "slots_state_root_idx"
    ON public."slots" 
    ("state_root" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "slots_eth_block_number_idx"
    ON public."slots" 
    ("eth_block_number" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "slots_eth_block_hash_idx"
    ON public."slots" 
    ("eth_block_hash" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "slots_proposer_idx"
    ON public."slots" 
    ("proposer" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "slots_status_idx"
    ON public."slots" 
    ("status" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "slots_parent_root_idx"
    ON public."slots" 
    ("parent_root" ASC NULLS LAST);

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


-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
