-- +goose Up
-- +goose StatementBegin

DROP TABLE IF EXISTS public."orphaned_blocks";

CREATE TABLE IF NOT EXISTS public."orphaned_blocks"
(
    "root" bytea NOT NULL,
    "header_ver" int NOT NULL,
    "header_ssz" bytea NOT NULL,
    "block_ver" int NOT NULL,
    "block_ssz" bytea NOT NULL,
    CONSTRAINT "orphaned_blocks_pkey" PRIMARY KEY ("root")
);

DROP TABLE IF EXISTS public."unfinalized_blocks";

CREATE TABLE IF NOT EXISTS public."unfinalized_blocks"
(
    "root" bytea NOT NULL,
    "slot" bigint NOT NULL,
    "header_ver" int NOT NULL,
    "header_ssz" bytea NOT NULL,
    "block_ver" int NOT NULL,
    "block_ssz" bytea NOT NULL,
    CONSTRAINT "unfinalized_blocks_pkey" PRIMARY KEY ("root")
);

CREATE INDEX IF NOT EXISTS "unfinalized_blocks_slot_idx"
    ON public."unfinalized_blocks" 
    ("slot" ASC NULLS LAST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
