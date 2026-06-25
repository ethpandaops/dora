-- +goose Up
-- +goose StatementBegin

-- Replace the el_event_index table with a per-tx event_count badge column on
-- el_transactions. Full event data lives in blockdb; the only readers of
-- el_event_index were a count badge and a degraded fallback, and its
-- source_id/topic1 indexes backed no live feature.
ALTER TABLE public."el_transactions" ADD COLUMN IF NOT EXISTS event_count SMALLINT NOT NULL DEFAULT 0;

-- Backfill the badge count from the existing index before dropping it so the
-- events tab keeps working for already-indexed transactions. One-time pass;
-- rewrites el_transactions rows, so run during a maintenance window and VACUUM
-- afterwards.
UPDATE public."el_transactions" t
SET event_count = LEAST(c.cnt, 32767)
FROM (
    SELECT tx_uid, COUNT(*) AS cnt
    FROM public."el_event_index"
    GROUP BY tx_uid
) c
WHERE c.tx_uid = t.tx_uid;

DROP TABLE IF EXISTS public."el_event_index";

-- Drop now-unused single-column from/to indexes. Account queries use the
-- composite (from_id, block_uid) / (to_id, block_uid) indexes, which also
-- serve from_id-only / to_id-only lookups via their leading column.
DROP INDEX IF EXISTS public."el_transactions_from_idx";
DROP INDEX IF EXISTS public."el_transactions_to_idx";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
