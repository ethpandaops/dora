-- +goose Up
-- +goose StatementBegin

-- Add tx_hash index on el_event_index for transaction detail page queries.
-- Without this, GetElEventIndicesByTxHash does a full sequential scan.
CREATE INDEX IF NOT EXISTS "el_event_index_tx_hash_idx"
    ON public."el_event_index"
    ("tx_hash" ASC NULLS FIRST);

-- Add tx_hash index on el_transactions_internal for transaction detail page queries.
-- Without this, GetElTransactionsInternalByTxHash does a full sequential scan.
CREATE INDEX IF NOT EXISTS "el_internal_tx_hash_idx"
    ON public."el_transactions_internal"
    ("tx_hash" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
