-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "unfinalized_blocks"
(
    "root" BLOB NOT NULL,
    "slot" bigint NOT NULL,
    "header" text NOT NULL,
    "block" text NOT NULL,
    CONSTRAINT "unfinalized_blocks_pkey" PRIMARY KEY ("root")
);

CREATE INDEX IF NOT EXISTS "unfinalized_blocks_slot_idx"
    ON "unfinalized_blocks" 
    ("slot" ASC);

CREATE TABLE IF NOT EXISTS "unfinalized_duties"
(
    "epoch" bigint NOT NULL,
    "dependent_root" BLOB NOT NULL,
    "duties" BLOB NOT NULL,
    CONSTRAINT "unfinalized_duties_pkey" PRIMARY KEY ("epoch", "dependent_root")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
