-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."tx_function_signatures"
(
    "bytes" bytea NOT NULL,
    "signature" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    PRIMARY KEY ("bytes")
);

CREATE TABLE IF NOT EXISTS public."tx_unknown_signatures"
(
    "bytes" bytea NOT NULL,
    "lastcheck" bigint NOT NULL,
    PRIMARY KEY ("bytes")
);

CREATE TABLE IF NOT EXISTS public."tx_pending_signatures"
(
    "bytes" bytea NOT NULL,
    "queuetime" bigint NOT NULL,
    PRIMARY KEY ("bytes")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
