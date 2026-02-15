-- +goose Up
-- +goose StatementBegin

-- SQLite does not support ALTER COLUMN, so we need to recreate the table.
CREATE TABLE IF NOT EXISTS "el_tx_events_new" (
    block_uid INTEGER NOT NULL,
    tx_hash BLOB NOT NULL,
    event_index INTEGER NOT NULL,
    source_id INTEGER NOT NULL,
    topic1 BLOB NULL,
    topic2 BLOB NULL,
    topic3 BLOB NULL,
    topic4 BLOB NULL,
    topic5 BLOB NULL,
    data BLOB NULL,
    CONSTRAINT el_tx_events_pkey PRIMARY KEY (block_uid, tx_hash, event_index)
);

INSERT INTO "el_tx_events_new" SELECT * FROM "el_tx_events";
DROP TABLE "el_tx_events";
ALTER TABLE "el_tx_events_new" RENAME TO "el_tx_events";

CREATE INDEX IF NOT EXISTS "el_tx_events_block_uid_idx"
    ON "el_tx_events"
    ("block_uid" ASC);

CREATE INDEX IF NOT EXISTS "el_tx_events_tx_hash_idx"
    ON "el_tx_events"
    ("tx_hash" ASC);

CREATE INDEX IF NOT EXISTS "el_tx_events_source_idx"
    ON "el_tx_events"
    ("source_id" ASC);

CREATE INDEX IF NOT EXISTS "el_tx_events_topic1_idx"
    ON "el_tx_events"
    ("topic1" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
