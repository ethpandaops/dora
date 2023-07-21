-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."SyncState"
(
    "Id" integer NOT NULL,
    "HeadSlot" bigint,
    "HeadRoot" bytea,
    CONSTRAINT "SyncState_pkey" PRIMARY KEY ("Id")
);

CREATE TABLE IF NOT EXISTS public."Blocks"
(
    "Slot" bigint NOT NULL,
    "Root" bytea NOT NULL,
    "ParentRoot" bytea NOT NULL,
    "StateRoot" bytea NOT NULL,
    "Orphaned" boolean NOT NULL,
    "Proposer" bigint NOT NULL,
    "Graffiti" bytea NOT NULL,
    "AttestationCount" integer NOT NULL DEFAULT 0,
    "DepositCount" integer NOT NULL DEFAULT 0,
    "ExitCount" integer NOT NULL DEFAULT 0,
    "AttesterSlashingCount" integer NOT NULL DEFAULT 0,
    "ProposerSlashingCount" integer NOT NULL DEFAULT 0,
    "SyncParticipation" real NOT NULL DEFAULT 0,
    CONSTRAINT "Blocks_pkey" PRIMARY KEY ("Slot")
);

CREATE INDEX IF NOT EXISTS "GraffitiIndex"
    ON public."Blocks" 
    ("Graffiti" ASC NULLS LAST);

CREATE UNIQUE INDEX IF NOT EXISTS "RootIndex"
    ON public."Blocks" 
    ("Root" ASC NULLS LAST);

CREATE TABLE IF NOT EXISTS public."OrphanedBlocks"
(
    "Root" bytea NOT NULL,
    "Header" text COLLATE pg_catalog."default" NOT NULL,
    "Block" text COLLATE pg_catalog."default" NOT NULL,
    CONSTRAINT "OrphanedBlocks_pkey" PRIMARY KEY ("Root")
);

CREATE TABLE IF NOT EXISTS public."Epochs"
(
    "Epoch" bigint NOT NULL,
    "Finalized" boolean NOT NULL DEFAULT false,
    "Eligible" bigint NOT NULL DEFAULT 0,
    "Voted" bigint NOT NULL DEFAULT 0,
    "BlockCount" smallint NOT NULL DEFAULT 0,
    "AttestationCount" integer NOT NULL DEFAULT 0,
    "DepositCount" integer NOT NULL DEFAULT 0,
    "ExitCount" integer NOT NULL DEFAULT 0,
    "AttesterSlashingCount" integer NOT NULL DEFAULT 0,
    "ProposerSlashingCount" integer NOT NULL DEFAULT 0,
    "SyncParticipation" real NOT NULL DEFAULT 0,
    CONSTRAINT "Epochs_pkey" PRIMARY KEY ("Epoch")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
