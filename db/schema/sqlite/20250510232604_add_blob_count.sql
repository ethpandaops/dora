-- +goose Up
-- +goose StatementBegin

ALTER TABLE "slots"
ADD "blob_count" integer NOT NULL DEFAULT 0;

ALTER TABLE "slots"
ADD "eth_gas_used" integer NOT NULL DEFAULT 0;

ALTER TABLE "slots"
ADD "eth_gas_limit" integer NOT NULL DEFAULT 0;

ALTER TABLE "slots"
ADD "eth_base_fee" integer NOT NULL DEFAULT 0;

ALTER TABLE "slots"
ADD "eth_fee_recipient" blob;

ALTER TABLE "epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0;

ALTER TABLE "epochs"
ADD "eth_gas_used" integer NOT NULL DEFAULT 0;

ALTER TABLE "epochs"
ADD "eth_gas_limit" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_epochs"
ADD "eth_gas_used" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_epochs"
ADD "eth_gas_limit" integer NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd 