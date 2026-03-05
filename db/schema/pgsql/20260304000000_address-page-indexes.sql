-- +goose Up
-- +goose StatementBegin

-- Composite indexes for token transfer count queries filtered by token_type.
-- Without these, count queries with token_type IN (...) filter fall back to
-- bitmap heap scans or sequential scans on large accounts (22s+ for 38k transfers).
CREATE INDEX IF NOT EXISTS "el_token_transfers_from_type_idx"
    ON public."el_token_transfers"
    ("from_id" ASC NULLS FIRST, "token_type" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_token_transfers_to_type_idx"
    ON public."el_token_transfers"
    ("to_id" ASC NULLS FIRST, "token_type" ASC NULLS FIRST);

-- Single-column indexes for internal transaction count and EXISTS queries.
-- Smaller than the composite (from_id, block_uid) indexes, enabling faster
-- index-only scans with fewer heap fetches for count-only operations.
CREATE INDEX IF NOT EXISTS "el_internal_tx_from_only_idx"
    ON public."el_transactions_internal"
    ("from_id" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_internal_tx_to_only_idx"
    ON public."el_transactions_internal"
    ("to_id" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
