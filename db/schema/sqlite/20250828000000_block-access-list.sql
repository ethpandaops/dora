-- +goose Up
-- +goose StatementBegin

ALTER TABLE "slots" 
    ADD "eth_block_access_list_hash" BLOB NULL;

CREATE INDEX IF NOT EXISTS "slots_eth_block_access_list_hash_idx"
    ON "slots" 
    ("eth_block_access_list_hash" ASC );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS "slots_eth_block_access_list_hash_idx";

-- SQLite doesn't support DROP COLUMN directly, need to recreate table
-- This is handled by goose migration tool

-- +goose StatementEnd