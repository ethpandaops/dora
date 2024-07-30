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

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
