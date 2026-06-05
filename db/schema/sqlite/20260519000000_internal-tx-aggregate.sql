-- +goose Up
-- +goose StatementBegin

-- Aggregate el_transactions_internal: collapse the per-call index (one row per
-- sub-call frame) into a per-account aggregate (one row per touched account
-- per transaction). Internal-call-heavy txs (eg. 100 calls to the same
-- contract) drop from 100 rows to ~2 rows, reducing per-slot insert pressure.
--
-- Historical migration preserves in/out call counts, value_in/out and
-- call_type_mask (rebuilt from the old per-row call_type via
-- SUM(DISTINCT 1<<call_type) — since each bit is a distinct power of two,
-- summing distinct values equals bitwise OR). gas_used is zeroed because the
-- old schema didn't track it; it only repopulates when affected blocks are
-- re-indexed.

DROP INDEX IF EXISTS "el_internal_tx_from_idx";
DROP INDEX IF EXISTS "el_internal_tx_to_idx";
DROP INDEX IF EXISTS "el_internal_tx_from_only_idx";
DROP INDEX IF EXISTS "el_internal_tx_to_only_idx";
DROP INDEX IF EXISTS "el_internal_tx_hash_idx";

CREATE TABLE "el_transactions_internal_new" (
    tx_uid INTEGER NOT NULL,
    account_id INTEGER NOT NULL,
    in_count INTEGER NOT NULL DEFAULT 0,
    out_count INTEGER NOT NULL DEFAULT 0,
    call_type_mask INTEGER NOT NULL DEFAULT 0,
    value_in REAL NOT NULL DEFAULT 0,
    value_out REAL NOT NULL DEFAULT 0,
    gas_used INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT el_transactions_internal_pkey PRIMARY KEY (tx_uid, account_id)
);

-- SQLite stores INTEGERs flexibly so the SMALLINT type hint isn't enforced;
-- no clamp needed here. The indexer clamps counts on insert.
INSERT INTO "el_transactions_internal_new"
    (tx_uid, account_id, in_count, out_count, call_type_mask, value_in, value_out, gas_used)
SELECT tx_uid, account_id,
       SUM(in_count) AS in_count,
       SUM(out_count) AS out_count,
       SUM(call_type_mask) AS call_type_mask,
       SUM(value_in) AS value_in,
       SUM(value_out) AS value_out,
       0 AS gas_used
FROM (
    -- from-side: every internal call contributes to the caller's out_count.
    -- call_type_mask stays 0 here since the mask only tracks incoming types.
    SELECT tx_uid, from_id AS account_id,
           0 AS in_count,
           COUNT(*) AS out_count,
           0 AS call_type_mask,
           0 AS value_in,
           SUM(value) AS value_out
    FROM "el_transactions_internal"
    GROUP BY tx_uid, from_id
    UNION ALL
    -- to-side: every internal call contributes to the callee's in_count.
    -- SUM(DISTINCT 1<<call_type) reconstructs the bitmask of incoming types.
    SELECT tx_uid, to_id AS account_id,
           COUNT(*) AS in_count,
           0 AS out_count,
           SUM(DISTINCT (1 << call_type)) AS call_type_mask,
           SUM(value) AS value_in,
           0 AS value_out
    FROM "el_transactions_internal"
    GROUP BY tx_uid, to_id
) sides
GROUP BY tx_uid, account_id;

DROP TABLE "el_transactions_internal";
ALTER TABLE "el_transactions_internal_new" RENAME TO "el_transactions_internal";

CREATE INDEX IF NOT EXISTS "el_internal_tx_account_idx"
    ON "el_transactions_internal" ("account_id" ASC, "tx_uid" DESC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
