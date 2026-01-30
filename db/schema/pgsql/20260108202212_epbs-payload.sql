-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."unfinalized_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

ALTER TABLE public."orphaned_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

ALTER TABLE public."slots" ADD
 "payload_status" smallint NOT NULL DEFAULT 0,
 "builder_index" bigint NOT NULL DEFAULT -1;

CREATE INDEX IF NOT EXISTS "slots_payload_status_idx"
    ON public."slots"
    ("payload_status" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "slots_builder_index_idx"
    ON public."slots"
    ("builder_index" ASC NULLS LAST);

ALTER TABLE public."epochs" ADD
 "payload_count" int NOT NULL DEFAULT 0;

ALTER TABLE public."unfinalized_epochs" ADD
 "payload_count" int NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS public."block_bids" (
    "parent_root" bytea NOT NULL,
    "parent_hash" bytea NOT NULL,
    "block_hash" bytea NOT NULL,
    "fee_recipient" bytea NOT NULL,
    "gas_limit" bigint NOT NULL,
    "builder_index" bigint NOT NULL,
    "slot" bigint NOT NULL,
    "value" bigint NOT NULL,
    "el_payment" bigint NOT NULL,
    CONSTRAINT block_bids_pkey PRIMARY KEY (parent_root, parent_hash, block_hash, builder_index)
);

CREATE INDEX IF NOT EXISTS "block_bids_parent_root_idx"
    ON public."block_bids"
    ("parent_root" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "block_bids_builder_index_idx"
    ON public."block_bids"
    ("builder_index" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "block_bids_slot_idx"
    ON public."block_bids"
    ("slot" ASC NULLS LAST);

CREATE TABLE IF NOT EXISTS public."builders" (
    "pubkey" bytea NOT NULL,
    "builder_index" bigint NOT NULL,
    "version" smallint NOT NULL,
    "execution_address" bytea NOT NULL,
    "deposit_epoch" bigint NOT NULL,
    "withdrawable_epoch" bigint NOT NULL,
    "superseded" boolean NOT NULL DEFAULT false,
    CONSTRAINT builders_pkey PRIMARY KEY (pubkey)
);

CREATE INDEX IF NOT EXISTS "builders_builder_index_idx"
    ON public."builders"
    ("builder_index" ASC NULLS LAST);

CREATE INDEX IF NOT EXISTS "builders_execution_address_idx"
    ON public."builders"
    ("execution_address" ASC NULLS LAST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd