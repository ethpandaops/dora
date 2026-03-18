-- +goose Up
-- +goose StatementBegin

-- Composite indexes for token transfer count queries filtered by token_type.
CREATE INDEX IF NOT EXISTS "el_token_transfers_from_type_idx"
    ON "el_token_transfers"
    ("from_id" ASC, "token_type" ASC);

CREATE INDEX IF NOT EXISTS "el_token_transfers_to_type_idx"
    ON "el_token_transfers"
    ("to_id" ASC, "token_type" ASC);

-- Single-column indexes for internal transaction count and EXISTS queries.
CREATE INDEX IF NOT EXISTS "el_internal_tx_from_only_idx"
    ON "el_transactions_internal"
    ("from_id" ASC);

CREATE INDEX IF NOT EXISTS "el_internal_tx_to_only_idx"
    ON "el_transactions_internal"
    ("to_id" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
