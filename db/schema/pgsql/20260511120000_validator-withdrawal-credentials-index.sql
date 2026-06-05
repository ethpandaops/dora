-- +goose Up
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS "validators_withdrawal_credentials_idx"
    ON public."validators" ("withdrawal_credentials");

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
