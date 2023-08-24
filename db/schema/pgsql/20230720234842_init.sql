-- +goose Up
-- +goose StatementBegin

/* trigram extension for faster text-search */
CREATE EXTENSION IF NOT EXISTS pg_trgm;


CREATE TABLE IF NOT EXISTS public."explorer_state"
(
    "key" character varying(150) COLLATE pg_catalog."default" NOT NULL,
    "value" text COLLATE pg_catalog."default",
    CONSTRAINT "explorer_state_pkey" PRIMARY KEY ("key")
);

CREATE TABLE IF NOT EXISTS public."blocks"
(
    "root" bytea NOT NULL,
    "slot" bigint NOT NULL,
    "parent_root" bytea NOT NULL,
    "state_root" bytea NOT NULL,
    "orphaned" boolean NOT NULL,
    "proposer" bigint NOT NULL,
    "graffiti" bytea NOT NULL,
    "graffiti_text" TEXT NULL,
    "attestation_count" integer NOT NULL DEFAULT 0,
    "deposit_count" integer NOT NULL DEFAULT 0,
    "exit_count" integer NOT NULL DEFAULT 0,
    "withdraw_count" integer NOT NULL DEFAULT 0,
    "withdraw_amount" bigint NOT NULL DEFAULT 0,
    "attester_slashing_count" integer NOT NULL DEFAULT 0,
    "proposer_slashing_count" integer NOT NULL DEFAULT 0,
    "bls_change_count" integer NOT NULL DEFAULT 0,
    "eth_transaction_count" integer NOT NULL DEFAULT 0,
    "eth_block_number" bigint NOT NULL DEFAULT 0,
    "eth_block_hash" bytea NOT NULL,
    "sync_participation" real NOT NULL DEFAULT 0,
    CONSTRAINT "blocks_pkey" PRIMARY KEY ("root")
);

CREATE INDEX IF NOT EXISTS "blocks_graffiti_idx"
    ON public."blocks" USING gin 
    ("graffiti_text" gin_trgm_ops);

CREATE INDEX IF NOT EXISTS "blocks_slot_idx"
    ON public."blocks" 
    ("slot" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "blocks_state_root_idx"
    ON public."blocks" 
    ("state_root" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "blocks_eth_block_number_idx"
    ON public."blocks" 
    ("eth_block_number" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "blocks_eth_block_hash_idx"
    ON public."blocks" 
    ("eth_block_hash" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "blocks_proposer_idx"
    ON public."blocks" 
    ("proposer" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "blocks_parent_root_idx"
    ON public."blocks" 
    ("parent_root" ASC NULLS LAST);

CREATE TABLE IF NOT EXISTS public."orphaned_blocks"
(
    "root" bytea NOT NULL,
    "header" text COLLATE pg_catalog."default" NOT NULL,
    "block" text COLLATE pg_catalog."default" NOT NULL,
    CONSTRAINT "orphaned_blocks_pkey" PRIMARY KEY ("root")
);

CREATE TABLE IF NOT EXISTS public."slot_assignments"
(
    "slot" bigint NOT NULL,
    "proposer" bigint NOT NULL,
    CONSTRAINT "slot_assignments_pkey" PRIMARY KEY ("slot")
);

CREATE INDEX IF NOT EXISTS "slot_assignments_proposer_idx"
    ON public."slot_assignments" 
    ("proposer" ASC NULLS LAST);

CREATE TABLE IF NOT EXISTS public."epochs"
(
    "epoch" bigint NOT NULL,
    "validator_count" bigint NOT NULL DEFAULT 0,
    "validator_balance" bigint NOT NULL DEFAULT 0,
    "eligible" bigint NOT NULL DEFAULT 0,
    "voted_target" bigint NOT NULL DEFAULT 0,
    "voted_head" bigint NOT NULL DEFAULT 0,
    "voted_total" bigint NOT NULL DEFAULT 0,
    "block_count" smallint NOT NULL DEFAULT 0,
    "orphaned_count" smallint NOT NULL DEFAULT 0,
    "attestation_count" integer NOT NULL DEFAULT 0,
    "deposit_count" integer NOT NULL DEFAULT 0,
    "exit_count" integer NOT NULL DEFAULT 0,
    "withdraw_count" integer NOT NULL DEFAULT 0,
    "withdraw_amount" bigint NOT NULL DEFAULT 0,
    "attester_slashing_count" integer NOT NULL DEFAULT 0,
    "proposer_slashing_count" integer NOT NULL DEFAULT 0,
    "bls_change_count" integer NOT NULL DEFAULT 0,
    "eth_transaction_count" integer NOT NULL DEFAULT 0,
    "sync_participation" real NOT NULL DEFAULT 0,
    CONSTRAINT "epochs_pkey" PRIMARY KEY ("epoch")
);



-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
