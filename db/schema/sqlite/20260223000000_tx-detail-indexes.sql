-- +goose Up
-- +goose StatementBegin

-- Add tx_hash index on el_event_index for transaction detail page queries.
CREATE INDEX IF NOT EXISTS "el_event_index_tx_hash_idx"
    ON "el_event_index"
    ("tx_hash" ASC);

-- Add tx_hash index on el_transactions_internal for transaction detail page queries.
CREATE INDEX IF NOT EXISTS "el_internal_tx_hash_idx"
    ON "el_transactions_internal"
    ("tx_hash" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
