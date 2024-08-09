-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."unfinalized_blocks"
ADD "status" integer NOT NULL DEFAULT 0;

ALTER TABLE public."unfinalized_blocks"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

ALTER TABLE public."deposit_txs"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

-- fix version (remove 0x30000000 bits from header_ver & block_ver)
-- 0x30000000 = 0b00110000000000000000000000000000 = 805306368
-- 0x4fffffff = 0b01001111111111111111111111111111 = 1342177279
UPDATE "unfinalized_blocks" SET "header_ver" = "header_ver" & 1342177279, "block_ver" = "block_ver" & 1342177279;
UPDATE "orphaned_blocks" SET "header_ver" = "header_ver" & 1342177279, "block_ver" = "block_ver" & 1342177279;

CREATE TABLE IF NOT EXISTS public."forks"
(
    "fork_id" bigint NOT NULL,
    "base_slot" bigint NOT NULL,
    "base_root" bytea NOT NULL,
    "leaf_slot" bigint NOT NULL,
    "leaf_root" bytea NOT NULL,
    "parent_fork" bigint NOT NULL,
    CONSTRAINT "forks_pkey" PRIMARY KEY ("fork_id")
);

CREATE INDEX IF NOT EXISTS "forks_base_slot_idx"
    ON public."forks" 
    ("base_slot" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "forks_base_root_idx"
    ON public."forks" 
    ("base_root" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "forks_leaf_root_idx"
    ON public."forks" 
    ("leaf_root" ASC NULLS LAST);

CREATE TABLE IF NOT EXISTS public."unfinalized_duties"
(
    "epoch" bigint NOT NULL,
    "dependent_root" bytea NOT NULL,
    "duties" bytea NOT NULL,
    CONSTRAINT "unfinalized_duties_pkey" PRIMARY KEY ("epoch", "dependent_root")
);

-- recreate unfinalized_epochs (no migration needed)
DROP TABLE IF EXISTS public."unfinalized_epochs";

CREATE TABLE IF NOT EXISTS public."unfinalized_epochs"
(
    "epoch" bigint NOT NULL,
    "dependent_root" bytea NOT NULL,
    "epoch_head_root" bytea NOT NULL,
    "epoch_head_fork_id" bigint NOT NULL,
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
    CONSTRAINT "unfinalized_epochs_pkey" PRIMARY KEY ("epoch", "dependent_root", "epoch_head_root")
);

CREATE INDEX IF NOT EXISTS "unfinalized_epochs_epoch_idx"
    ON public."unfinalized_epochs" 
    ("epoch" ASC NULLS LAST);

-- add fork_id to slots
ALTER TABLE public."slots"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

UPDATE "slots" SET "fork_id" = 1 WHERE "status" = 2;

CREATE INDEX IF NOT EXISTS "slots_fork_id_idx"
    ON public."slots" 
    ("fork_id" ASC NULLS LAST);

-- add fork_id to deposits
ALTER TABLE public."deposits"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

UPDATE "deposits" SET "fork_id" = 1 WHERE "orphaned" = TRUE;

CREATE INDEX IF NOT EXISTS "deposits_fork_id_idx"
    ON public."deposits" 
    ("fork_id" ASC NULLS LAST);

-- add fork_id to voluntary_exits
ALTER TABLE public."voluntary_exits"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

UPDATE "voluntary_exits" SET "fork_id" = 1 WHERE "orphaned" = TRUE;

CREATE INDEX IF NOT EXISTS "voluntary_exits_fork_id_idx"
    ON public."voluntary_exits" 
    ("fork_id" ASC NULLS LAST);

-- add fork_id to slashings
ALTER TABLE public."slashings"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

UPDATE "slashings" SET "fork_id" = 1 WHERE "orphaned" = TRUE;

CREATE INDEX IF NOT EXISTS "slashings_fork_id_idx"
    ON public."slashings" 
    ("fork_id" ASC NULLS LAST);

-- drop slot assignments
DROP TABLE IF EXISTS public."slot_assignments";

-- drop blob index
DROP TABLE IF EXISTS public."blob_assignments";
DROP TABLE IF EXISTS public."blobs";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
