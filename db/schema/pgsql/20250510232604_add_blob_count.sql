-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."slots"
ADD "blob_count" integer NOT NULL DEFAULT 0,
ADD "eth_gas_used" bigint NOT NULL DEFAULT 0,
ADD "eth_gas_limit" bigint NOT NULL DEFAULT 0,
ADD "eth_base_fee" bigint NOT NULL DEFAULT 0,
ADD "eth_fee_recipient" bytea;

ALTER TABLE public."epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0,
ADD "eth_gas_used" bigint NOT NULL DEFAULT 0,
ADD "eth_gas_limit" bigint NOT NULL DEFAULT 0;

ALTER TABLE public."unfinalized_epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0,
ADD "eth_gas_used" bigint NOT NULL DEFAULT 0,
ADD "eth_gas_limit" bigint NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd 