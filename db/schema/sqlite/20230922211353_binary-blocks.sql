-- +goose Up
-- +goose StatementBegin

DROP TABLE IF EXISTS "orphaned_blocks";

CREATE TABLE IF NOT EXISTS "orphaned_blocks"
(
    "root" BLOB NOT NULL,
    "header_ver" int NOT NULL,
    "header_ssz" BLOB NOT NULL,
    "block_ver" int NOT NULL,
    "block_ssz" BLOB NOT NULL,
    CONSTRAINT "orphaned_blocks_pkey" PRIMARY KEY ("root")
);

DROP TABLE IF EXISTS "unfinalized_blocks";

CREATE TABLE IF NOT EXISTS "unfinalized_blocks"
(
    "root" BLOB NOT NULL,
    "slot" bigint NOT NULL,
    "header_ver" int NOT NULL,
    "header_ssz" BLOB NOT NULL,
    "block_ver" int NOT NULL,
    "block_ssz" BLOB NOT NULL,
    CONSTRAINT "unfinalized_blocks_pkey" PRIMARY KEY ("root")
);

CREATE INDEX IF NOT EXISTS "unfinalized_blocks_slot_idx"
    ON "unfinalized_blocks" 
    ("slot" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
