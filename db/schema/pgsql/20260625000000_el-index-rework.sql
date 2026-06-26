-- +goose Up
-- +goose StatementBegin

-- EL index overhead reduction, in one migration:
--   1. Replace the el_event_index table with a per-tx event_count badge column.
--   2. Add the long-lived tx_hash -> tx_uid index (el_txhash), drop the dedicated
--      tx_hash column index, and switch the el_transactions primary key to tx_uid.
-- This rewrites el_transactions rows and scans the whole table; run during a
-- maintenance window and VACUUM afterwards.

-- 1a. event_count badge column. Full event data lives in blockdb; the only
-- readers of el_event_index were a count badge and a degraded fallback, and its
-- source_id/topic1 indexes backed no live feature.
ALTER TABLE public."el_transactions" ADD COLUMN IF NOT EXISTS event_count SMALLINT NOT NULL DEFAULT 0;

-- 1b. Backfill the badge count from the existing index before dropping it so the
-- events tab keeps working for already-indexed transactions.
UPDATE public."el_transactions" t
SET event_count = LEAST(c.cnt, 32767)
FROM (
    SELECT tx_uid, COUNT(*) AS cnt
    FROM public."el_event_index"
    GROUP BY tx_uid
) c
WHERE c.tx_uid = t.tx_uid;

DROP TABLE IF EXISTS public."el_event_index";

-- 1c. Drop now-unused single-column from/to indexes (covered by the composite
-- (from_id, block_uid) / (to_id, block_uid) indexes via their leading column).
DROP INDEX IF EXISTS public."el_transactions_from_idx";
DROP INDEX IF EXISTS public."el_transactions_to_idx";

-- 2a. Long-lived tx_hash -> tx_uid index. Decouples by-hash lookups from the
-- relational el_transactions rows so /tx/{hash} keeps working after those rows
-- are pruned (the tx is then reconstructed from blockdb). Stores a 10-byte hash
-- prefix; full-hash disambiguation happens in the application layer.
CREATE TABLE IF NOT EXISTS public."el_txhash" (
    hash10 BYTEA NOT NULL,
    tx_uid BIGINT NOT NULL,
    CONSTRAINT el_txhash_pkey PRIMARY KEY (hash10, tx_uid)
);

-- 2b. Backfill from existing transactions. substr() over BYTEA returns BYTEA, so
-- the 10-byte prefix is computed in-database.
INSERT INTO public."el_txhash" (hash10, tx_uid)
    SELECT substr(tx_hash, 1, 10), tx_uid FROM public."el_transactions"
    ON CONFLICT (hash10, tx_uid) DO NOTHING;

-- 2c. The by-hash path moves to el_txhash; drop the dedicated tx_hash index.
DROP INDEX IF EXISTS public."el_transactions_tx_hash_idx";

-- 2d. Switch the primary key from (block_uid, tx_hash) to tx_uid by promoting the
-- existing unique index (cheap: no table/index rebuild). tx_hash stays a plain
-- column for list display.
ALTER TABLE public."el_transactions" DROP CONSTRAINT IF EXISTS el_transactions_pkey;
ALTER TABLE public."el_transactions" ADD CONSTRAINT el_transactions_pkey PRIMARY KEY USING INDEX "el_transactions_tx_uid_idx";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
