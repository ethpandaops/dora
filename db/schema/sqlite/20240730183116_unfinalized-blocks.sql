-- +goose Up
-- +goose StatementBegin

ALTER TABLE "unfinalized_blocks"
ADD "status" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_blocks"
ADD "fork_id" BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS "forks"
(
    "fork_id" bigint NOT NULL,
    "base_slot" bigint NOT NULL,
    "base_root" BLOB NOT NULL,
    "leaf_slot" bigint NOT NULL,
    "leaf_root" BLOB NOT NULL,
    "parent_fork" bigint NOT NULL,
    CONSTRAINT "forks_pkey" PRIMARY KEY ("fork_id")
);

CREATE INDEX IF NOT EXISTS "forks_base_slot_idx"
    ON "forks" 
    ("base_slot" ASC);

CREATE INDEX IF NOT EXISTS "forks_base_root_idx"
    ON "forks" 
    ("base_root" ASC);


-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
