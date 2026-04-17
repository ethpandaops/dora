-- +goose Up
-- +goose StatementBegin

-- Introduce tx_uid = block_uid << 16 | tx_index as a compact unique transaction
-- identifier.  Dependent tables switch from (block_uid, tx_hash) to tx_uid.

-- Step 1: Add tx_uid column to el_transactions ----------------------------------
ALTER TABLE "el_transactions" ADD COLUMN tx_uid INTEGER NOT NULL DEFAULT 0;
UPDATE "el_transactions" SET tx_uid = (block_uid << 16) | tx_index;
CREATE UNIQUE INDEX IF NOT EXISTS "el_transactions_tx_uid_idx"
    ON "el_transactions" ("tx_uid");

-- Step 2: Recreate el_event_index with tx_uid -----------------------------------
DROP INDEX IF EXISTS "el_event_index_source_idx";
DROP INDEX IF EXISTS "el_event_index_topic1_idx";
DROP INDEX IF EXISTS "el_event_index_tx_hash_idx";

CREATE TABLE "el_event_index_new" (
    tx_uid INTEGER NOT NULL,
    event_index INTEGER NOT NULL,
    source_id INTEGER NOT NULL DEFAULT 0,
    topic1 BLOB,
    CONSTRAINT el_event_index_pkey PRIMARY KEY (tx_uid, event_index)
);

INSERT INTO "el_event_index_new" (tx_uid, event_index, source_id, topic1)
SELECT t.tx_uid, e.event_index, e.source_id, e.topic1
FROM "el_event_index" e
JOIN "el_transactions" t
    ON e.block_uid = t.block_uid AND e.tx_hash = t.tx_hash;

DROP TABLE "el_event_index";
ALTER TABLE "el_event_index_new" RENAME TO "el_event_index";

CREATE INDEX IF NOT EXISTS "el_event_index_source_idx"
    ON "el_event_index" ("source_id" ASC, "tx_uid" DESC);
CREATE INDEX IF NOT EXISTS "el_event_index_topic1_idx"
    ON "el_event_index" ("topic1" ASC, "tx_uid" DESC);

-- Step 3: Recreate el_token_transfers with tx_uid --------------------------------
DROP INDEX IF EXISTS "el_token_transfers_block_uid_idx";
DROP INDEX IF EXISTS "el_token_transfers_tx_hash_idx";
DROP INDEX IF EXISTS "el_token_transfers_token_id_idx";
DROP INDEX IF EXISTS "el_token_transfers_token_type_idx";
DROP INDEX IF EXISTS "el_token_transfers_token_index_idx";
DROP INDEX IF EXISTS "el_token_transfers_from_idx";
DROP INDEX IF EXISTS "el_token_transfers_to_idx";
DROP INDEX IF EXISTS "el_token_transfers_token_block_idx";
DROP INDEX IF EXISTS "el_token_transfers_from_block_idx";
DROP INDEX IF EXISTS "el_token_transfers_to_block_idx";
DROP INDEX IF EXISTS "el_token_transfers_from_type_idx";
DROP INDEX IF EXISTS "el_token_transfers_to_type_idx";

CREATE TABLE "el_token_transfers_new" (
    tx_uid INTEGER NOT NULL,
    tx_idx INTEGER NOT NULL,
    token_id INTEGER NOT NULL,
    token_type INTEGER NOT NULL DEFAULT 0,
    token_index BLOB NULL,
    from_id INTEGER NOT NULL,
    to_id INTEGER NOT NULL,
    amount REAL NOT NULL DEFAULT 0,
    amount_raw BLOB NOT NULL,
    CONSTRAINT el_token_transfers_pkey PRIMARY KEY (tx_uid, tx_idx)
);

INSERT INTO "el_token_transfers_new"
    (tx_uid, tx_idx, token_id, token_type, token_index, from_id, to_id, amount, amount_raw)
SELECT t.tx_uid, tt.tx_idx, tt.token_id, tt.token_type, tt.token_index,
       tt.from_id, tt.to_id, tt.amount, tt.amount_raw
FROM "el_token_transfers" tt
JOIN "el_transactions" t
    ON tt.block_uid = t.block_uid AND tt.tx_hash = t.tx_hash;

DROP TABLE "el_token_transfers";
ALTER TABLE "el_token_transfers_new" RENAME TO "el_token_transfers";

CREATE INDEX IF NOT EXISTS "el_token_transfers_token_uid_idx"
    ON "el_token_transfers" ("token_id" ASC, "tx_uid" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_from_uid_idx"
    ON "el_token_transfers" ("from_id" ASC, "tx_uid" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_to_uid_idx"
    ON "el_token_transfers" ("to_id" ASC, "tx_uid" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_from_type_idx"
    ON "el_token_transfers" ("from_id" ASC, "token_type" ASC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_to_type_idx"
    ON "el_token_transfers" ("to_id" ASC, "token_type" ASC);

-- Step 4: Recreate el_transactions_internal with tx_uid --------------------------
DROP INDEX IF EXISTS "el_internal_tx_from_idx";
DROP INDEX IF EXISTS "el_internal_tx_to_idx";
DROP INDEX IF EXISTS "el_internal_tx_hash_idx";
DROP INDEX IF EXISTS "el_internal_tx_from_only_idx";
DROP INDEX IF EXISTS "el_internal_tx_to_only_idx";

CREATE TABLE "el_transactions_internal_new" (
    tx_uid INTEGER NOT NULL,
    tx_callidx INTEGER NOT NULL,
    call_type INTEGER NOT NULL DEFAULT 0,
    from_id INTEGER NOT NULL DEFAULT 0,
    to_id INTEGER NOT NULL DEFAULT 0,
    value REAL NOT NULL DEFAULT 0,
    value_raw BLOB,
    CONSTRAINT el_transactions_internal_pkey PRIMARY KEY (tx_uid, tx_callidx)
);

INSERT INTO "el_transactions_internal_new"
    (tx_uid, tx_callidx, call_type, from_id, to_id, value, value_raw)
SELECT t.tx_uid, ti.tx_callidx, ti.call_type, ti.from_id, ti.to_id,
       ti.value, ti.value_raw
FROM "el_transactions_internal" ti
JOIN "el_transactions" t
    ON ti.block_uid = t.block_uid AND ti.tx_hash = t.tx_hash;

DROP TABLE "el_transactions_internal";
ALTER TABLE "el_transactions_internal_new" RENAME TO "el_transactions_internal";

CREATE INDEX IF NOT EXISTS "el_internal_tx_from_idx"
    ON "el_transactions_internal" ("from_id" ASC, "tx_uid" DESC);
CREATE INDEX IF NOT EXISTS "el_internal_tx_to_idx"
    ON "el_transactions_internal" ("to_id" ASC, "tx_uid" DESC);
CREATE INDEX IF NOT EXISTS "el_internal_tx_from_only_idx"
    ON "el_transactions_internal" ("from_id" ASC);
CREATE INDEX IF NOT EXISTS "el_internal_tx_to_only_idx"
    ON "el_transactions_internal" ("to_id" ASC);

-- Step 5: Drop tx_index from el_transactions (redundant with tx_uid & 0xFFFF) ---
-- SQLite 3.35.0+ supports ALTER TABLE DROP COLUMN
ALTER TABLE "el_transactions" DROP COLUMN tx_index;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
