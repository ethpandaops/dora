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

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
