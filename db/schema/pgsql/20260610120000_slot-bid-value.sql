-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."slots"
 ADD COLUMN "eth_bid_value" bigint NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
