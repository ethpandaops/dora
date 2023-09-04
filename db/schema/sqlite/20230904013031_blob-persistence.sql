-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "blobs"
(
    "commitment" BLOB NOT NULL,
    "slot" bigint NOT NULL,
    "root" BLOB NOT NULL,
    "proof" BLOB NOT NULL,
    "size" INT NOT NULL,
    "blob" BLOB NULL,
    PRIMARY KEY ("commitment")
);

CREATE INDEX IF NOT EXISTS "blobs_root_idx"
    ON "blobs" 
    ("root" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
