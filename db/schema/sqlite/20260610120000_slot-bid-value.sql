-- +goose Up
-- +goose StatementBegin

ALTER TABLE "slots" ADD "eth_bid_value" BIGINT NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
