-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."tx_function_signatures"
(
    "signature" TEXT NOT NULL,
    "bytes" bytea NOT NULL,
    "name" TEXT NOT NULL,
    PRIMARY KEY ("signature")
);

CREATE INDEX IF NOT EXISTS "tx_function_signatures_bytes_idx"
    ON public."tx_function_signatures" 
    ("bytes" ASC NULLS LAST);


CREATE TABLE IF NOT EXISTS public."tx_unknown_signatures"
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
