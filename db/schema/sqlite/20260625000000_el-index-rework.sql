-- +goose Up
-- +goose StatementBegin

-- EL index overhead reduction, in one migration:
--   1. Replace the el_event_index table with a per-tx event_count badge column.
--   2. Add the long-lived tx_hash -> tx_uid index (el_txhash) and drop the
--      dedicated tx_hash column index.
--   3. Deduplicate revert reasons into el_revert_reason and replace the
--      el_transactions.reverted bool with a revert_id.

-- 1a. event_count badge column. Full event data lives in blockdb; the only
-- readers of el_event_index were a count badge and a degraded fallback, and its
-- source_id/topic1 indexes backed no live feature.
ALTER TABLE el_transactions ADD COLUMN event_count SMALLINT NOT NULL DEFAULT 0;

-- 1b. Backfill the badge count from the existing index before dropping it.
UPDATE el_transactions
SET event_count = MIN(
    (SELECT COUNT(*) FROM el_event_index WHERE el_event_index.tx_uid = el_transactions.tx_uid),
    32767
);

DROP TABLE IF EXISTS el_event_index;

-- 1c. Drop now-unused single-column from/to indexes (covered by the composite
-- (from_id, block_uid) / (to_id, block_uid) indexes via their leading column).
DROP INDEX IF EXISTS el_transactions_from_idx;
DROP INDEX IF EXISTS el_transactions_to_idx;

-- 2a. Long-lived tx_hash -> tx_uid index. Decouples by-hash lookups from the
-- relational el_transactions rows so /tx/{hash} keeps working after those rows
-- are pruned (the tx is then reconstructed from blockdb). Stores a 10-byte hash
-- prefix; full-hash disambiguation happens in the application layer.
CREATE TABLE IF NOT EXISTS "el_txhash" (
    hash10 BLOB NOT NULL,
    tx_uid INTEGER NOT NULL,
    CONSTRAINT el_txhash_pkey PRIMARY KEY (hash10, tx_uid)
);

-- 2b. Backfill from existing transactions. substr() over BLOB returns BLOB.
INSERT OR IGNORE INTO "el_txhash" (hash10, tx_uid)
    SELECT substr(tx_hash, 1, 10), tx_uid FROM "el_transactions";

-- 2c. The by-hash path moves to el_txhash; drop the dedicated tx_hash index.
DROP INDEX IF EXISTS "el_transactions_tx_hash_idx";

-- Note: unlike pgsql, the el_transactions primary key is left as
-- (block_uid, tx_hash). Swapping it to tx_uid in SQLite requires a full table
-- rebuild (CREATE/copy/DROP/RENAME) which is heavy and unnecessary for the small
-- deployments that use SQLite. The unique "el_transactions_tx_uid_idx" already
-- backs ON CONFLICT/INSERT OR REPLACE on tx_uid.

-- 3a. Deduplicated revert reasons. reason_hash is the first 16 bytes of
-- sha256(reason) — a 128-bit dedup key. Reserved ids 1-9 are fixed sentinels;
-- dynamic reasons start at 10. last_tx_uid records the highest referencing
-- tx_uid for orphan reclaim (see the pgsql migration).
CREATE TABLE IF NOT EXISTS "el_revert_reason" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reason TEXT NOT NULL,
    reason_hash BLOB NOT NULL,
    last_tx_uid INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS "el_revert_reason_hash_uniq" ON "el_revert_reason" ("reason_hash");
CREATE INDEX IF NOT EXISTS "el_revert_reason_last_tx_uid_idx" ON "el_revert_reason" ("last_tx_uid");

-- Reserved sentinels (ids 1-99) for well-known errors. The explicit inserts
-- create the AUTOINCREMENT sqlite_sequence row; setting seq=99 makes the next
-- auto-assigned id 100.
INSERT OR IGNORE INTO "el_revert_reason" (id, reason, reason_hash, last_tx_uid) VALUES
    (1, 'unknown',         X'00000000000000000000000000000001', 0),
    (2, 'out of gas',      X'00000000000000000000000000000002', 0),
    (3, 'stack underflow', X'00000000000000000000000000000003', 0),
    (4, 'invalid opcode',  X'00000000000000000000000000000004', 0);
UPDATE sqlite_sequence SET seq = 99 WHERE name = 'el_revert_reason';

-- 3b. Replace el_transactions.reverted (bool) with revert_id (0 = success,
-- 1 = reverted/unknown, >= 10 = deduped reason).
ALTER TABLE "el_transactions" ADD COLUMN revert_id INTEGER NOT NULL DEFAULT 0;
UPDATE "el_transactions" SET revert_id = 1 WHERE reverted;
ALTER TABLE "el_transactions" DROP COLUMN reverted;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
