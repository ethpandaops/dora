-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "tx_function_signatures"
(
    "bytes" BLOB NOT NULL,
    "signature" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    PRIMARY KEY ("bytes")
);

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
