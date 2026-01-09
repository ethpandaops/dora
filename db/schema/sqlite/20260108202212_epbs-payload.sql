-- +goose Up
-- +goose StatementBegin

ALTER TABLE "unfinalized_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "unfinalized_blocks" ADD "payload_ssz" BLOB NULL;

ALTER TABLE "orphaned_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "orphaned_blocks" ADD "payload_ssz" BLOB NULL;

ALTER TABLE "slots" ADD "payload_status" smallint NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS "slots_payload_status_idx" ON "slots" ("payload_status" ASC);

ALTER TABLE "epochs" ADD "payload_count" int NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_epochs" ADD "payload_count" int NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS "block_bids" (
    "parent_root" BLOB NOT NULL,
    "parent_hash" BLOB NOT NULL,
    "block_hash" BLOB NOT NULL,
    "fee_recipient" BLOB NOT NULL,
    "gas_limit" BIGINT NOT NULL,
    "builder_index" BIGINT NOT NULL,
    "slot" BIGINT NOT NULL,
    "value" BIGINT NOT NULL,
    "el_payment" BIGINT NOT NULL,
    CONSTRAINT block_bids_pkey PRIMARY KEY (parent_root, parent_hash, block_hash, builder_index)
);

CREATE INDEX IF NOT EXISTS "block_bids_parent_root_idx" ON "block_bids" ("parent_root" ASC);

CREATE INDEX IF NOT EXISTS "block_bids_builder_index_idx" ON "block_bids" ("builder_index" ASC);

CREATE INDEX IF NOT EXISTS "block_bids_slot_idx" ON "block_bids" ("slot" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd