-- +goose Up
-- +goose StatementBegin

ALTER TABLE IF EXISTS public."slots" 
    ADD COLUMN IF NOT EXISTS "eth_block_access_list_hash" bytea NULL;

CREATE INDEX IF NOT EXISTS "slots_eth_block_access_list_hash_idx"
    ON public."slots" 
    ("eth_block_access_list_hash" ASC NULLS LAST);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS public."slots_eth_block_access_list_hash_idx";

ALTER TABLE IF EXISTS public."slots" 
    DROP COLUMN IF EXISTS "eth_block_access_list_hash";

-- +goose StatementEnd