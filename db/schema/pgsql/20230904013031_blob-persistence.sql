-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."blobs"
(
    "commitment" bytea NOT NULL,
    "slot" bigint NOT NULL,
    "root" bytea NOT NULL,
    "proof" bytea NOT NULL,
    "size" INT NOT NULL,
    "blob" bytea NULL,
    PRIMARY KEY ("commitment")
);

CREATE INDEX IF NOT EXISTS "blobs_root_idx"
    ON public."blobs" 
    ("root" ASC NULLS LAST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
