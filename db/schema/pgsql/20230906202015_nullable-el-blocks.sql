-- +goose Up
-- +goose StatementBegin

ALTER TABLE IF EXISTS public."blocks"
    ALTER COLUMN "eth_block_number" DROP DEFAULT;

ALTER TABLE IF EXISTS public."blocks"
    ALTER COLUMN "eth_block_number" DROP NOT NULL;

ALTER TABLE IF EXISTS public."blocks"
    ALTER COLUMN "eth_block_hash" DROP NOT NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
