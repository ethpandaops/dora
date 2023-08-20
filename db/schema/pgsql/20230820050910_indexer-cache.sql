-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."unfinalized_blocks"
(
    "root" bytea NOT NULL,
    "slot" bigint NOT NULL,
    "header" text COLLATE pg_catalog."default" NOT NULL,
    "block" text COLLATE pg_catalog."default" NOT NULL,
    CONSTRAINT "unfinalized_blocks_pkey" PRIMARY KEY ("root")
);

CREATE INDEX IF NOT EXISTS "unfinalized_blocks_slot_idx"
    ON public."unfinalized_blocks" 
    ("slot" ASC NULLS LAST);

CREATE TABLE IF NOT EXISTS public."unfinalized_duties"
(
    "epoch" bigint NOT NULL,
    "dependent_root" bytea NOT NULL,
    "duties" bytea NOT NULL,
    CONSTRAINT "unfinalized_duties_pkey" PRIMARY KEY ("epoch", "dependent_root")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
