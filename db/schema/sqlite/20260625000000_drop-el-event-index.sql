-- +goose Up
-- +goose StatementBegin

-- Replace the el_event_index table with a per-tx event_count badge column on
-- el_transactions. Full event data lives in blockdb; the only readers of
-- el_event_index were a count badge and a degraded fallback, and its
-- source_id/topic1 indexes backed no live feature.
ALTER TABLE el_transactions ADD COLUMN event_count SMALLINT NOT NULL DEFAULT 0;

-- Backfill the badge count from the existing index before dropping it so the
-- events tab keeps working for already-indexed transactions.
UPDATE el_transactions
SET event_count = MIN(
    (SELECT COUNT(*) FROM el_event_index WHERE el_event_index.tx_uid = el_transactions.tx_uid),
    32767
);

DROP TABLE IF EXISTS el_event_index;

-- Drop now-unused single-column from/to indexes (covered by the composite
-- (from_id, block_uid) / (to_id, block_uid) indexes via their leading column).
DROP INDEX IF EXISTS el_transactions_from_idx;
DROP INDEX IF EXISTS el_transactions_to_idx;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
