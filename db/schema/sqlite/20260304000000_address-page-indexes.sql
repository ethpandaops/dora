-- +goose Up
-- +goose StatementBegin

-- Composite indexes for token transfer count queries filtered by token_type.
CREATE INDEX IF NOT EXISTS "el_token_transfers_from_type_idx"
    ON "el_token_transfers"
    ("from_id" ASC, "token_type" ASC);

CREATE INDEX IF NOT EXISTS "el_token_transfers_to_type_idx"
    ON "el_token_transfers"
    ("to_id" ASC, "token_type" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
