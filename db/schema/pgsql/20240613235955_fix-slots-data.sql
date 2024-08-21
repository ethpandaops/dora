-- +goose Up
-- +goose StatementBegin

UPDATE slots
SET eth_block_extra = '', eth_block_extra_text = ''
WHERE state_root IS NOT NULL AND eth_block_extra IS NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
