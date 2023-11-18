-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "tx_function_signatures"
(
    "signature" TEXT NOT NULL,
    "bytes" BLOB NOT NULL,
    "name" TEXT NOT NULL,
    PRIMARY KEY ("signature")
);

CREATE INDEX IF NOT EXISTS "tx_function_signatures_bytes_idx"
    ON "tx_function_signatures" 
    ("bytes" ASC);

CREATE TABLE IF NOT EXISTS "tx_unknown_signatures"
(
    "bytes" bytea NOT NULL,
    "lastcheck" bigint NOT NULL,
    PRIMARY KEY ("bytes")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
