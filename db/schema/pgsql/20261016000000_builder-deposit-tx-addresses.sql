-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_tx_sender_idx"
    ON public."builder_deposit_request_txs" ("tx_sender" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "builder_deposit_request_txs_tx_target_idx"
    ON public."builder_deposit_request_txs" ("tx_target" ASC NULLS FIRST);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS "builder_deposit_request_txs_tx_sender_idx";
DROP INDEX IF EXISTS "builder_deposit_request_txs_tx_target_idx";
-- +goose StatementEnd
