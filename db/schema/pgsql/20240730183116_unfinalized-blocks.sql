-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."unfinalized_blocks"
ADD "status" integer NOT NULL DEFAULT 0;

ALTER TABLE public."unfinalized_blocks"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

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

CREATE TABLE IF NOT EXISTS public."unfinalized_duties"
(
    "epoch" bigint NOT NULL,
    "dependent_root" bytea NOT NULL,
    "duties" bytea NOT NULL,
    CONSTRAINT "unfinalized_duties_pkey" PRIMARY KEY ("epoch", "dependent_root")
);

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

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
