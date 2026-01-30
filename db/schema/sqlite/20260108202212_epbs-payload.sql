-- +goose Up
-- +goose StatementBegin

ALTER TABLE "unfinalized_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "unfinalized_blocks" ADD "payload_ssz" BLOB NULL;

ALTER TABLE "orphaned_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "orphaned_blocks" ADD "payload_ssz" BLOB NULL;

ALTER TABLE "slots" ADD "payload_status" smallint NOT NULL DEFAULT 0;
ALTER TABLE "slots" ADD "builder_index" BIGINT NOT NULL DEFAULT -1;

CREATE INDEX IF NOT EXISTS "slots_payload_status_idx" ON "slots" ("payload_status" ASC);
CREATE INDEX IF NOT EXISTS "slots_builder_index_idx" ON "slots" ("builder_index" ASC);

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

CREATE TABLE IF NOT EXISTS "builders" (
    "pubkey" BLOB NOT NULL,
    "builder_index" BIGINT NOT NULL,
    "version" SMALLINT NOT NULL,
    "execution_address" BLOB NOT NULL,
    "deposit_epoch" BIGINT NOT NULL,
    "withdrawable_epoch" BIGINT NOT NULL,
    "superseded" BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (pubkey)
);

CREATE INDEX IF NOT EXISTS "builders_builder_index_idx" ON "builders" ("builder_index" ASC);

CREATE INDEX IF NOT EXISTS "builders_execution_address_idx" ON "builders" ("execution_address" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd